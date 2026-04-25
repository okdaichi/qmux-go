package qmux

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/okdaichi/qmux-go/qmux/internal/flowcontrol"
	"github.com/okdaichi/qmux-go/qmux/internal/wire"
	"github.com/quic-go/quic-go"
)

type baseStream struct {
	id      StreamID
	session *Conn

	ctx       context.Context
	cancelCtx context.CancelFunc

	mutex sync.Mutex

	// Receive side
	readBuffer      bytes.Buffer
	readChan        chan struct{}
	receiveFC       *flowcontrol.FlowController
	receiveOffset   uint64
	receiveClosed   bool
	receiveError    error
	readDeadline    time.Time

	// Send side
	sendOffset    uint64
	sendClosed    bool
	sendError     error
	sendFC        *flowcontrol.FlowController
	writeChan     chan struct{}
	writeDeadline time.Time
}

func newBaseStream(id StreamID, sess *Conn, initialSendWindow, initialReceiveWindow uint64) *baseStream {
	ctx, cancel := context.WithCancel(sess.ctx)
	return &baseStream{
		id:        id,
		session:   sess,
		ctx:       ctx,
		cancelCtx: cancel,
		readChan:  make(chan struct{}, 1),
		writeChan: make(chan struct{}, 1),
		sendFC:    flowcontrol.NewFlowController(initialSendWindow),
		receiveFC: flowcontrol.NewFlowController(initialReceiveWindow),
	}
}

// ReceiveStream is a unidirectional receive-only stream.
type ReceiveStream struct {
	*baseStream
}

func (s *ReceiveStream) Read(p []byte) (int, error) { return s.baseStream.read(p) }
func (s *ReceiveStream) CancelRead(code quic.StreamErrorCode) { s.baseStream.cancelRead(code) }
func (s *ReceiveStream) StreamID() StreamID { return s.baseStream.id }
func (s *ReceiveStream) SetReadDeadline(t time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.readDeadline = t
	return nil
}

// SendStream is a unidirectional send-only stream.
type SendStream struct {
	*baseStream
}

func (s *SendStream) Write(p []byte) (int, error) { return s.baseStream.write(p) }
func (s *SendStream) Close() error { return s.baseStream.close() }
func (s *SendStream) CancelWrite(code quic.StreamErrorCode) { s.baseStream.cancelWrite(code) }
func (s *SendStream) StreamID() StreamID { return s.baseStream.id }
func (s *SendStream) SetWriteDeadline(t time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.writeDeadline = t
	return nil
}

// Stream is a bidirectional stream.
type Stream struct {
	*baseStream
}

func (s *Stream) Read(p []byte) (int, error) { return s.baseStream.read(p) }
func (s *Stream) Write(p []byte) (int, error) { return s.baseStream.write(p) }
func (s *Stream) Close() error { return s.baseStream.close() }
func (s *Stream) CancelRead(code quic.StreamErrorCode) { s.baseStream.cancelRead(code) }
func (s *Stream) CancelWrite(code quic.StreamErrorCode) { s.baseStream.cancelWrite(code) }
func (s *Stream) StreamID() StreamID { return s.baseStream.id }
func (s *Stream) Context() context.Context { return s.baseStream.ctx }
func (s *Stream) SetDeadline(t time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.readDeadline = t
	s.writeDeadline = t
	return nil
}
func (s *Stream) SetReadDeadline(t time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.readDeadline = t
	return nil
}
func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.writeDeadline = t
	return nil
}

// Internal methods on baseStream
func (s *baseStream) read(p []byte) (n int, err error) {
	s.mutex.Lock()
	for s.readBuffer.Len() == 0 && !s.receiveClosed && s.receiveError == nil {
		if !s.readDeadline.IsZero() && time.Now().After(s.readDeadline) {
			s.mutex.Unlock()
			return 0, errors.New("deadline exceeded")
		}
		
		var timeoutChan <-chan time.Time
		if !s.readDeadline.IsZero() {
			timer := time.NewTimer(time.Until(s.readDeadline))
			timeoutChan = timer.C
			defer timer.Stop()
		}

		s.mutex.Unlock()
		select {
		case <-s.readChan:
		case <-timeoutChan:
			return 0, errors.New("deadline exceeded")
		case <-s.ctx.Done():
			return 0, s.ctx.Err()
		}
		s.mutex.Lock()
	}

	if s.readBuffer.Len() > 0 {
		n, _ = s.readBuffer.Read(p)
		s.mutex.Unlock()
		
		if update, limit := s.receiveFC.AddReadBytes(uint64(n)); update {
			s.session.queueFrame(&wire.MaxStreamDataFrame{
				StreamID:          uint64(s.id),
				MaximumStreamData: limit,
			})
		}
		return n, nil
	}

	if s.receiveError != nil {
		err = s.receiveError
	} else if s.receiveClosed {
		err = io.EOF
	}
	s.mutex.Unlock()
	return n, err
}

func (s *baseStream) write(p []byte) (n int, err error) {
	s.mutex.Lock()
	if s.sendClosed {
		if s.sendError != nil {
			err = s.sendError
		} else {
			err = errors.New("stream closed for writing")
		}
		s.mutex.Unlock()
		return 0, err
	}
	s.mutex.Unlock()

	total := 0
	for total < len(p) {
		remaining := uint64(len(p) - total)
		
		s.mutex.Lock()
		window := s.sendFC.SendWindowRemaining()
		connWindow := s.session.connFC.SendWindowRemaining()
		s.mutex.Unlock()

		if window > connWindow {
			window = connWindow
		}

		if window == 0 {
			// Wait for window update or error
			if !s.writeDeadline.IsZero() && time.Now().After(s.writeDeadline) {
				return total, errors.New("deadline exceeded")
			}

			var timeoutChan <-chan time.Time
			if !s.writeDeadline.IsZero() {
				timer := time.NewTimer(time.Until(s.writeDeadline))
				timeoutChan = timer.C
				defer timer.Stop()
			}

			select {
			case <-s.sendFC.WaitSendWindow():
			case <-s.session.connFC.WaitSendWindow():
			case <-s.writeChan:
			case <-timeoutChan:
				return total, errors.New("deadline exceeded")
			case <-s.ctx.Done():
				return total, s.ctx.Err()
			}

			s.mutex.Lock()
			if s.sendClosed {
				if s.sendError != nil {
					err = s.sendError
				} else {
					err = errors.New("stream closed for writing")
				}
				s.mutex.Unlock()
				return total, err
			}
			s.mutex.Unlock()
			continue
		}

		canSend := remaining
		if canSend > window {
			canSend = window
		}
		
		data := p[total : total+int(canSend)]
		
		s.mutex.Lock()
		f := &wire.StreamFrame{
			StreamID: uint64(s.id),
			Offset:   s.sendOffset,
			Data:     data,
		}
		
		if err := s.session.sendFrame(f); err != nil {
			s.mutex.Unlock()
			return total, err
		}
		
		s.sendFC.AddSentBytes(uint64(len(data)))
		s.session.connFC.AddSentBytes(uint64(len(data)))
		s.sendOffset += uint64(len(data))
		s.mutex.Unlock()

		total += len(data)
	}

	return total, nil
}

func (s *baseStream) close() error {
	s.mutex.Lock()
	if s.sendClosed {
		s.mutex.Unlock()
		return nil
	}
	s.sendClosed = true
	offset := s.sendOffset
	s.mutex.Unlock()

	return s.session.sendFrame(&wire.StreamFrame{
		StreamID: uint64(s.id),
		Offset:   offset,
		Fin:      true,
	})
}

func (s *baseStream) cancelRead(code quic.StreamErrorCode) {
	s.session.queueFrame(&wire.StopSendingFrame{
		StreamID:  uint64(s.id),
		ErrorCode: uint64(code),
	})
}

func (s *baseStream) cancelWrite(code quic.StreamErrorCode) {
	s.session.queueFrame(&wire.ResetStreamFrame{
		StreamID:  uint64(s.id),
		ErrorCode: uint64(code),
		FinalSize: s.sendOffset,
	})
}

func (s *baseStream) handleStreamFrame(f *wire.StreamFrame) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if f.Offset != s.receiveOffset {
		return &QMuxError{ErrorCode: ProtocolViolationError, Message: "out of order STREAM frame"}
	}

	if !s.receiveFC.AddReceivedBytes(uint64(len(f.Data))) {
		return &QMuxError{ErrorCode: FlowControlError, Message: "stream flow control violation"}
	}

	s.readBuffer.Write(f.Data)
	s.receiveOffset += uint64(len(f.Data))
	if f.Fin {
		s.receiveClosed = true
	}

	select {
	case s.readChan <- struct{}{}:
	default:
	}

	return nil
}

func (s *baseStream) handleResetStreamFrame(f *wire.ResetStreamFrame) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.receiveClosed {
		return
	}
	s.receiveClosed = true
	s.receiveError = &quic.StreamError{StreamID: s.id, ErrorCode: quic.StreamErrorCode(f.ErrorCode)}
	
	select {
	case s.readChan <- struct{}{}:
	default:
	}
}

func (s *baseStream) handleStopSendingFrame(f *wire.StopSendingFrame) {
	s.mutex.Lock()
	if s.sendClosed {
		s.mutex.Unlock()
		return
	}
	s.sendClosed = true
	s.sendError = &quic.StreamError{StreamID: s.id, ErrorCode: quic.StreamErrorCode(f.ErrorCode)}
	offset := s.sendOffset
	s.mutex.Unlock()

	// Send RESET_STREAM in response to STOP_SENDING as per RFC 9000
	s.session.queueFrame(&wire.ResetStreamFrame{
		StreamID:  uint64(s.id),
		ErrorCode: uint64(f.ErrorCode),
		FinalSize: offset,
	})

	select {
	case s.writeChan <- struct{}{}:
	default:
	}
}
