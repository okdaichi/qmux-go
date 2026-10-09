package qmux

import (
	"net"
	"time"

	"github.com/okdaichi/qmux-go/qmux/internal/wire"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/quicvarint"
)

// StreamID is a 62-bit integer as defined in QUIC.
type StreamID = quic.StreamID

const (
	defaultMaxIncomingStreams      = 100
	defaultMaxIncomingUniStreams   = 100
	defaultStreamReceiveWindow     = 512 * 1024
	defaultConnectionReceiveWindow = 1024 * 1024
	defaultMaxIdleTimeout          = 30 * time.Second
	defaultHandshakeIdleTimeout    = 5 * time.Second
	defaultMaxDatagramFrameSize    = 1200
)

// Config contains configuration for a QMux connection. A zero field takes
// its default.
type Config struct {
	// MaxIncomingStreams is the number of bidirectional streams the peer
	// may have open at a time. The default is 100; a negative value allows
	// none. The peer can open that many with a single frame, so the limit
	// also bounds the memory a peer can claim.
	MaxIncomingStreams int64
	// MaxIncomingUniStreams is the number of unidirectional streams the
	// peer may have open at a time. The default is 100; a negative value
	// allows none.
	MaxIncomingUniStreams int64
	// InitialStreamReceiveWindow is the flow control window of each stream:
	// the most data the peer may send ahead of what the application has
	// read. The default is 512 KiB.
	InitialStreamReceiveWindow uint64
	// InitialConnectionReceiveWindow is the flow control window of the
	// connection, across all streams. The default is 1 MiB.
	InitialConnectionReceiveWindow uint64
	// MaxRecordSize is the largest record the peer may send. The default,
	// which is also the minimum, is 16382 bytes.
	MaxRecordSize uint64
	// KeepAlivePeriod is the interval between QX_PING frames. Zero sends
	// none. It is at most half of MaxIdleTimeout.
	KeepAlivePeriod time.Duration
	// HandshakeIdleTimeout closes the connection when the peer's transport
	// parameters have not arrived within this long. The default is 5
	// seconds.
	HandshakeIdleTimeout time.Duration
	// MaxIdleTimeout closes the connection when no record was sent or
	// received for this long. The peer's own timeout applies when it is
	// shorter. The default is 30 seconds; a negative value declares none.
	MaxIdleTimeout time.Duration
	// EnableDatagrams enables the datagram extension (RFC 9221). QMux
	// delivers datagrams reliably and in order.
	EnableDatagrams bool
	// MaxDatagramFrameSize is the largest DATAGRAM frame the peer may send
	// when datagrams are enabled. The default is 1200 bytes.
	MaxDatagramFrameSize uint64
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	c := (*Config)(nil).normalized()
	return &c
}

// Clone returns a copy of the configuration.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	clone := *c
	return &clone
}

// normalized returns a copy of the configuration with every default filled
// in and every limit within what the protocol allows.
func (c *Config) normalized() Config {
	var n Config
	if c != nil {
		n = *c
	}
	n.MaxIncomingStreams = streamLimit(n.MaxIncomingStreams, defaultMaxIncomingStreams)
	n.MaxIncomingUniStreams = streamLimit(n.MaxIncomingUniStreams, defaultMaxIncomingUniStreams)
	if n.InitialStreamReceiveWindow == 0 {
		n.InitialStreamReceiveWindow = defaultStreamReceiveWindow
	}
	if n.InitialConnectionReceiveWindow == 0 {
		n.InitialConnectionReceiveWindow = defaultConnectionReceiveWindow
	}
	// Transport parameters are variable-length integers.
	n.InitialStreamReceiveWindow = min(n.InitialStreamReceiveWindow, quicvarint.Max)
	n.InitialConnectionReceiveWindow = min(n.InitialConnectionReceiveWindow, quicvarint.Max)
	n.MaxRecordSize = min(max(n.MaxRecordSize, wire.DefaultMaxRecordSize), quicvarint.Max)
	if n.MaxIdleTimeout == 0 {
		n.MaxIdleTimeout = defaultMaxIdleTimeout
	}
	n.MaxIdleTimeout = max(n.MaxIdleTimeout, 0)
	if n.HandshakeIdleTimeout <= 0 {
		n.HandshakeIdleTimeout = defaultHandshakeIdleTimeout
	}
	if n.KeepAlivePeriod > 0 && n.MaxIdleTimeout > 0 {
		n.KeepAlivePeriod = min(n.KeepAlivePeriod, n.MaxIdleTimeout/2)
	}
	if !n.EnableDatagrams {
		n.MaxDatagramFrameSize = 0
	} else if n.MaxDatagramFrameSize == 0 {
		n.MaxDatagramFrameSize = defaultMaxDatagramFrameSize
	}
	n.MaxDatagramFrameSize = min(n.MaxDatagramFrameSize, n.MaxRecordSize)
	return n
}

func streamLimit(v, def int64) int64 {
	switch {
	case v == 0:
		return def
	case v < 0:
		return 0
	}
	return min(v, 1<<60)
}

// Dial starts a QMux connection as the client over conn, an established
// byte stream such as a TLS connection. It returns without waiting for the
// peer: operations on the connection wait for the peer's transport
// parameters as they need them. The connection owns conn from here on.
//
// QMux does not negotiate the application protocol. The transport must
// have, for example with ALPN.
func Dial(conn net.Conn, config *Config) (*Conn, error) {
	cfg := config.normalized()
	return newConn(newStreamTransport(conn, cfg.MaxRecordSize), cfg, false), nil
}

// Server starts a QMux connection as the server over conn. See Dial.
func Server(conn net.Conn, config *Config) (*Conn, error) {
	cfg := config.normalized()
	return newConn(newStreamTransport(conn, cfg.MaxRecordSize), cfg, true), nil
}

// DialMessages starts a QMux connection as the client over a message
// transport, sending one record per message. The message boundary delimits
// the record, so the record's Size field is not sent. This is the mapping
// that QMux implementations use over WebSocket, where the negotiated
// subprotocol names the application protocol and the QMux draft.
func DialMessages(mc MessageConn, config *Config) (*Conn, error) {
	cfg := config.normalized()
	return newConn(&messageTransport{mc: mc, max: cfg.MaxRecordSize}, cfg, false), nil
}

// ServerMessages starts a QMux connection as the server over a message
// transport. See DialMessages.
func ServerMessages(mc MessageConn, config *Config) (*Conn, error) {
	cfg := config.normalized()
	return newConn(&messageTransport{mc: mc, max: cfg.MaxRecordSize}, cfg, true), nil
}
