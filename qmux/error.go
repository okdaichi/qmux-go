package qmux

import (
	"errors"

	"github.com/quic-go/quic-go"
)

// TransportErrorCode is a QUIC transport error code. quic-go names the
// codes: quic.ProtocolViolation, quic.FlowControlError and so on. A
// connection that ends on one fails with a *quic.TransportError.
type TransportErrorCode = quic.TransportErrorCode

// ApplicationErrorCode is a QUIC application error code.
type ApplicationErrorCode = quic.ApplicationErrorCode

// StreamErrorCode is a QUIC stream error code.
type StreamErrorCode = quic.StreamErrorCode

// ErrDatagramsNotSupported is returned by SendDatagram when the peer does
// not accept datagrams.
var ErrDatagramsNotSupported = errors.New("qmux: peer does not accept datagrams")
