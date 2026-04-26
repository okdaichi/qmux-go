package qmux

import (
	"fmt"

	"github.com/quic-go/quic-go"
)

// TransportErrorCode is a QUIC transport error code.
type TransportErrorCode = quic.TransportErrorCode

// ApplicationErrorCode is a QUIC application error code.
type ApplicationErrorCode = quic.ApplicationErrorCode

const (
	InternalError           TransportErrorCode = quic.InternalError
	FlowControlError        TransportErrorCode = 0x03
	StreamLimitError        TransportErrorCode = 0x04
	TransportParameterError TransportErrorCode = 0x08
	ProtocolViolationError  TransportErrorCode = 0x0a
)

// Error is an error returned by QMux operations.
type Error struct {
	ErrorCode TransportErrorCode
	Message   string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.ErrorCode.String(), e.Message)
	}
	return e.ErrorCode.String()
}
