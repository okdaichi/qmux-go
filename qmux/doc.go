/*
Package qmux implements QMux: QUIC's streams, flow control and datagrams
over a reliable, ordered transport such as TLS over TCP or a WebSocket. It
follows draft-ietf-quic-qmux-02.

# Usage

A connection runs over a transport that is already established. Its API
mirrors that of a quic-go connection:

	// Server
	conn, _ := ln.Accept()
	sess, _ := qmux.Server(conn, nil)
	stream, _ := sess.AcceptStream(ctx)

	// Client
	conn, _ := tls.Dial("tcp", addr, tlsConfig)
	sess, _ := qmux.Dial(conn, nil)
	stream, _ := sess.OpenStreamSync(ctx)

# Application protocol

QMux does not negotiate the application protocol, and has no identifier of
its own: the transport does it (Section 8.1 of the draft). Over TLS that is
ALPN. Over WebSocket it is the subprotocol, which by convention also names
the QMux draft, as in "qmux-02.myapp".

# WebSocket

DialMessages and ServerMessages run a connection over a MessageConn, with one
record per message. The message boundary delimits the record, so the record's
Size field is not sent. This is the mapping other QMux implementations use
over WebSocket.

	// With gorilla/websocket
	type wsConn struct{ *websocket.Conn }

	func (c wsConn) ReadMessage() ([]byte, error) {
		_, p, err := c.Conn.ReadMessage()
		return p, err
	}

	func (c wsConn) WriteMessage(p []byte) error {
		return c.Conn.WriteMessage(websocket.BinaryMessage, p)
	}

	sess, _ := qmux.ServerMessages(wsConn{ws}, nil)

NetConn adapts a MessageConn to a net.Conn instead, for peers that treat the
messages as a plain byte stream.

# Differences from QUIC

All streams share one ordered transport: a lost segment delays every stream,
and a datagram, once sent, is delivered reliably. Stream priorities order the
writes that wait for the transport; what the transport already holds is sent
in the order it was written.
*/
package qmux
