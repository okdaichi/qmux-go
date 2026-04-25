// Package qmux implements the QMux protocol, providing QUIC-like stream and datagram operations
// over reliable, bi-directional byte streams.
// It follows the draft-ietf-quic-qmux-01 specification.
package qmux

import (
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

// StreamID is a 62-bit integer as defined in QUIC.
type StreamID = quic.StreamID

// Config contains configuration for a QMux connection.
type Config struct {
	// MaxIncomingStreams is the maximum number of concurrent bidirectional streams
	// that a peer is allowed to open.
	MaxIncomingStreams int64
	// MaxIncomingUniStreams is the maximum number of concurrent unidirectional streams
	// that a peer is allowed to open.
	MaxIncomingUniStreams int64
	// InitialStreamReceiveWindow is the initial flow control window for each stream.
	InitialStreamReceiveWindow uint64
	// InitialConnectionReceiveWindow is the initial flow control window for the connection.
	InitialConnectionReceiveWindow uint64
	// MaxRecordSize is the maximum size of a QMux record.
	MaxRecordSize uint64
	// KeepAlivePeriod is the interval between QX_PING frames.
	KeepAlivePeriod time.Duration
}

// Dial establishes a QMux connection over an existing network connection.
// It acts as a client.
func Dial(conn net.Conn, config *Config) (*Conn, error) {
	s := newSession(conn, config, false)
	go s.run()

	select {
	case <-s.handshakeDone:
		return s, nil
	case <-s.ctx.Done():
		return nil, s.closeErr
	}
}

// Server starts a QMux connection over an existing network connection.
// It acts as a server.
func Server(conn net.Conn, config *Config) (*Conn, error) {
	s := newSession(conn, config, true)
	go s.run()

	select {
	case <-s.handshakeDone:
		return s, nil
	case <-s.ctx.Done():
		return nil, s.closeErr
	}
}
