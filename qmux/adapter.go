package qmux

import (
	"io"
	"net"
	"time"
)

// MessageConn defines an interface for message-oriented transports (like WebSockets).
// It allows these transports to be adapted into a stream-based net.Conn for use with QMux.
type MessageConn interface {
	// ReadMessage reads a single message/packet from the transport.
	ReadMessage() ([]byte, error)
	// WriteMessage writes a single message/packet to the transport.
	WriteMessage([]byte) error
	// io.Closer for connection termination.
	io.Closer
}

// NewNetConn wraps a MessageConn to implement the standard net.Conn interface.
// This enables QMux to run over any message-based transport (like Gorilla WebSocket)
// without needing library-specific integration code.
func NewNetConn(mc MessageConn) net.Conn {
	return &messageConnAdapter{mc: mc}
}

type messageConnAdapter struct {
	mc  MessageConn
	buf []byte
}

func (a *messageConnAdapter) Read(b []byte) (int, error) {
	if len(a.buf) == 0 {
		p, err := a.mc.ReadMessage()
		if err != nil {
			return 0, err
		}
		a.buf = p
	}

	n := copy(b, a.buf)
	a.buf = a.buf[n:]
	return n, nil
}

func (a *messageConnAdapter) Write(b []byte) (int, error) {
	err := a.mc.WriteMessage(b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (a *messageConnAdapter) Close() error {
	return a.mc.Close()
}

// NetConnWithAddr allows providing specific addresses for the adapter.
type NetConnWithAddr interface {
	MessageConn
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
}

// NetConnWithDeadlines allows providing deadline control for the adapter.
type NetConnWithDeadlines interface {
	MessageConn
	SetDeadline(t time.Time) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

func (a *messageConnAdapter) LocalAddr() net.Addr {
	if wa, ok := a.mc.(NetConnWithAddr); ok {
		return wa.LocalAddr()
	}
	return &net.TCPAddr{IP: net.IPv4zero, Port: 0}
}

func (a *messageConnAdapter) RemoteAddr() net.Addr {
	if wa, ok := a.mc.(NetConnWithAddr); ok {
		return wa.RemoteAddr()
	}
	return &net.TCPAddr{IP: net.IPv4zero, Port: 0}
}

func (a *messageConnAdapter) SetDeadline(t time.Time) error {
	if wd, ok := a.mc.(NetConnWithDeadlines); ok {
		return wd.SetDeadline(t)
	}
	return nil
}

func (a *messageConnAdapter) SetReadDeadline(t time.Time) error {
	if wd, ok := a.mc.(NetConnWithDeadlines); ok {
		return wd.SetReadDeadline(t)
	}
	return nil
}

func (a *messageConnAdapter) SetWriteDeadline(t time.Time) error {
	if wd, ok := a.mc.(NetConnWithDeadlines); ok {
		return wd.SetWriteDeadline(t)
	}
	return nil
}
