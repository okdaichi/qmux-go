package qmux

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/okdaichi/qmux-go/qmux/internal/wire"
	"github.com/quic-go/quic-go"
)

type receiveSide struct {
	readBuffer    bytes.Buffer
	readChan      chan struct{}
	receiveFC     *flowController
	receiveOffset atomic.Uint64
	receiveClosed atomic.Bool
	receiveError  error
	readDeadline  time.Time
}

type sendSide struct {
	sendOffset    atomic.Uint64
	sendClosed    atomic.Bool
	sendError     error
	sendFC        *flowController
	writeChan     chan struct{}
	writeDeadline time.Time
}

type baseStream struct {
	id      StreamID
	session *Conn

	ctx       context.Context
	cancelCtx context.CancelFunc

	mutex sync.Mutex

	receive receiveSide
	send    sendSide
}

func newBaseStream(id StreamID, sess *Conn, initialSendWindow, initialReceiveWindow uint64) *baseStream {
	ctx, cancel := context.WithCancel(sess.ctx)
	return &baseStream{
		id:        id,
		session:   sess,
		ctx:       ctx,
		cancelCtx: cancel,
		receive: receiveSide{
			readChan:  make(chan struct{}, 1),
			receiveFC: newFlowController(initialReceiveWindow),
		},
		send: sendSide{
			writeChan: make(chan struct{}, 1),
			sendFC:    newFlowController(initialSendWindow),
		},
	}
}

// ReceiveStream is a unidirectional receive-only stream.
type ReceiveStream struct {
	*baseStream
}

func (s *ReceiveStream) Read(p []byte) (int, error) { return s.baseStream.read(p) }
func (s *ReceiveStream) CancelRead(code StreamErrorCode) { s.baseStream.cancelRead(code) }
func (s *ReceiveStream) StreamID() StreamID { return s.baseStream.id }
func (s *ReceiveStream) SetReadDeadline(t time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.receive.readDeadline = t
	return nil
}

// SendStream is a unidirectional send-only stream.
type SendStream struct {
	*baseStream
}

func (s *SendStream) Write(p []byte) (int, error) { return s.baseStream.write(p) }
func (s *SendStream) Close() error { return s.baseStream.close() }
func (s *SendStream) CancelWrite(code StreamErrorCode) { s.baseStream.cancelWrite(code) }
func (s *SendStream) StreamID() StreamID { return s.baseStream.id }
func (s *SendStream) SetWriteDeadline(t time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.send.writeDeadline = t
	return nil
}

// Stream is a bidirectional stream.
type Stream struct {
	*baseStream
}

func (s *Stream) Read(p []byte) (int, error) { return s.baseStream.read(p) }
func (s *Stream) Write(p []byte) (int, error) { return s.baseStream.write(p) }
func (s *Stream) Close() error { return s.baseStream.close() }
func (s *Stream) CancelRead(code StreamErrorCode) { s.baseStream.cancelRead(code) }
func (s *Stream) CancelWrite(code StreamErrorCode) { s.baseStream.cancelWrite(code) }
func (s *Stream) StreamID() StreamID { return s.baseStream.id }
func (s *Stream) Context() context.Context { return s.baseStream.ctx }
func (s *Stream) SetDeadline(t time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.receive.readDeadline = t
	s.send.writeDeadline = t
	return nil
}
func (s *Stream) SetReadDeadline(t time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.receive.readDeadline = t
	return nil
}
func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.send.writeDeadline = t
	return nil
}

// Internal methods on baseStream
func (s *baseStream) read(p []byte) (n int, err error) {
	s.mutex.Lock()
	for s.receive.readBuffer.Len() == 0 && !s.receive.receiveClosed.Load() && s.receive.receiveError == nil {
		if !s.receive.readDeadline.IsZero() && time.Now().After(s.receive.readDeadline) {
			s.mutex.Unlock()
			return 0, errors.New("deadline exceeded")
		}

		var timeoutChan <-chan time.Time
		var timer *time.Timer
		if !s.receive.readDeadline.IsZero() {
			duration := time.Until(s.receive.readDeadline)
			if duration <= 0 {
				s.mutex.Unlock()
				return 0, errors.New("deadline exceeded")
			}
			timer = time.NewTimer(duration)
			timeoutChan = timer.C
		}

		s.mutex.Unlock()
		select {
		case <-s.receive.readChan:
		case <-timeoutChan:
			return 0, errors.New("deadline exceeded")
		case <-s.ctx.Done():
			s.mutex.Lock()
			if s.receive.receiveError != nil {
				err = s.receive.receiveError
				s.mutex.Unlock()
				return 0, err
			}
			s.mutex.Unlock()
			return 0, s.ctx.Err()
		}
		if timer != nil {
			timer.Stop()
		}
		s.mutex.Lock()
	}

	if s.receive.readBuffer.Len() > 0 {
		n, _ = s.receive.readBuffer.Read(p)
		s.mutex.Unlock()

		// Update stream flow control
		if update, limit := s.receive.receiveFC.AddReadBytes(uint64(n)); update {
			s.session.queueControlFrame(&wire.MaxStreamDataFrame{
				StreamID:          uint64(s.id),
				MaximumStreamData: limit,
			})
		}
		// Update connection flow control
		if update, limit := s.session.connFC.AddReadBytes(uint64(n)); update {
			s.session.queueControlFrame(&wire.MaxDataFrame{
				MaximumData: limit,
			})
		}
		return n, nil
	}

	if s.receive.receiveError != nil {
		err = s.receive.receiveError
	} else if s.receive.receiveClosed.Load() {
		err = io.EOF
	}
	s.mutex.Unlock()
	return n, err
}

func (s *baseStream) write(p []byte) (n int, err error) {
	total := 0
	for total < len(p) {
		s.mutex.Lock()
		if s.send.sendClosed.Load() {
			if s.send.sendError != nil {
				err = s.send.sendError
			} else {
				err = errors.New("stream closed for writing")
			}
			s.mutex.Unlock()
			return total, err
		}
		s.mutex.Unlock()

		select {
		case <-s.ctx.Done():
			s.mutex.Lock()
			if s.send.sendError != nil {
				err = s.send.sendError
				s.mutex.Unlock()
				return total, err
			}
			s.mutex.Unlock()
			return total, s.ctx.Err()
		default:
		}

		if !s.send.writeDeadline.IsZero() && time.Now().After(s.send.writeDeadline) {
			return total, errors.New("deadline exceeded")
		}

		remaining := uint64(len(p) - total)

		// Get wake channels BEFORE checking window to avoid race condition
		streamWake := s.send.sendFC.WaitSendWindow()
		connWake := s.session.connFC.WaitSendWindow()

		s.mutex.Lock()
		window := s.send.sendFC.SendWindowRemaining()
		connWindow := s.session.connFC.SendWindowRemaining()
		peerMaxRecord := s.session.peerMaxRecordSize.Load()
		s.mutex.Unlock()

		if window > connWindow {
			window = connWindow
		}

		if window == 0 {
			var timeoutChan <-chan time.Time
			var timer *time.Timer
			if !s.send.writeDeadline.IsZero() {
				duration := time.Until(s.send.writeDeadline)
				if duration <= 0 {
					return total, errors.New("deadline exceeded")
				}
				timer = time.NewTimer(duration)
				timeoutChan = timer.C
			}

			select {
			case <-streamWake:
			case <-connWake:
			case <-s.send.writeChan:
			case <-timeoutChan:
				return total, errors.New("deadline exceeded")
			case <-s.ctx.Done():
				s.mutex.Lock()
				if s.send.sendError != nil {
					err = s.send.sendError
					s.mutex.Unlock()
					return total, err
				}
				s.mutex.Unlock()
				return total, s.ctx.Err()
			}
			if timer != nil {
				timer.Stop()
			}
			continue
		}

		canSend := min(remaining, window)

		// Respect peerMaxRecordSize.
		const maxOverhead = 64
		if peerMaxRecord > maxOverhead {
			maxPayload := peerMaxRecord - maxOverhead
			if canSend > maxPayload {
				canSend = maxPayload
			}
		}

		data := p[total : total+int(canSend)]

		f := wire.GetStreamFrame()
		f.StreamID = uint64(s.id)
		f.Offset = s.send.sendOffset.Load()
		f.Data = data
		f.Fin = false

		if err := s.session.sendFrame(f); err != nil {
			f.Recycle()
			return total, err
		}

		s.send.sendFC.AddSentBytes(uint64(len(data)))
		s.session.connFC.AddSentBytes(uint64(len(data)))
		s.send.sendOffset.Add(uint64(len(data)))

		total += len(data)
	}

	return total, nil
}

func (s *baseStream) closeWithError(err error) {
	// Close receive side
	if !s.receive.receiveClosed.Swap(true) {
		s.mutex.Lock()
		s.receive.receiveError = err
		s.mutex.Unlock()
		select {
		case s.receive.readChan <- struct{}{}:
		default:
		}
	}

	// Close send side
	if !s.send.sendClosed.Swap(true) {
		s.mutex.Lock()
		s.send.sendError = err
		s.mutex.Unlock()
		select {
		case s.send.writeChan <- struct{}{}:
		default:
		}
	}
}

func (s *baseStream) close() error {
	if s.send.sendClosed.Swap(true) {
		return nil
	}
	offset := s.send.sendOffset.Load()

	f := wire.GetStreamFrame()
	f.StreamID = uint64(s.id)
	f.Offset = offset
	f.Data = nil
	f.Fin = true

	return s.session.sendFrame(f)
}

func (s *baseStream) cancelRead(code StreamErrorCode) {
	s.session.queueControlFrame(&wire.StopSendingFrame{
		StreamID:  uint64(s.id),
		ErrorCode: uint64(code),
	})
}

func (s *baseStream) cancelWrite(code StreamErrorCode) {
	offset := s.send.sendOffset.Load()

	s.session.queueControlFrame(&wire.ResetStreamFrame{
		StreamID:  uint64(s.id),
		ErrorCode: uint64(code),
		FinalSize: offset,
	})
}

func (s *baseStream) handleStreamFrame(f *wire.StreamFrame) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if f.Offset != s.receive.receiveOffset.Load() {
		return &quic.TransportError{ErrorCode: ProtocolViolationError, ErrorMessage: "out of order STREAM frame"}
	}

	if !s.receive.receiveFC.AddReceivedBytes(uint64(len(f.Data))) {
		return &quic.TransportError{ErrorCode: FlowControlError, ErrorMessage: "stream flow control violation"}
	}
	if !s.session.connFC.AddReceivedBytes(uint64(len(f.Data))) {
		return &quic.TransportError{ErrorCode: FlowControlError, ErrorMessage: "connection flow control violation"}
	}


	s.receive.readBuffer.Write(f.Data)
	s.receive.receiveOffset.Add(uint64(len(f.Data)))
	if f.Fin {
		s.receive.receiveClosed.Store(true)
	}

	select {
	case s.receive.readChan <- struct{}{}:
	default:
	}

	return nil
}

func (s *baseStream) handleResetStreamFrame(f *wire.ResetStreamFrame) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.receive.receiveClosed.Swap(true) {
		return
	}
	s.receive.receiveError = &quic.StreamError{StreamID: s.id, ErrorCode: StreamErrorCode(f.ErrorCode)}

	// Unblock Read
	select {
	case s.receive.readChan <- struct{}{}:
	default:
	}
	// Also unblock Write just in case
	select {
	case s.send.writeChan <- struct{}{}:
	default:
	}
}

func (s *baseStream) handleStopSendingFrame(f *wire.StopSendingFrame) {
	s.mutex.Lock()
	if s.send.sendClosed.Swap(true) {
		s.mutex.Unlock()
		return
	}
	s.send.sendError = &quic.StreamError{StreamID: s.id, ErrorCode: StreamErrorCode(f.ErrorCode)}
	offset := s.send.sendOffset.Load()
	s.mutex.Unlock()

	// Send RESET_STREAM in response to STOP_SENDING as per RFC 9000
	s.session.queueControlFrame(&wire.ResetStreamFrame{
		StreamID:  uint64(s.id),
		ErrorCode: uint64(f.ErrorCode),
		FinalSize: offset,
	})

	// Unblock Write
	select {
	case s.send.writeChan <- struct{}{}:
	default:
	}
	// Also unblock Read just in case
	select {
	case s.receive.readChan <- struct{}{}:
	default:
	}
}
