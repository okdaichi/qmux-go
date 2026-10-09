package wire

import (
	"fmt"

	"github.com/quic-go/quic-go/quicvarint"
)

// DefaultMaxRecordSize is the value of max_record_size when the transport
// parameter is absent, and the smallest value it can take.
const DefaultMaxRecordSize = 16382

// Transport parameter identifiers.
const (
	paramMaxIdleTimeout              uint64 = 0x01
	paramInitialMaxData              uint64 = 0x04
	paramInitialMaxStreamDataBidiLoc uint64 = 0x05
	paramInitialMaxStreamDataBidiRem uint64 = 0x06
	paramInitialMaxStreamDataUni     uint64 = 0x07
	paramInitialMaxStreamsBidi       uint64 = 0x08
	paramInitialMaxStreamsUni        uint64 = 0x09
	paramMaxDatagramFrameSize        uint64 = 0x20
	paramMaxRecordSize               uint64 = 0x0571c59429cd0845
)

// prohibitedParam names the transport parameters of QUIC version 1 that
// QMux forbids (Section 5.1), and returns "" for any other.
func prohibitedParam(id uint64) string {
	switch id {
	case 0x00:
		return "original_destination_connection_id"
	case 0x02:
		return "stateless_reset_token"
	case 0x03:
		return "max_udp_payload_size"
	case 0x0a:
		return "ack_delay_exponent"
	case 0x0b:
		return "max_ack_delay"
	case 0x0c:
		return "disable_active_migration"
	case 0x0d:
		return "preferred_address"
	case 0x0e:
		return "active_connection_id_limit"
	case 0x0f:
		return "initial_source_connection_id"
	case 0x10:
		return "retry_source_connection_id"
	}
	return ""
}

// Parameters are the transport parameters an endpoint declares. A zero field
// is the parameter's default, and is not sent.
type Parameters struct {
	// MaxIdleTimeout is in milliseconds.
	MaxIdleTimeout                 uint64
	InitialMaxData                 uint64
	InitialMaxStreamDataBidiLocal  uint64
	InitialMaxStreamDataBidiRemote uint64
	InitialMaxStreamDataUni        uint64
	InitialMaxStreamsBidi          uint64
	InitialMaxStreamsUni           uint64
	// MaxDatagramFrameSize is zero when datagrams are not supported.
	MaxDatagramFrameSize uint64
	// MaxRecordSize is DefaultMaxRecordSize after parsing when absent; zero
	// stands for the default when sending.
	MaxRecordSize uint64
}

// field returns where the parameter id is stored, or nil for a parameter
// this implementation does not know.
func (p *Parameters) field(id uint64) *uint64 {
	switch id {
	case paramMaxIdleTimeout:
		return &p.MaxIdleTimeout
	case paramInitialMaxData:
		return &p.InitialMaxData
	case paramInitialMaxStreamDataBidiLoc:
		return &p.InitialMaxStreamDataBidiLocal
	case paramInitialMaxStreamDataBidiRem:
		return &p.InitialMaxStreamDataBidiRemote
	case paramInitialMaxStreamDataUni:
		return &p.InitialMaxStreamDataUni
	case paramInitialMaxStreamsBidi:
		return &p.InitialMaxStreamsBidi
	case paramInitialMaxStreamsUni:
		return &p.InitialMaxStreamsUni
	case paramMaxDatagramFrameSize:
		return &p.MaxDatagramFrameSize
	case paramMaxRecordSize:
		return &p.MaxRecordSize
	}
	return nil
}

func (p *Parameters) append(b []byte) []byte {
	for _, id := range [...]uint64{
		paramMaxIdleTimeout,
		paramInitialMaxData,
		paramInitialMaxStreamDataBidiLoc,
		paramInitialMaxStreamDataBidiRem,
		paramInitialMaxStreamDataUni,
		paramInitialMaxStreamsBidi,
		paramInitialMaxStreamsUni,
		paramMaxDatagramFrameSize,
		paramMaxRecordSize,
	} {
		value := *p.field(id)
		if value == 0 {
			continue
		}
		b = quicvarint.Append(b, id)
		b = quicvarint.Append(b, uint64(quicvarint.Len(value)))
		b = quicvarint.Append(b, value)
	}
	return b
}

func parameterError(reason string) *Error {
	return &Error{Code: TransportParameterError, Reason: reason}
}

// parseParameters parses the Transport Parameters field of a
// QX_TRANSPORT_PARAMETERS frame. Unknown parameters are ignored.
func parseParameters(b []byte) (Parameters, error) {
	p := Parameters{MaxRecordSize: DefaultMaxRecordSize}
	seen := make(map[uint64]struct{})
	r := &reader{b: b}
	for len(r.b) > 0 {
		id := r.varint()
		value := r.bytes(r.varint())
		if r.failed {
			return Parameters{}, parameterError("transport parameters are truncated")
		}
		if name := prohibitedParam(id); name != "" {
			return Parameters{}, parameterError(name + " is not allowed")
		}
		if _, dup := seen[id]; dup {
			return Parameters{}, parameterError(fmt.Sprintf("transport parameter 0x%x is repeated", id))
		}
		seen[id] = struct{}{}

		dst := p.field(id)
		if dst == nil {
			continue
		}
		v, n, err := quicvarint.Parse(value)
		if err != nil || n != len(value) {
			return Parameters{}, parameterError(fmt.Sprintf("transport parameter 0x%x is not an integer", id))
		}
		*dst = v
	}
	if p.InitialMaxStreamsBidi > maxStreams || p.InitialMaxStreamsUni > maxStreams {
		return Parameters{}, parameterError("initial_max_streams above 2^60")
	}
	if p.MaxRecordSize < DefaultMaxRecordSize {
		return Parameters{}, parameterError("max_record_size below 16382")
	}
	return p, nil
}
