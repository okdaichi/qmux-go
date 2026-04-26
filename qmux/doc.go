/*
Package qmux implements the QMux protocol, providing QUIC-like stream and datagram operations
over reliable, bi-directional byte streams.

It strictly follows the draft-ietf-quic-qmux-01 specification, enabling multiplexing
semantics (like multiple streams and unreliable datagrams) over transports that are normally
limited to a single stream, such as TCP, TLS, or WebSockets.

# Architecture

QMux uses a layered architecture:
  - Transport Layer: Any reliable byte-stream (net.Conn).
  - Record Layer: Self-delimiting chunks of data containing QMux frames.
  - Multiplexing Layer: Manages stream IDs, flow control, and datagrams.

# Basic Usage (TCP)

A QMux connection starts with an existing net.Conn. You can wrap it as a server or a client:

	// Server
	ln, _ := net.Listen("tcp", "localhost:8080")
	conn, _ := ln.Accept()
	sess, _ := qmux.Server(conn, nil)
	stream, _ := sess.AcceptStream(context.Background())

	// Client
	conn, _ := net.Dial("tcp", "localhost:8080")
	sess, _ := qmux.Dial(conn, nil)
	stream, _ := sess.OpenStream()

# WebSocket Support

QMux can run over WebSockets by using the provided MessageConn adapter. This allows QMux to
map its records directly to WebSocket messages:

	// With gorilla/websocket
	type wsAdapter struct { *websocket.Conn }
	func (a *wsAdapter) ReadMessage() ([]byte, error) { _, p, err := a.ReadMessage(); return p, err }
	func (a *wsAdapter) WriteMessage(p []byte) error  { return a.WriteMessage(websocket.BinaryMessage, p) }

	nc := qmux.NewNetConn(&wsAdapter{conn})
	sess, _ := qmux.Dial(nc, nil)

# Protocol Negotiation

QMux supports in-band application protocol negotiation (similar to ALPN) via Transport Parameters.
This allows a single QMux connection to support multiple application protocols:

	config := qmux.DefaultConfig()
	config.ApplicationProtocols = []string{"moq-v1"}
	sess, _ := qmux.Dial(conn, config)
	
	// Check negotiated protocol
	proto := sess.ConnectionState().TLS.NegotiatedProtocol

# Performance

The implementation is optimized for high-performance and low-allocation scenarios, utilizing
buffer pooling, atomic state management, and the quic-go/quicvarint library.
*/
package qmux
