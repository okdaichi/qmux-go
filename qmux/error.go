package qmux

import (
	"errors"

	"github.com/quic-go/quic-go"
)

// TransportErrorCode is a QUIC transport error code.
type TransportErrorCode = quic.TransportErrorCode

// ApplicationErrorCode is a QUIC application error code.
type ApplicationErrorCode = quic.ApplicationErrorCode

// StreamErrorCode is a QUIC stream error code.
type StreamErrorCode = quic.StreamErrorCode

// The transport error codes of QUIC version 1 (RFC 9000, Section 20.1). A
// connection that ends on one fails with a *quic.TransportError.
const (
	NoError                 TransportErrorCode = 0x00
	InternalError           TransportErrorCode = 0x01
	ConnectionRefused       TransportErrorCode = 0x02
	FlowControlError        TransportErrorCode = 0x03
	StreamLimitError        TransportErrorCode = 0x04
	StreamStateError        TransportErrorCode = 0x05
	FinalSizeError          TransportErrorCode = 0x06
	FrameEncodingError      TransportErrorCode = 0x07
	TransportParameterError TransportErrorCode = 0x08
	ConnectionIDLimitError  TransportErrorCode = 0x09
	ProtocolViolationError  TransportErrorCode = 0x0a
	InvalidTokenError       TransportErrorCode = 0x0b
	ApplicationError        TransportErrorCode = 0x0c
	CryptoBufferExceeded    TransportErrorCode = 0x0d
	KeyUpdateError          TransportErrorCode = 0x0e
	AEADLimitReached        TransportErrorCode = 0x0f
	NoViablePath            TransportErrorCode = 0x10
)

var (
	// ErrStreamLimitReached is returned by OpenStream and OpenUniStream
	// when the peer's stream limit does not allow another stream yet.
	ErrStreamLimitReached = errors.New("qmux: stream limit reached")

	// ErrDatagramsNotSupported is returned by SendDatagram when the peer
	// does not accept datagrams.
	ErrDatagramsNotSupported = errors.New("qmux: peer does not accept datagrams")

	// ErrDatagramTooLarge is returned by SendDatagram for a datagram larger
	// than the peer accepts.
	ErrDatagramTooLarge = errors.New("qmux: datagram too large")
)
