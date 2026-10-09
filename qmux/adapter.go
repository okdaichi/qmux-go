package qmux

import (
	"io"
	"net"
	"time"
)

// MessageConn is a reliable, ordered, message-oriented transport, such as a
// WebSocket carrying binary messages.
type MessageConn interface {
	// ReadMessage reads the next message. The returned slice is not
	// modified by the caller, and is not used after the next call.
	ReadMessage() ([]byte, error)
	// WriteMessage writes one message. It must not retain p.
	WriteMessage(p []byte) error
	io.Closer
}

// NetConn adapts a MessageConn to a net.Conn that treats the messages as a
// byte stream: message boundaries carry no meaning. A QMux connection over
// the result sends each record with its Size field, as over TCP.
//
// Use DialMessages or ServerMessages instead to send one record per
// message.
func NetConn(mc MessageConn) net.Conn {
	return &messageConnAdapter{mc: mc}
}

var _ net.Conn = (*messageConnAdapter)(nil)

type messageConnAdapter struct {
	mc  MessageConn
	buf []byte
}

func (a *messageConnAdapter) Read(b []byte) (int, error) {
	for len(a.buf) == 0 {
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
	if err := a.mc.WriteMessage(b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (a *messageConnAdapter) Close() error {
	return a.mc.Close()
}

func (a *messageConnAdapter) LocalAddr() net.Addr {
	if wa, ok := a.mc.(interface{ LocalAddr() net.Addr }); ok {
		return wa.LocalAddr()
	}
	return unknownAddr{}
}

func (a *messageConnAdapter) RemoteAddr() net.Addr {
	if wa, ok := a.mc.(interface{ RemoteAddr() net.Addr }); ok {
		return wa.RemoteAddr()
	}
	return unknownAddr{}
}

func (a *messageConnAdapter) SetDeadline(t time.Time) error {
	if wd, ok := a.mc.(interface{ SetDeadline(time.Time) error }); ok {
		return wd.SetDeadline(t)
	}
	return nil
}

func (a *messageConnAdapter) SetReadDeadline(t time.Time) error {
	if wd, ok := a.mc.(interface{ SetReadDeadline(time.Time) error }); ok {
		return wd.SetReadDeadline(t)
	}
	return nil
}

func (a *messageConnAdapter) SetWriteDeadline(t time.Time) error {
	if wd, ok := a.mc.(interface{ SetWriteDeadline(time.Time) error }); ok {
		return wd.SetWriteDeadline(t)
	}
	return nil
}
