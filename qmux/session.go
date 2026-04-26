package qmux

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/okdaichi/qmux-go/qmux/internal/wire"
	"github.com/quic-go/quic-go"
)

const (
	defaultWriteDeadline = 5 * time.Second

	// Stream ID bits
	streamIDClientInitiated = 0x00
	streamIDServerInitiated = 0x01
	streamIDBidirectional   = 0x00
	streamIDUnidirectional  = 0x02
)

type streamManager struct {
	mutex   sync.Mutex
	streams map[StreamID]*baseStream

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
}

func newStreamManager(config *Config, isServer bool) *streamManager {
	sm := &streamManager{
		streams:            make(map[StreamID]*baseStream),
		incomingBidi:       make(chan *Stream, int(config.MaxIncomingStreams)),
		incomingUni:        make(chan *ReceiveStream, int(config.MaxIncomingUniStreams)),
		streamLimitWake:    make(chan struct{}),
		peerMaxBidiStreams: 100, // Initial default
		peerMaxUniStreams:  100,
	}

	if isServer {
		sm.nextBidiStreamID = StreamID(streamIDServerInitiated | streamIDBidirectional)
		sm.nextUniStreamID = StreamID(streamIDServerInitiated | streamIDUnidirectional)
	} else {
		sm.nextBidiStreamID = StreamID(streamIDClientInitiated | streamIDBidirectional)
		sm.nextUniStreamID = StreamID(streamIDClientInitiated | streamIDUnidirectional)
	}
	return sm
}

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

	writeMutex sync.Mutex
	sm         *streamManager

	connFC *flowController

	handshakeDone chan struct{}
	closeErr      error

	mutex             sync.Mutex
	lastFrameTime     time.Time
	idleTimeout       time.Duration
	peerMaxRecordSize uint64

	peerMaxDatagramFrameSize uint64
	incomingDatagrams        chan []byte

	// Frame queues
	queueMutex    sync.Mutex
	controlFrames []wire.Frame
	streamFrames  []wire.Frame
	wake          chan struct{}
}

func newSession(conn net.Conn, config *Config, isServer bool) *Conn {
	if config == nil {
		config = DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &Conn{
		conn:              conn,
		config:            config,
		isServer:          isServer,
		rr:                wire.NewRecordReader(conn),
		rw:                wire.NewRecordWriter(conn),
		ctx:               ctx,
		cancelCtx:         cancel,
		sm:                newStreamManager(config, isServer),
		connFC:            newFlowController(config.InitialConnectionReceiveWindow),
		handshakeDone:     make(chan struct{}),
		lastFrameTime:     time.Now(),
		peerMaxRecordSize: 16382, // Spec default
		incomingDatagrams: make(chan []byte, 100),
		wake:              make(chan struct{}, 1),
	}

	// Drain goroutine to prevent handleFrame from blocking if session is closed
	go func() {
		<-ctx.Done()
		for {
			select {
			case <-s.sm.incomingBidi:
			case <-s.sm.incomingUni:
			default:
				return
			}
		}
	}()

	return s
}

func (s *Conn) run() {
	go s.writeLoop()
	
	defer s.Close()
	defer func() {
		// Ensure handshakeDone is always closed
		select {
		case <-s.handshakeDone:
		default:
			close(s.handshakeDone)
		}
	}()

	// 1. Send our parameters
	if err := s.sendTransportParameters(); err != nil {
		s.CloseWithError(quic.ApplicationErrorCode(InternalError), err.Error())
		return
	}

	// 2. Read loop
	firstRecord := true
	for {
		frames, err := s.rr.ReadRecord()
		if err != nil {
			s.CloseWithError(0, err.Error())
			return
		}

		s.mutex.Lock()
		s.lastFrameTime = time.Now()
		s.mutex.Unlock()

		if firstRecord {
			if len(frames) == 0 {
				s.CloseWithError(quic.ApplicationErrorCode(TransportParameterError), "empty initial record")
				return
			}
			f, ok := frames[0].(*wire.TransportParametersFrame)
			if !ok {
				s.CloseWithError(quic.ApplicationErrorCode(TransportParameterError), "first frame is not QX_TRANSPORT_PARAMETERS")
				return
			}
			s.handleHandshakeParameters(f)
			close(s.handshakeDone)
			firstRecord = false
			frames = frames[1:]
			s.startBackgroundTasks()
		}

		for _, f := range frames {
			if err := s.handleFrame(f); err != nil {
				var appErr *quic.ApplicationError
				if errors.As(err, &appErr) {
					s.CloseWithError(appErr.ErrorCode, appErr.ErrorMessage)
				} else {
					s.CloseWithError(0, err.Error())
				}
				return
			}
		}
	}
}

func (s *Conn) sendTransportParameters() error {
	params := []wire.TransportParameter{
		{ID: wire.TransportParameterInitialMaxData, Value: encodeVarInt(s.config.InitialConnectionReceiveWindow)},
		{ID: wire.TransportParameterInitialMaxStreamDataBidiLocal, Value: encodeVarInt(s.config.InitialStreamReceiveWindow)},
		{ID: wire.TransportParameterInitialMaxStreamDataBidiRemote, Value: encodeVarInt(s.config.InitialStreamReceiveWindow)},
		{ID: wire.TransportParameterInitialMaxStreamDataUni, Value: encodeVarInt(s.config.InitialStreamReceiveWindow)},
		{ID: wire.TransportParameterInitialMaxStreamsBidi, Value: encodeVarInt(uint64(s.config.MaxIncomingStreams))},
		{ID: wire.TransportParameterInitialMaxStreamsUni, Value: encodeVarInt(uint64(s.config.MaxIncomingUniStreams))},
	}
	if s.config.MaxRecordSize > 0 {
		params = append(params, wire.TransportParameter{ID: wire.TransportParameterMaxRecordSize, Value: encodeVarInt(s.config.MaxRecordSize)})
	}
	if s.config.MaxIdleTimeout > 0 {
		params = append(params, wire.TransportParameter{ID: wire.TransportParameterMaxIdleTimeout, Value: encodeVarInt(uint64(s.config.MaxIdleTimeout / time.Millisecond))})
	}
	if s.config.EnableDatagrams {
		params = append(params, wire.TransportParameter{ID: wire.TransportParameterMaxDatagramFrameSize, Value: encodeVarInt(s.config.MaxDatagramFrameSize)})
	}
	s.queueControlFrame(&wire.TransportParametersFrame{Parameters: params})
	return nil
}

func (s *Conn) handleHandshakeParameters(f *wire.TransportParametersFrame) {
	limitsUpdated := false
	for _, p := range f.Parameters {
		val, _ := wire.ReadVarInt(bytes.NewReader(p.Value))
		switch p.ID {
		case wire.TransportParameterInitialMaxData:
			s.connFC.UpdateSendWindow(val)
		case wire.TransportParameterInitialMaxStreamsBidi:
			s.sm.mutex.Lock()
			s.sm.peerMaxBidiStreams = val
			limitsUpdated = true
			s.sm.mutex.Unlock()
		case wire.TransportParameterInitialMaxStreamsUni:
			s.sm.mutex.Lock()
			s.sm.peerMaxUniStreams = val
			limitsUpdated = true
			s.sm.mutex.Unlock()
		case wire.TransportParameterMaxIdleTimeout:
			s.mutex.Lock()
			s.idleTimeout = time.Duration(val) * time.Millisecond
			s.mutex.Unlock()
		case wire.TransportParameterMaxRecordSize:
			s.mutex.Lock()
			s.peerMaxRecordSize = val
			s.mutex.Unlock()
		case wire.TransportParameterMaxDatagramFrameSize:
			s.mutex.Lock()
			s.peerMaxDatagramFrameSize = val
			s.mutex.Unlock()
		}
	}

	if limitsUpdated {
		s.sm.mutex.Lock()
		close(s.sm.streamLimitWake)
		s.sm.streamLimitWake = make(chan struct{})
		s.sm.mutex.Unlock()
	}
}

func (s *Conn) startBackgroundTasks() {
	if s.config.KeepAlivePeriod > 0 {
		go func() {
			ticker := time.NewTicker(s.config.KeepAlivePeriod)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					s.queueControlFrame(&wire.PingFrame{
						TypeField: wire.FrameTypePingRequest,
						Sequence:  uint64(time.Now().UnixNano()),
					})
				case <-s.ctx.Done():
					return
				}
			}
		}()
	}

	s.mutex.Lock()
	idleTimeout := s.idleTimeout
	s.mutex.Unlock()

	if idleTimeout > 0 {
		go func() {
			ticker := time.NewTicker(idleTimeout / 2)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					s.mutex.Lock()
					last := s.lastFrameTime
					s.mutex.Unlock()
					if time.Since(last) > idleTimeout {
						s.CloseWithError(quic.ApplicationErrorCode(InternalError), "idle timeout")
						return
					}
				case <-s.ctx.Done():
					return
				}
			}
		}()
	}
}

func (s *Conn) writeLoop() {
	for {
		s.queueMutex.Lock()
		for len(s.controlFrames) == 0 && len(s.streamFrames) == 0 {
			s.queueMutex.Unlock()
			select {
			case <-s.wake:
				s.queueMutex.Lock()
			case <-s.ctx.Done():
				return
			}
		}

		frames := make([]wire.Frame, 0, len(s.controlFrames)+len(s.streamFrames))
		frames = append(frames, s.controlFrames...)
		s.controlFrames = s.controlFrames[:0]
		frames = append(frames, s.streamFrames...)
		s.streamFrames = s.streamFrames[:0]
		s.queueMutex.Unlock()

		if err := s.writeFrames(frames...); err != nil {
			s.CloseWithError(0, err.Error())
			return
		}
	}
}

func (s *Conn) writeFrames(frames ...wire.Frame) error {
	s.writeMutex.Lock()
	defer s.writeMutex.Unlock()

	s.mutex.Lock()
	s.lastFrameTime = time.Now()
	peerMax := s.peerMaxRecordSize
	s.mutex.Unlock()

	// Split frames into multiple records if total size exceeds peerMaxRecordSize.
	// Note: A single frame MUST fit into peerMaxRecordSize.
	var current []wire.Frame
	var currentSize uint64
	for _, f := range frames {
		fLen := f.Length()
		if fLen > peerMax {
			return &Error{ErrorCode: ProtocolViolationError, Message: "frame too large for peer record size"}
		}

		if currentSize+fLen > peerMax && len(current) > 0 {
			if err := s.sendRecord(current); err != nil {
				return err
			}
			current = nil
			currentSize = 0
		}
		current = append(current, f)
		currentSize += fLen
	}

	if len(current) > 0 {
		return s.sendRecord(current)
	}
	return nil
}

func (s *Conn) sendRecord(frames []wire.Frame) error {
	// Use a deadline to avoid blocking forever
	s.conn.SetWriteDeadline(time.Now().Add(defaultWriteDeadline))
	err := s.rw.WriteRecord(frames...)
	s.conn.SetWriteDeadline(time.Time{})
	return err
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
		str, err := s.getOrCreateBaseStream(StreamID(ff.StreamID))
		if err != nil {
			return err
		}
		str.send.sendFC.UpdateSendWindow(ff.MaximumStreamData)
	case *wire.TransportParametersFrame:
		return &Error{ErrorCode: ProtocolViolationError, Message: "duplicate QX_TRANSPORT_PARAMETERS"}
	case *wire.MaxStreamsFrame:
		s.handleMaxStreamsFrame(ff)
	case *wire.StreamsBlockedFrame:
		return nil
	case *wire.DataBlockedFrame:
		return nil
	case *wire.StreamDataBlockedFrame:
		return nil
	case *wire.PingFrame:
		s.handlePingFrame(ff)
	case *wire.DatagramFrame:
		s.handleDatagramFrame(ff)
	case *wire.ConnectionCloseFrame:
		return &quic.ApplicationError{ErrorCode: quic.ApplicationErrorCode(ff.ErrorCode), ErrorMessage: ff.ReasonPhrase}
	}
	return nil
}

func (s *Conn) handleDatagramFrame(f *wire.DatagramFrame) {
	select {
	case s.incomingDatagrams <- f.Data:
	default:
		// Drop datagram if queue is full as per RFC 9221
	}
}

// SendMessage sends a datagram message.
func (s *Conn) SendMessage(p []byte) error {
	if !s.config.EnableDatagrams {
		return errors.New("datagrams not enabled")
	}
	s.mutex.Lock()
	peerMax := s.peerMaxDatagramFrameSize
	s.mutex.Unlock()

	if uint64(len(p)) > peerMax {
		return errors.New("message too large")
	}

	return s.writeFrames(&wire.DatagramFrame{Data: p})
}

// ReceiveMessage receives a datagram message.
func (s *Conn) ReceiveMessage(ctx context.Context) ([]byte, error) {
	if !s.config.EnableDatagrams {
		return nil, errors.New("datagrams not enabled")
	}
	select {
	case msg := <-s.incomingDatagrams:
		return msg, nil
	case <-s.ctx.Done():
		s.mutex.Lock()
		err := s.closeErr
		s.mutex.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("connection closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Conn) handleMaxStreamsFrame(f *wire.MaxStreamsFrame) {
	s.sm.mutex.Lock()
	defer s.sm.mutex.Unlock()
	if f.TypeField == wire.FrameTypeMaxStreamsBi {
		s.sm.peerMaxBidiStreams = f.MaximumStreams
	} else {
		s.sm.peerMaxUniStreams = f.MaximumStreams
	}
	close(s.sm.streamLimitWake)
	s.sm.streamLimitWake = make(chan struct{})
}

func (s *Conn) handlePingFrame(f *wire.PingFrame) {
	if f.TypeField == wire.FrameTypePingRequest {
		s.queueControlFrame(&wire.PingFrame{
			TypeField: wire.FrameTypePingResponse,
			Sequence:  f.Sequence,
		})
	}
}

func (s *Conn) getBaseStream(id StreamID) *baseStream {
	s.sm.mutex.Lock()
	defer s.sm.mutex.Unlock()
	return s.sm.streams[id]
}

func (s *Conn) getOrCreateBaseStream(id StreamID) (*baseStream, error) {
	s.sm.mutex.Lock()
	defer s.sm.mutex.Unlock()
	if str, ok := s.sm.streams[id]; ok {
		return str, nil
	}

	var receiveWindow uint64
	if id&streamIDUnidirectional == 0 {
		receiveWindow = s.config.InitialStreamReceiveWindow
	} else {
		receiveWindow = s.config.InitialStreamReceiveWindow // We use same for now, but could differentiate
	}

	str := newBaseStream(id, s, s.config.InitialStreamReceiveWindow, receiveWindow)
	s.sm.streams[id] = str

	if id&streamIDUnidirectional == 0 { // Bidirectional
		select {
		case s.sm.incomingBidi <- &Stream{baseStream: str}:
		default:
			return nil, &Error{ErrorCode: StreamLimitError, Message: "too many concurrent streams"}
		}
	} else {
		select {
		case s.sm.incomingUni <- &ReceiveStream{baseStream: str}:
		default:
			return nil, &Error{ErrorCode: StreamLimitError, Message: "too many concurrent uni streams"}
		}
	}
	return str, nil
}

func (s *Conn) sendFrame(f wire.Frame) error {
	s.queueMutex.Lock()
	s.streamFrames = append(s.streamFrames, f)
	s.queueMutex.Unlock()
	s.signalWake()
	return nil
}

func (s *Conn) queueFrame(f wire.Frame) {
	_ = s.sendFrame(f)
}

func (s *Conn) queueControlFrame(f wire.Frame) {
	s.queueMutex.Lock()
	s.controlFrames = append(s.controlFrames, f)
	s.queueMutex.Unlock()
	s.signalWake()
}

func (s *Conn) signalWake() {
	select {
	case s.wake <- struct{}{}:
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
	s.mutex.Unlock()

	s.sm.mutex.Lock()
	for _, str := range s.sm.streams {
		str.mutex.Lock()
		if !str.receive.receiveClosed {
			str.receive.receiveClosed = true
			str.receive.receiveError = s.closeErr
			select {
			case str.receive.readChan <- struct{}{}:
			default:
			}
		}
		if !str.send.sendClosed {
			str.send.sendClosed = true
			str.send.sendError = s.closeErr
			select {
			case str.send.writeChan <- struct{}{}:
			default:
			}
		}
		str.mutex.Unlock()
	}
	close(s.sm.streamLimitWake)
	s.sm.mutex.Unlock()

	// Direct write for connection close
	s.writeFrames(&wire.ConnectionCloseFrame{
		TypeField:    wire.FrameTypeApplicationClose,
		ErrorCode:    uint64(code),
		ReasonPhrase: msg,
	})
	
	return s.conn.Close()
}

func (s *Conn) waitHandshake(ctx context.Context) error {
	select {
	case <-s.handshakeDone:
		s.mutex.Lock()
		err := s.closeErr
		s.mutex.Unlock()
		return err
	case <-s.ctx.Done():
		s.mutex.Lock()
		if s.closeErr != nil {
			err := s.closeErr
			s.mutex.Unlock()
			return err
		}
		s.mutex.Unlock()
		return errors.New("connection closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AcceptStream accepts the next incoming bidirectional stream.
func (s *Conn) AcceptStream(ctx context.Context) (*Stream, error) {
	if err := s.waitHandshake(ctx); err != nil {
		return nil, err
	}

	select {
	case str := <-s.sm.incomingBidi:
		return str, nil
	case <-s.ctx.Done():
		s.mutex.Lock()
		err := s.closeErr
		s.mutex.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("connection closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// AcceptUniStream accepts the next incoming unidirectional stream.
func (s *Conn) AcceptUniStream(ctx context.Context) (*ReceiveStream, error) {
	if err := s.waitHandshake(ctx); err != nil {
		return nil, err
	}

	select {
	case str := <-s.sm.incomingUni:
		return str, nil
	case <-s.ctx.Done():
		s.mutex.Lock()
		err := s.closeErr
		s.mutex.Unlock()
		if err != nil {
			return nil, err
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
	if err := s.waitHandshake(ctx); err != nil {
		return nil, err
	}

	for {
		s.sm.mutex.Lock()
		if s.sm.openedBidiStreams < s.sm.peerMaxBidiStreams {
			id := s.sm.nextBidiStreamID
			s.sm.nextBidiStreamID += 4
			s.sm.openedBidiStreams++
			str := newBaseStream(id, s, s.config.InitialStreamReceiveWindow, s.config.InitialStreamReceiveWindow)
			s.sm.streams[id] = str
			s.sm.mutex.Unlock()

			// Signal the peer about the new stream by sending a MAX_STREAM_DATA frame.
			// This allows the peer's AcceptStream to return even if no data is sent yet.
			s.queueControlFrame(&wire.MaxStreamDataFrame{
				StreamID:          uint64(id),
				MaximumStreamData: s.config.InitialStreamReceiveWindow,
			})

			return &Stream{baseStream: str}, nil
		}

		wake := s.sm.streamLimitWake
		s.sm.mutex.Unlock()

		s.queueControlFrame(&wire.StreamsBlockedFrame{
			TypeField:      wire.FrameTypeStreamsBlockedBi,
			MaximumStreams: s.sm.openedBidiStreams,
		})

		select {
		case <-wake:
		case <-s.ctx.Done():
			s.mutex.Lock()
			err := s.closeErr
			s.mutex.Unlock()
			if err != nil {
				return nil, err
			}
			return nil, errors.New("connection closed")
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
	if err := s.waitHandshake(ctx); err != nil {
		return nil, err
	}

	for {
		s.sm.mutex.Lock()
		if s.sm.openedUniStreams < s.sm.peerMaxUniStreams {
			id := s.sm.nextUniStreamID
			s.sm.nextUniStreamID += 4
			s.sm.openedUniStreams++
			str := newBaseStream(id, s, s.config.InitialStreamReceiveWindow, s.config.InitialStreamReceiveWindow)
			s.sm.streams[id] = str
			s.sm.mutex.Unlock()

			// Signal the peer about the new stream by sending a STREAM frame with no data.
			s.queueFrame(&wire.StreamFrame{
				StreamID: uint64(id),
				Offset:   0,
				Data:     nil,
				Fin:      false,
			})

			return &SendStream{baseStream: str}, nil
		}

		wake := s.sm.streamLimitWake
		s.sm.mutex.Unlock()

		s.queueControlFrame(&wire.StreamsBlockedFrame{
			TypeField:      wire.FrameTypeStreamsBlockedUni,
			MaximumStreams: s.sm.openedUniStreams,
		})

		select {
		case <-wake:
		case <-s.ctx.Done():
			s.mutex.Lock()
			err := s.closeErr
			s.mutex.Unlock()
			if err != nil {
				return nil, err
			}
			return nil, errors.New("connection closed")
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
