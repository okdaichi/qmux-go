package qmux

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/okdaichi/qmux-go/qmux/internal/wire"
	"github.com/quic-go/quic-go/quicvarint"
)

// recordHeadroom is the space a record buffer leaves before its Frames
// field, for the transport to put the Size field in.
const recordHeadroom = 8

// closeTimeout bounds how long a closing connection waits for its last
// record to be written and for the peer to shut down in turn.
const closeTimeout = time.Second

// recordTransport carries QMux Records. Reads and writes may run
// concurrently with each other, but not with themselves.
type recordTransport interface {
	// readRecord returns the Frames field of the next record. It is valid
	// until the next call.
	readRecord() ([]byte, error)
	// writeRecord writes one record and returns the bytes put on the
	// transport. buf[recordHeadroom:] is the Frames field; buf is not
	// retained.
	writeRecord(buf []byte) (int, error)
	// closeWrite shuts down the sending side and reports whether the
	// transport can do so while still receiving.
	closeWrite() bool
	// expire makes blocked and future writes fail by the time t.
	expire(t time.Time)
	close() error
	localAddr() net.Addr
	remoteAddr() net.Addr
	tlsState() tls.ConnectionState
}

func recordTooLarge(size, limit uint64) error {
	return &wire.Error{
		Code:   wire.FrameEncodingError,
		Reason: fmt.Sprintf("record of %d bytes exceeds max_record_size %d", size, limit),
	}
}

// streamTransport carries records on a byte stream, each one preceded by
// its Size field (Section 3.2).
type streamTransport struct {
	conn net.Conn
	br   *bufio.Reader
	buf  []byte
	// max is the max_record_size this endpoint declared.
	max uint64
}

var _ recordTransport = (*streamTransport)(nil)

func newStreamTransport(conn net.Conn, maxRecordSize uint64) *streamTransport {
	return &streamTransport{conn: conn, br: bufio.NewReader(conn), max: maxRecordSize}
}

func (t *streamTransport) readRecord() ([]byte, error) {
	size, err := quicvarint.Read(t.br)
	if err != nil {
		return nil, err
	}
	if size > t.max {
		// Skip the record, so that the stream stays aligned while the
		// connection closes.
		if _, err := io.CopyN(io.Discard, t.br, int64(size)); err != nil {
			return nil, err
		}
		return nil, recordTooLarge(size, t.max)
	}
	if uint64(cap(t.buf)) < size {
		t.buf = make([]byte, size)
	}
	t.buf = t.buf[:size]
	if _, err := io.ReadFull(t.br, t.buf); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return t.buf, nil
}

func (t *streamTransport) writeRecord(buf []byte) (int, error) {
	size := uint64(len(buf) - recordHeadroom)
	start := recordHeadroom - quicvarint.Len(size)
	quicvarint.Append(buf[start:start], size)
	return t.conn.Write(buf[start:])
}

func (t *streamTransport) closeWrite() bool {
	hc, ok := t.conn.(interface{ CloseWrite() error })
	if !ok {
		return false
	}
	return hc.CloseWrite() == nil
}

func (t *streamTransport) expire(deadline time.Time) {
	_ = t.conn.SetWriteDeadline(deadline) // not actionable: the connection is closing either way
}

func (t *streamTransport) close() error         { return t.conn.Close() }
func (t *streamTransport) localAddr() net.Addr  { return t.conn.LocalAddr() }
func (t *streamTransport) remoteAddr() net.Addr { return t.conn.RemoteAddr() }

func (t *streamTransport) tlsState() tls.ConnectionState {
	return tlsStateOf(t.conn)
}

// messageTransport carries one record per message. The message boundary
// delimits the record, so the Size field is not sent.
type messageTransport struct {
	mc  MessageConn
	max uint64
}

var _ recordTransport = (*messageTransport)(nil)

func (t *messageTransport) readRecord() ([]byte, error) {
	p, err := t.mc.ReadMessage()
	if err != nil {
		return nil, err
	}
	if uint64(len(p)) > t.max {
		return nil, recordTooLarge(uint64(len(p)), t.max)
	}
	return p, nil
}

func (t *messageTransport) writeRecord(buf []byte) (int, error) {
	if err := t.mc.WriteMessage(buf[recordHeadroom:]); err != nil {
		return 0, err
	}
	return len(buf) - recordHeadroom, nil
}

// closeWrite reports false: a message transport closes both directions at
// once, with a closing handshake of its own.
func (t *messageTransport) closeWrite() bool { return false }

func (t *messageTransport) expire(deadline time.Time) {
	if d, ok := t.mc.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = d.SetWriteDeadline(deadline) // not actionable: the connection is closing either way
	}
}

func (t *messageTransport) close() error { return t.mc.Close() }

func (t *messageTransport) localAddr() net.Addr {
	if a, ok := t.mc.(interface{ LocalAddr() net.Addr }); ok {
		return a.LocalAddr()
	}
	return unknownAddr{}
}

func (t *messageTransport) remoteAddr() net.Addr {
	if a, ok := t.mc.(interface{ RemoteAddr() net.Addr }); ok {
		return a.RemoteAddr()
	}
	return unknownAddr{}
}

func (t *messageTransport) tlsState() tls.ConnectionState {
	return tlsStateOf(t.mc)
}

func tlsStateOf(v any) tls.ConnectionState {
	if c, ok := v.(interface{ ConnectionState() tls.ConnectionState }); ok {
		return c.ConnectionState()
	}
	return tls.ConnectionState{}
}

// unknownAddr is the address of a transport that does not report one.
type unknownAddr struct{}

func (unknownAddr) Network() string { return "qmux" }
func (unknownAddr) String() string  { return "unknown" }
