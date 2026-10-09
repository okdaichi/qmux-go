package qmux

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/okdaichi/qmux-go/qmux/internal/wire"
	"github.com/quic-go/quic-go"
)

// maxStreamData is the most stream data that fits in a record of the
// default size, which every peer accepts.
const maxStreamData = wire.DefaultMaxRecordSize - wire.StreamOverhead

// stream is the state of one stream. The connection's mutex guards it.
type stream struct {
	c  *Conn
	id StreamID

	// ctx ends with the sending side. It is nil on a receive-only stream.
	ctx    context.Context
	cancel context.CancelCauseFunc

	accepted bool // the application holds the stream
	done     bool // both directions have ended; the connection forgot it

	canRecv       bool
	rbuf          []byte // received data; rbuf[roff:] is unread
	roff          int
	recvOffset    uint64 // data received from the peer
	recvMax       uint64 // the limit declared to the peer
	readOffset    uint64 // data read by the application
	finRecv       bool
	recvReset     bool  // the peer reset the stream
	readCancelled bool  // the application cancelled reading
	recvErr       error // what Read returns after a reset or a cancel
	readDeadline  time.Time
	readSignal    signal

	canSend       bool
	sendOffset    uint64
	sendMax       uint64 // the peer's limit
	finSent       bool
	sendErr       error // what Write returns after a reset
	writeDeadline time.Time
	writeSignal   signal
}

// newStreamLocked creates the state of a stream that the peer's transport
// parameters allow: they are in by the time either side opens one.
func (c *Conn) newStreamLocked(id StreamID) *stream {
	local := c.initiatedLocally(id)
	uni := streamDir(id) == dirUni
	s := &stream{
		c:       c,
		id:      id,
		canRecv: !uni || !local,
		canSend: !uni || local,
	}
	if s.canRecv {
		s.recvMax = c.config.InitialStreamReceiveWindow
	}
	if s.canSend {
		s.ctx, s.cancel = context.WithCancelCause(c.ctx)
		switch {
		case uni:
			s.sendMax = c.peer.InitialMaxStreamDataUni
		case local:
			s.sendMax = c.peer.InitialMaxStreamDataBidiRemote
		default:
			s.sendMax = c.peer.InitialMaxStreamDataBidiLocal
		}
	}
	c.streams[id] = s
	return s
}

func (s *stream) recvDone() bool {
	if !s.canRecv || s.recvReset {
		return true
	}
	return s.finRecv && (s.readCancelled || s.roff == len(s.rbuf))
}

func (s *stream) sendDone() bool {
	return !s.canSend || s.finSent || s.sendErr != nil
}

// wait blocks until wake or other is closed. It fails once the deadline,
// when set, has passed.
func wait(deadline time.Time, wake, other <-chan struct{}) error {
	var expired <-chan time.Time
	if !deadline.IsZero() {
		d := time.Until(deadline)
		if d <= 0 {
			return os.ErrDeadlineExceeded
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case <-wake:
	case <-other:
	case <-expired:
		return os.ErrDeadlineExceeded
	}
	return nil
}

func (s *stream) read(p []byte) (int, error) {
	for {
		n, wake, deadline, err := s.tryRead(p)
		if n > 0 || err != nil || len(p) == 0 {
			return n, err
		}
		if err := wait(deadline, wake, nil); err != nil {
			return 0, err
		}
	}
}

// tryRead copies received data to p. With none to copy and no error, it
// returns a channel that is closed when the stream's state changes.
func (s *stream) tryRead(p []byte) (int, <-chan struct{}, time.Time, error) {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.closeErr != nil:
		return 0, nil, time.Time{}, c.closeErr
	case s.recvErr != nil:
		return 0, nil, time.Time{}, s.recvErr
	}
	if s.roff < len(s.rbuf) {
		n := copy(p, s.rbuf[s.roff:])
		s.roff += n
		if s.roff == len(s.rbuf) {
			s.rbuf, s.roff = s.rbuf[:0], 0
		}
		s.readLocked(uint64(n))
		c.completeLocked(s)
		return n, nil, time.Time{}, nil
	}
	if s.finRecv {
		return 0, nil, time.Time{}, io.EOF
	}
	return 0, s.readSignal.wait(), s.readDeadline, nil
}

// readLocked accounts for data the application has read, and extends the
// stream's limit once half the window is used.
func (s *stream) readLocked(n uint64) {
	c := s.c
	s.readOffset += n
	c.consumeLocked(n)
	window := c.config.InitialStreamReceiveWindow
	if !s.finRecv && s.recvMax-s.readOffset < window/2 {
		s.recvMax = s.readOffset + window
		c.queueLocked(&wire.MaxStreamData{StreamID: uint64(s.id), Max: s.recvMax})
	}
}

func (s *stream) cancelRead(code StreamErrorCode) {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeErr != nil || s.recvErr != nil || s.recvDone() {
		return
	}
	s.readCancelled = true
	s.recvErr = &quic.StreamError{StreamID: s.id, ErrorCode: code}
	c.consumeLocked(uint64(len(s.rbuf) - s.roff))
	s.rbuf, s.roff = nil, 0
	if !s.finRecv {
		c.queueLocked(&wire.StopSending{StreamID: uint64(s.id), Code: uint64(code)})
	}
	s.readSignal.broadcast()
	c.completeLocked(s)
}

func (s *stream) write(p []byte) (int, error) {
	total := 0
	for total < len(p) {
		if err := s.waitSendCredit(); err != nil {
			return total, err
		}
		n, err := s.writeChunk(p[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// sendErrLocked returns why the stream cannot send, if it cannot.
func (s *stream) sendErrLocked() error {
	switch {
	case s.c.closeErr != nil:
		return s.c.closeErr
	case s.sendErr != nil:
		return s.sendErr
	case s.finSent:
		return fmt.Errorf("qmux: write on closed stream %d", s.id)
	}
	return nil
}

// sendCreditLocked returns how much data flow control lets the stream send.
func (s *stream) sendCreditLocked() uint64 {
	c := s.c
	if s.sendMax <= s.sendOffset || c.sendMax <= c.sent {
		return 0
	}
	return min(s.sendMax-s.sendOffset, c.sendMax-c.sent)
}

// trySend reports whether the stream has flow control credit. Without any,
// it returns the channels that are closed when that may have changed.
func (s *stream) trySend() (ok bool, wake, connWake <-chan struct{}, deadline time.Time, err error) {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := s.sendErrLocked(); err != nil {
		return false, nil, nil, time.Time{}, err
	}
	if s.sendCreditLocked() > 0 {
		return true, nil, nil, time.Time{}, nil
	}
	return false, s.writeSignal.wait(), c.sendSignal.wait(), s.writeDeadline, nil
}

func (s *stream) waitSendCredit() error {
	for {
		ok, wake, connWake, deadline, err := s.trySend()
		if ok || err != nil {
			return err
		}
		if err := wait(deadline, wake, connWake); err != nil {
			return err
		}
	}
}

// reserve takes flow control credit for as much of n bytes as it can, and
// returns the offset to send them at.
func (s *stream) reserve(n int) (offset uint64, reserved int, err error) {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := s.sendErrLocked(); err != nil {
		return 0, 0, err
	}
	reserved = int(min(uint64(n), maxStreamData, s.sendCreditLocked()))
	offset = s.sendOffset
	s.sendOffset += uint64(reserved)
	c.sent += uint64(reserved)
	return offset, reserved, nil
}

// writeChunk sends the start of p in one record. It may send nothing when
// another stream took the connection's credit first.
func (s *stream) writeChunk(p []byte) (int, error) {
	c := s.c
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	offset, n, err := s.reserve(len(p))
	if err != nil || n == 0 {
		return 0, err
	}
	// The data is copied into the record here, so p is not retained.
	if err := c.writeFramesLocked(&wire.Stream{StreamID: uint64(s.id), Offset: offset, Data: p[:n]}); err != nil {
		c.abort(fmt.Errorf("qmux: write to transport: %w", err))
		return 0, context.Cause(c.ctx)
	}
	return n, nil
}

// finish marks the sending side as ended and returns the final size. It
// reports false when there is nothing left to send: the stream has already
// ended, or was reset.
func (s *stream) finish() (finalSize uint64, ok bool, err error) {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeErr != nil {
		return 0, false, c.closeErr
	}
	if s.finSent || s.sendErr != nil {
		return 0, false, nil
	}
	s.finSent = true
	s.cancel(nil)
	s.writeSignal.broadcast()
	c.completeLocked(s)
	return s.sendOffset, true, nil
}

func (s *stream) closeSend() error {
	c := s.c
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	finalSize, ok, err := s.finish()
	if !ok {
		return err
	}
	if err := c.writeFramesLocked(&wire.Stream{StreamID: uint64(s.id), Offset: finalSize, Fin: true}); err != nil {
		c.abort(fmt.Errorf("qmux: write to transport: %w", err))
		return context.Cause(c.ctx)
	}
	return nil
}

func (s *stream) cancelWrite(code StreamErrorCode) {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeErr != nil {
		return
	}
	s.resetSendLocked(&quic.StreamError{StreamID: s.id, ErrorCode: code})
}

// stopSendingLocked handles the peer's STOP_SENDING: the stream is reset
// with the peer's error code (RFC 9000, Section 3.5).
func (s *stream) stopSendingLocked(code StreamErrorCode) {
	s.resetSendLocked(&quic.StreamError{StreamID: s.id, ErrorCode: code, Remote: true})
}

func (s *stream) resetSendLocked(reason *quic.StreamError) {
	if s.sendDone() {
		return
	}
	c := s.c
	s.sendErr = reason
	s.cancel(reason)
	c.queueLocked(&wire.ResetStream{StreamID: uint64(s.id), Code: uint64(reason.ErrorCode), FinalSize: s.sendOffset})
	s.writeSignal.broadcast()
	c.completeLocked(s)
}

func (s *stream) setReadDeadline(t time.Time) {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	s.readDeadline = t
	s.readSignal.broadcast()
}

func (s *stream) setWriteDeadline(t time.Time) {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	s.writeDeadline = t
	s.writeSignal.broadcast()
}

var (
	_ io.Reader          = (*ReceiveStream)(nil)
	_ io.WriteCloser     = (*SendStream)(nil)
	_ io.ReadWriteCloser = (*Stream)(nil)
)

// ReceiveStream is the receiving side of a unidirectional stream.
type ReceiveStream struct {
	s *stream
}

// StreamID returns the stream's identifier.
func (s *ReceiveStream) StreamID() StreamID { return s.s.id }

// Read reads data from the stream. It returns io.EOF once the peer has
// closed the stream and everything was read, a *quic.StreamError after a
// reset or CancelRead, and os.ErrDeadlineExceeded past the read deadline.
func (s *ReceiveStream) Read(p []byte) (int, error) { return s.s.read(p) }

// CancelRead abandons reading: buffered data is discarded, and the peer is
// asked to stop sending with the given error code.
func (s *ReceiveStream) CancelRead(code StreamErrorCode) { s.s.cancelRead(code) }

// SetReadDeadline sets the deadline for Read calls, pending and future. A
// zero value means no deadline.
func (s *ReceiveStream) SetReadDeadline(t time.Time) error {
	s.s.setReadDeadline(t)
	return nil
}

// SendStream is the sending side of a unidirectional stream.
type SendStream struct {
	s *stream
}

// StreamID returns the stream's identifier.
func (s *SendStream) StreamID() StreamID { return s.s.id }

// Write writes data to the stream, waiting for the peer's flow control
// while it has to. It does not retain p. It returns a *quic.StreamError
// after CancelWrite or once the peer has stopped reading, and
// os.ErrDeadlineExceeded past the write deadline.
func (s *SendStream) Write(p []byte) (int, error) { return s.s.write(p) }

// Close ends the stream: the peer reads io.EOF after the data written so
// far. It is a no-op after CancelWrite.
func (s *SendStream) Close() error { return s.s.closeSend() }

// CancelWrite resets the stream with the given error code. Data the peer
// has not read yet may be lost.
func (s *SendStream) CancelWrite(code StreamErrorCode) { s.s.cancelWrite(code) }

// Context returns a context that is cancelled when the sending side ends:
// on Close, CancelWrite, the peer's request to stop sending, or the close
// of the connection. The cause of a reset is its *quic.StreamError.
func (s *SendStream) Context() context.Context { return s.s.ctx }

// SetWriteDeadline sets the deadline for Write calls, pending and future.
// A zero value means no deadline.
func (s *SendStream) SetWriteDeadline(t time.Time) error {
	s.s.setWriteDeadline(t)
	return nil
}

// Stream is a bidirectional stream.
type Stream struct {
	s *stream
}

// StreamID returns the stream's identifier.
func (s *Stream) StreamID() StreamID { return s.s.id }

// Read reads data from the stream. See ReceiveStream.Read.
func (s *Stream) Read(p []byte) (int, error) { return s.s.read(p) }

// Write writes data to the stream. See SendStream.Write.
func (s *Stream) Write(p []byte) (int, error) { return s.s.write(p) }

// Close ends the sending side of the stream. The receiving side stays
// open: read it to the end, or call CancelRead.
func (s *Stream) Close() error { return s.s.closeSend() }

// CancelRead abandons reading. See ReceiveStream.CancelRead.
func (s *Stream) CancelRead(code StreamErrorCode) { s.s.cancelRead(code) }

// CancelWrite resets the sending side. See SendStream.CancelWrite.
func (s *Stream) CancelWrite(code StreamErrorCode) { s.s.cancelWrite(code) }

// Context returns a context that is cancelled when the sending side ends.
// See SendStream.Context.
func (s *Stream) Context() context.Context { return s.s.ctx }

// SetDeadline sets the read and write deadlines.
func (s *Stream) SetDeadline(t time.Time) error {
	s.s.setReadDeadline(t)
	s.s.setWriteDeadline(t)
	return nil
}

// SetReadDeadline sets the deadline for Read calls.
func (s *Stream) SetReadDeadline(t time.Time) error {
	s.s.setReadDeadline(t)
	return nil
}

// SetWriteDeadline sets the deadline for Write calls.
func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.s.setWriteDeadline(t)
	return nil
}
