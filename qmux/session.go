package qmux

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/okdaichi/qmux-go/qmux/internal/flowcontrol"
	"github.com/okdaichi/qmux-go/qmux/internal/wire"
	"github.com/quic-go/quic-go"
)

// Conn is a QMux connection.
// It implements the quic.Connection interface.
type Conn struct {
	conn     net.Conn
	config   *Config
	isServer bool

	rr *wire.RecordReader
	rw *wire.RecordWriter

	ctx       context.Context
	cancelCtx context.CancelFunc

	mutex      sync.Mutex
	writeMutex sync.Mutex
	streams    map[StreamID]*baseStream

	nextBidiStreamID StreamID
	nextUniStreamID  StreamID

	// Peer stream limits
	peerMaxBidiStreams uint64
	peerMaxUniStreams  uint64
	openedBidiStreams  uint64
	openedUniStreams   uint64
	streamLimitWake    chan struct{}

	incomingBidi chan *Stream
	incomingUni  chan *ReceiveStream

	connFC *flowcontrol.FlowController

	handshakeDone chan struct{}
	closeErr      error

	lastFrameTime time.Time
	idleTimeout   time.Duration

	// sendQueue for asynchronous frame sending
	sendQueue chan wire.Frame
}

func newSession(conn net.Conn, config *Config, isServer bool) *Conn {
	if config == nil {
		config = &Config{
			MaxIncomingStreams:             100,
			MaxIncomingUniStreams:          100,
			InitialStreamReceiveWindow:     65536,
			InitialConnectionReceiveWindow: 65536,
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &Conn{
		conn:            conn,
		config:          config,
		isServer:        isServer,
		rr:              wire.NewRecordReader(conn),
		rw:              wire.NewRecordWriter(conn),
		ctx:             ctx,
		cancelCtx:       cancel,
		streams:         make(map[StreamID]*baseStream),
		incomingBidi:    make(chan *Stream, int(config.MaxIncomingStreams)),
		incomingUni:     make(chan *ReceiveStream, int(config.MaxIncomingUniStreams)),
		connFC:          flowcontrol.NewFlowController(config.InitialConnectionReceiveWindow),
		handshakeDone:   make(chan struct{}),
		streamLimitWake: make(chan struct{}),
		lastFrameTime:   time.Now(),
		sendQueue:       make(chan wire.Frame, 1024),
	}

	if isServer {
		s.nextBidiStreamID = 1
		s.nextUniStreamID = 3
	} else {
		s.nextBidiStreamID = 0
		s.nextUniStreamID = 2
	}

	return s
}

func (s *Conn) run() {
	defer s.Close()

	go s.writeLoop()

	if err := s.handshake(); err != nil {
		s.CloseWithError(0, err.Error())
		return
	}
	close(s.handshakeDone)

	if s.config.KeepAlivePeriod > 0 {
		go func() {
			ticker := time.NewTicker(s.config.KeepAlivePeriod)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					s.queueFrame(&wire.QXPingFrame{
						TypeField: wire.FrameTypeQXPingRequest,
						Sequence:  uint64(time.Now().UnixNano()),
					})
				case <-s.ctx.Done():
					return
				}
			}
		}()
	}

	if s.idleTimeout > 0 {
		go func() {
			ticker := time.NewTicker(s.idleTimeout / 2)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					s.mutex.Lock()
					if time.Since(s.lastFrameTime) > s.idleTimeout {
						s.mutex.Unlock()
						s.CloseWithError(0, "idle timeout")
						return
					}
					s.mutex.Unlock()
				case <-s.ctx.Done():
					return
				}
			}
		}()
	}

	for {
		frames, err := s.rr.ReadRecord()
		if err != nil {
			s.CloseWithError(0, err.Error())
			return
		}

		s.mutex.Lock()
		s.lastFrameTime = time.Now()
		s.mutex.Unlock()

		for _, f := range frames {
			if err := s.handleFrame(f); err != nil {
				s.CloseWithError(0, err.Error())
				return
			}
		}
	}
}

func (s *Conn) writeLoop() {
	for {
		select {
		case f := <-s.sendQueue:
			if err := s.writeFrame(f); err != nil {
				s.CloseWithError(0, err.Error())
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Conn) writeFrame(f wire.Frame) error {
	s.writeMutex.Lock()
	defer s.writeMutex.Unlock()
	return s.rw.WriteRecord(f)
}

func (s *Conn) handshake() error {
	params := []wire.TransportParameter{
		{ID: wire.TransportParameterInitialMaxData, Value: encodeVarInt(s.config.InitialConnectionReceiveWindow)},
		{ID: wire.TransportParameterInitialMaxStreamDataBidiLocal, Value: encodeVarInt(s.config.InitialStreamReceiveWindow)},
		{ID: wire.TransportParameterInitialMaxStreamsBidi, Value: encodeVarInt(uint64(s.config.MaxIncomingStreams))},
		{ID: wire.TransportParameterInitialMaxStreamsUni, Value: encodeVarInt(uint64(s.config.MaxIncomingUniStreams))},
	}
	if s.config.MaxRecordSize > 0 {
		params = append(params, wire.TransportParameter{ID: wire.TransportParameterMaxRecordSize, Value: encodeVarInt(s.config.MaxRecordSize)})
	}

	writeErrChan := make(chan error, 1)
	go func() {
		writeErrChan <- s.writeFrame(&wire.QXTransportParametersFrame{Parameters: params})
	}()

	frames, err := s.rr.ReadRecord()
	if err != nil {
		return err
	}
	if len(frames) == 0 {
		return &QMuxError{ErrorCode: TransportParameterError, Message: "empty initial record"}
	}
	f, ok := frames[0].(*wire.QXTransportParametersFrame)
	if !ok {
		return &QMuxError{ErrorCode: TransportParameterError, Message: "first frame is not QX_TRANSPORT_PARAMETERS"}
	}

	for _, p := range f.Parameters {
		val, _ := wire.ReadVarInt(bytes.NewReader(p.Value))
		switch p.ID {
		case wire.TransportParameterInitialMaxData:
			s.connFC.UpdateSendWindow(val)
		case wire.TransportParameterInitialMaxStreamsBidi:
			s.mutex.Lock()
			s.peerMaxBidiStreams = val
			s.mutex.Unlock()
		case wire.TransportParameterInitialMaxStreamsUni:
			s.mutex.Lock()
			s.peerMaxUniStreams = val
			s.mutex.Unlock()
		case wire.TransportParameterMaxIdleTimeout:
			s.idleTimeout = time.Duration(val) * time.Millisecond
		}
	}

	return <-writeErrChan
}

func (s *Conn) handleFrame(f wire.Frame) error {
	switch ff := f.(type) {
	case *wire.StreamFrame:
		str, err := s.getOrCreateBaseStream(StreamID(ff.StreamID))
		if err != nil {
			return err
		}
		return str.handleStreamFrame(ff)
	case *wire.ResetStreamFrame:
		str, err := s.getOrCreateBaseStream(StreamID(ff.StreamID))
		if err != nil {
			return err
		}
		str.handleResetStreamFrame(ff)
	case *wire.StopSendingFrame:
		str, err := s.getOrCreateBaseStream(StreamID(ff.StreamID))
		if err != nil {
			return err
		}
		str.handleStopSendingFrame(ff)
	case *wire.MaxDataFrame:
		s.connFC.UpdateSendWindow(ff.MaximumData)
	case *wire.MaxStreamDataFrame:
		str := s.getBaseStream(StreamID(ff.StreamID))
		if str != nil {
			str.sendFC.UpdateSendWindow(ff.MaximumStreamData)
		}
	case *wire.QXTransportParametersFrame:
		return &QMuxError{ErrorCode: ProtocolViolationError, Message: "duplicate QX_TRANSPORT_PARAMETERS"}
	case *wire.MaxStreamsFrame:
		s.mutex.Lock()
		if ff.TypeField == wire.FrameTypeMaxStreamsBi {
			s.peerMaxBidiStreams = ff.MaximumStreams
		} else {
			s.peerMaxUniStreams = ff.MaximumStreams
		}
		close(s.streamLimitWake)
		s.streamLimitWake = make(chan struct{})
		s.mutex.Unlock()
	case *wire.StreamsBlockedFrame:
		return nil
	case *wire.QXPingFrame:
		if ff.TypeField == wire.FrameTypeQXPingRequest {
			s.queueFrame(&wire.QXPingFrame{
				TypeField: wire.FrameTypeQXPingResponse,
				Sequence:  ff.Sequence,
			})
		}
	case *wire.ConnectionCloseFrame:
		return &quic.ApplicationError{ErrorCode: quic.ApplicationErrorCode(ff.ErrorCode), ErrorMessage: ff.ReasonPhrase}
	}
	return nil
}

func (s *Conn) getBaseStream(id StreamID) *baseStream {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.streams[id]
}

func (s *Conn) getOrCreateBaseStream(id StreamID) (*baseStream, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if str, ok := s.streams[id]; ok {
		return str, nil
	}

	str := newBaseStream(id, s, s.config.InitialStreamReceiveWindow, s.config.InitialStreamReceiveWindow)
	s.streams[id] = str

	if id%4 == 0 || id%4 == 1 {
		select {
		case s.incomingBidi <- &Stream{baseStream: str}:
		default:
			return nil, &QMuxError{ErrorCode: StreamLimitError, Message: "too many concurrent streams"}
		}
	} else {
		select {
		case s.incomingUni <- &ReceiveStream{baseStream: str}:
		default:
			return nil, &QMuxError{ErrorCode: StreamLimitError, Message: "too many concurrent uni streams"}
		}
	}
	return str, nil
}

func (s *Conn) sendFrame(f wire.Frame) error {
	select {
	case s.sendQueue <- f:
		return nil
	case <-s.ctx.Done():
		return s.closeErr
	}
}

func (s *Conn) queueFrame(f wire.Frame) {
	select {
	case s.sendQueue <- f:
	default:
	}
}

// Close closes the connection.
func (s *Conn) Close() error {
	return s.CloseWithError(0, "")
}

// CloseWithError closes the connection with an error code and a reason phrase.
func (s *Conn) CloseWithError(code quic.ApplicationErrorCode, msg string) error {
	s.mutex.Lock()
	if s.closeErr != nil {
		s.mutex.Unlock()
		return nil
	}
	s.closeErr = &quic.ApplicationError{ErrorCode: code, ErrorMessage: msg}
	s.cancelCtx()

	for _, str := range s.streams {
		str.handleResetStreamFrame(&wire.ResetStreamFrame{ErrorCode: uint64(code)})
	}
	close(s.streamLimitWake)
	s.mutex.Unlock()

	s.writeFrame(&wire.ConnectionCloseFrame{
		TypeField:    wire.FrameTypeApplicationClose,
		ErrorCode:    uint64(code),
		ReasonPhrase: msg,
	})
	return s.conn.Close()
}

// AcceptStream accepts the next incoming bidirectional stream.
func (s *Conn) AcceptStream(ctx context.Context) (*Stream, error) {
	select {
	case str := <-s.incomingBidi:
		return str, nil
	case <-s.ctx.Done():
		if s.closeErr != nil {
			return nil, s.closeErr
		}
		return nil, errors.New("connection closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// AcceptUniStream accepts the next incoming unidirectional stream.
func (s *Conn) AcceptUniStream(ctx context.Context) (*ReceiveStream, error) {
	select {
	case str := <-s.incomingUni:
		return str, nil
	case <-s.ctx.Done():
		if s.closeErr != nil {
			return nil, s.closeErr
		}
		return nil, errors.New("connection closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// OpenStream opens a new bidirectional stream.
func (s *Conn) OpenStream() (*Stream, error) {
	return s.OpenStreamSync(context.Background())
}

// OpenStreamSync opens a new bidirectional stream, blocking until one is available.
func (s *Conn) OpenStreamSync(ctx context.Context) (*Stream, error) {
	for {
		s.mutex.Lock()
		if s.openedBidiStreams < s.peerMaxBidiStreams {
			id := s.nextBidiStreamID
			s.nextBidiStreamID += 4
			s.openedBidiStreams++
			str := newBaseStream(id, s, s.config.InitialStreamReceiveWindow, s.config.InitialStreamReceiveWindow)
			s.streams[id] = str
			s.mutex.Unlock()
			return &Stream{baseStream: str}, nil
		}

		wake := s.streamLimitWake
		s.mutex.Unlock()

		s.queueFrame(&wire.StreamsBlockedFrame{
			TypeField:      wire.FrameTypeStreamsBlockedBi,
			MaximumStreams: s.openedBidiStreams,
		})

		select {
		case <-wake:
		case <-s.ctx.Done():
			return nil, s.closeErr
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// OpenUniStream opens a new unidirectional stream.
func (s *Conn) OpenUniStream() (*SendStream, error) {
	return s.OpenUniStreamSync(context.Background())
}

// OpenUniStreamSync opens a new unidirectional stream, blocking until one is available.
func (s *Conn) OpenUniStreamSync(ctx context.Context) (*SendStream, error) {
	for {
		s.mutex.Lock()
		if s.openedUniStreams < s.peerMaxUniStreams {
			id := s.nextUniStreamID
			s.nextUniStreamID += 4
			s.openedUniStreams++
			str := newBaseStream(id, s, s.config.InitialStreamReceiveWindow, s.config.InitialStreamReceiveWindow)
			s.streams[id] = str
			s.mutex.Unlock()
			return &SendStream{baseStream: str}, nil
		}

		wake := s.streamLimitWake
		s.mutex.Unlock()

		s.queueFrame(&wire.StreamsBlockedFrame{
			TypeField:      wire.FrameTypeStreamsBlockedUni,
			MaximumStreams: s.openedUniStreams,
		})

		select {
		case <-wake:
		case <-s.ctx.Done():
			return nil, s.closeErr
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// LocalAddr returns the local address.
func (s *Conn) LocalAddr() net.Addr { return s.conn.LocalAddr() }

// RemoteAddr returns the remote address.
func (s *Conn) RemoteAddr() net.Addr { return s.conn.RemoteAddr() }

// Context returns the connection context.
func (s *Conn) Context() context.Context { return s.ctx }

// ConnectionState returns the connection state.
func (s *Conn) ConnectionState() quic.ConnectionState {
	return quic.ConnectionState{}
}

func encodeVarInt(i uint64) []byte {
	var buf bytes.Buffer
	wire.WriteVarInt(&buf, i)
	return buf.Bytes()
}
