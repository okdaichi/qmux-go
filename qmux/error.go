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
	NoError                   TransportErrorCode = 0x00
	InternalError             TransportErrorCode = 0x01
	ConnectionRefused         TransportErrorCode = 0x02
	FlowControlError          TransportErrorCode = 0x03
	StreamLimitError          TransportErrorCode = 0x04
	StreamStateError          TransportErrorCode = 0x05
	FinalSizeError            TransportErrorCode = 0x06
	FrameEncodingError        TransportErrorCode = 0x07
	TransportParameterError   TransportErrorCode = 0x08
	ConnectionIDLimitError    TransportErrorCode = 0x09
	ProtocolViolationError    TransportErrorCode = 0x0a
	InvalidTokenError         TransportErrorCode = 0x0b
	ApplicationError          TransportErrorCode = 0x0c
	CryptoBufferExceeded      TransportErrorCode = 0x0d
	KeyUpdateError            TransportErrorCode = 0x0e
	AEADLimitReached          TransportErrorCode = 0x0f
	NoViablePath              TransportErrorCode = 0x10
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
