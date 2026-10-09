// Package wire encodes and decodes the frames and transport parameters of
// QMux (draft-ietf-quic-qmux-02).
package wire

import (
	"fmt"

	"github.com/quic-go/quic-go/quicvarint"
)

// Frame types that QMux carries, with their QUIC version 1 values.
const (
	typeResetStream       = 0x04
	typeStopSending       = 0x05
	typeStream            = 0x08 // 0x08-0x0f
	typeStreamMax         = 0x0f
	typeMaxData           = 0x10
	typeMaxStreamData     = 0x11
	typeMaxStreamsBidi    = 0x12
	typeMaxStreamsUni     = 0x13
	typeDataBlocked       = 0x14
	typeStreamDataBlocked = 0x15
	typeStreamsBlocked    = 0x16
	typeStreamsBlockedUni = 0x17
	typeConnectionClose   = 0x1c
	typeApplicationClose  = 0x1d
	typeDatagram          = 0x30
	typeDatagramLen       = 0x31

	typeTransportParameters = 0x3f5153300d0a0d0a
	typePingRequest         = 0x348c67529ef8c7bd
	typePingResponse        = 0x348c67529ef8c7be

	streamBitFin = 0x01
	streamBitLen = 0x02
	streamBitOff = 0x04
)

// Transport error codes of QUIC version 1 that QMux uses.
const (
	InternalError           uint64 = 0x01
	FlowControlError        uint64 = 0x03
	StreamLimitError        uint64 = 0x04
	StreamStateError        uint64 = 0x05
	FinalSizeError          uint64 = 0x06
	FrameEncodingError      uint64 = 0x07
	TransportParameterError uint64 = 0x08
	ProtocolViolation       uint64 = 0x0a
)

// maxStreams is the largest stream count a peer may declare (RFC 9000,
// Section 4.6).
const maxStreams = 1 << 60

// StreamOverhead is the largest encoding of a STREAM frame without its data.
const StreamOverhead = 1 + 8 + 8 + 8

// Error is a connection error: what the endpoint that detects it puts in its
// CONNECTION_CLOSE frame.
type Error struct {
	Code   uint64
	Reason string
}

// Error describes the error with its code.
func (e *Error) Error() string {
	return fmt.Sprintf("qmux: transport error 0x%x: %s", e.Code, e.Reason)
}

func frameEncodingError(reason string) *Error {
	return &Error{Code: FrameEncodingError, Reason: reason}
}

// Frame is a frame that an endpoint can send.
type Frame interface {
	// Append appends the encoded frame to b.
	Append(b []byte) []byte
}

var (
	_ Frame = (*ResetStream)(nil)
	_ Frame = (*StopSending)(nil)
	_ Frame = (*Stream)(nil)
	_ Frame = (*MaxData)(nil)
	_ Frame = (*MaxStreamData)(nil)
	_ Frame = (*MaxStreams)(nil)
	_ Frame = (*ConnectionClose)(nil)
	_ Frame = (*Datagram)(nil)
	_ Frame = (*Ping)(nil)
	_ Frame = (*TransportParameters)(nil)
)

// ResetStream is a RESET_STREAM frame.
type ResetStream struct {
	StreamID  uint64
	Code      uint64
	FinalSize uint64
}

// Append appends the encoded frame to b.
func (f *ResetStream) Append(b []byte) []byte {
	b = append(b, typeResetStream)
	b = quicvarint.Append(b, f.StreamID)
	b = quicvarint.Append(b, f.Code)
	return quicvarint.Append(b, f.FinalSize)
}

// StopSending is a STOP_SENDING frame.
type StopSending struct {
	StreamID uint64
	Code     uint64
}

// Append appends the encoded frame to b.
func (f *StopSending) Append(b []byte) []byte {
	b = append(b, typeStopSending)
	b = quicvarint.Append(b, f.StreamID)
	return quicvarint.Append(b, f.Code)
}

// Stream is a STREAM frame. Data of a parsed frame aliases the record it was
// parsed from.
type Stream struct {
	StreamID uint64
	Offset   uint64
	Data     []byte
	Fin      bool
}

// Append appends the encoded frame to b. The frame always carries its
// length, so that other frames can follow it in a record.
func (f *Stream) Append(b []byte) []byte {
	t := byte(typeStream | streamBitLen)
	if f.Offset > 0 {
		t |= streamBitOff
	}
	if f.Fin {
		t |= streamBitFin
	}
	b = append(b, t)
	b = quicvarint.Append(b, f.StreamID)
	if f.Offset > 0 {
		b = quicvarint.Append(b, f.Offset)
	}
	b = quicvarint.Append(b, uint64(len(f.Data)))
	return append(b, f.Data...)
}

// MaxData is a MAX_DATA frame.
type MaxData struct {
	Max uint64
}

// Append appends the encoded frame to b.
func (f *MaxData) Append(b []byte) []byte {
	return quicvarint.Append(append(b, typeMaxData), f.Max)
}

// MaxStreamData is a MAX_STREAM_DATA frame.
type MaxStreamData struct {
	StreamID uint64
	Max      uint64
}

// Append appends the encoded frame to b.
func (f *MaxStreamData) Append(b []byte) []byte {
	b = append(b, typeMaxStreamData)
	b = quicvarint.Append(b, f.StreamID)
	return quicvarint.Append(b, f.Max)
}

// MaxStreams is a MAX_STREAMS frame, for bidirectional or unidirectional
// streams.
type MaxStreams struct {
	Uni bool
	Max uint64
}

// Append appends the encoded frame to b.
func (f *MaxStreams) Append(b []byte) []byte {
	t := byte(typeMaxStreamsBidi)
	if f.Uni {
		t = typeMaxStreamsUni
	}
	return quicvarint.Append(append(b, t), f.Max)
}

// Blocked is a DATA_BLOCKED, STREAM_DATA_BLOCKED or STREAMS_BLOCKED frame.
// It is informational, so only its presence is reported.
type Blocked struct{}

// ConnectionClose is a CONNECTION_CLOSE frame, of the transport (0x1c) or
// the application (0x1d) kind.
type ConnectionClose struct {
	Application bool
	Code        uint64
	FrameType   uint64 // transport kind only
	Reason      string
}

// Append appends the encoded frame to b.
func (f *ConnectionClose) Append(b []byte) []byte {
	if f.Application {
		b = append(b, typeApplicationClose)
		b = quicvarint.Append(b, f.Code)
	} else {
		b = append(b, typeConnectionClose)
		b = quicvarint.Append(b, f.Code)
		b = quicvarint.Append(b, f.FrameType)
	}
	b = quicvarint.Append(b, uint64(len(f.Reason)))
	return append(b, f.Reason...)
}

// Datagram is a DATAGRAM frame. Data of a parsed frame aliases the record it
// was parsed from.
type Datagram struct {
	Data []byte
}

// Append appends the encoded frame to b, with its length.
func (f *Datagram) Append(b []byte) []byte {
	b = append(b, typeDatagramLen)
	b = quicvarint.Append(b, uint64(len(f.Data)))
	return append(b, f.Data...)
}

// Len returns the size of the encoded frame.
func (f *Datagram) Len() int {
	return 1 + quicvarint.Len(uint64(len(f.Data))) + len(f.Data)
}

// Ping is a QX_PING frame: a request, or the response that echoes its
// sequence number.
type Ping struct {
	Response bool
	Sequence uint64
}

// Append appends the encoded frame to b.
func (f *Ping) Append(b []byte) []byte {
	t := uint64(typePingRequest)
	if f.Response {
		t = typePingResponse
	}
	return quicvarint.Append(quicvarint.Append(b, t), f.Sequence)
}

// TransportParameters is a QX_TRANSPORT_PARAMETERS frame.
type TransportParameters struct {
	Parameters
}

// Append appends the encoded frame to b.
func (f *TransportParameters) Append(b []byte) []byte {
	params := f.append(nil)
	b = quicvarint.Append(b, typeTransportParameters)
	b = quicvarint.Append(b, uint64(len(params)))
	return append(b, params...)
}

// reader consumes a record. Running out of bytes sets failed; every later
// read then returns zero, so that a parser checks once at its end.
type reader struct {
	b      []byte
	failed bool
}

func (r *reader) varint() uint64 {
	if r.failed {
		return 0
	}
	v, n, err := quicvarint.Parse(r.b)
	if err != nil {
		r.failed = true
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *reader) bytes(n uint64) []byte {
	if r.failed || n > uint64(len(r.b)) {
		r.failed = true
		return nil
	}
	p := r.b[:n]
	r.b = r.b[n:]
	return p
}

func (r *reader) rest() []byte {
	if r.failed {
		return nil
	}
	p := r.b
	r.b = nil
	return p
}

// Parse parses the first frame of b, the remainder of a record's Frames
// field, and returns the frame with what follows it. PADDING is skipped: a
// nil frame with a nil error means that only padding was left. A frame that
// is truncated, prohibited or unknown is an error of type
// FRAME_ENCODING_ERROR.
func Parse(b []byte) (any, []byte, error) {
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	if len(b) == 0 {
		return nil, nil, nil
	}
	r := &reader{b: b}
	t := r.varint()
	var f any
	switch {
	case t == typeResetStream:
		f = &ResetStream{StreamID: r.varint(), Code: r.varint(), FinalSize: r.varint()}
	case t == typeStopSending:
		f = &StopSending{StreamID: r.varint(), Code: r.varint()}
	case t >= typeStream && t <= typeStreamMax:
		s := &Stream{StreamID: r.varint(), Fin: t&streamBitFin != 0}
		if t&streamBitOff != 0 {
			s.Offset = r.varint()
		}
		if t&streamBitLen != 0 {
			s.Data = r.bytes(r.varint())
		} else {
			s.Data = r.rest()
		}
		f = s
	case t == typeMaxData:
		f = &MaxData{Max: r.varint()}
	case t == typeMaxStreamData:
		f = &MaxStreamData{StreamID: r.varint(), Max: r.varint()}
	case t == typeMaxStreamsBidi || t == typeMaxStreamsUni:
		m := &MaxStreams{Uni: t == typeMaxStreamsUni, Max: r.varint()}
		if m.Max > maxStreams {
			return nil, nil, frameEncodingError("MAX_STREAMS above 2^60")
		}
		f = m
	case t == typeDataBlocked:
		r.varint()
		f = &Blocked{}
	case t == typeStreamDataBlocked:
		r.varint()
		r.varint()
		f = &Blocked{}
	case t == typeStreamsBlocked || t == typeStreamsBlockedUni:
		if r.varint() > maxStreams {
			return nil, nil, frameEncodingError("STREAMS_BLOCKED above 2^60")
		}
		f = &Blocked{}
	case t == typeConnectionClose || t == typeApplicationClose:
		c := &ConnectionClose{Application: t == typeApplicationClose, Code: r.varint()}
		if !c.Application {
			c.FrameType = r.varint()
		}
		c.Reason = string(r.bytes(r.varint()))
		f = c
	case t == typeDatagram:
		f = &Datagram{Data: r.rest()}
	case t == typeDatagramLen:
		f = &Datagram{Data: r.bytes(r.varint())}
	case t == typeTransportParameters:
		params, err := parseParameters(r.bytes(r.varint()))
		if err != nil && !r.failed {
			return nil, nil, err
		}
		f = &TransportParameters{Parameters: params}
	case t == typePingRequest || t == typePingResponse:
		f = &Ping{Response: t == typePingResponse, Sequence: r.varint()}
	default:
		// Every other frame of QUIC version 1 is prohibited, and no
		// extension frame is negotiated.
		return nil, nil, frameEncodingError(fmt.Sprintf("frame type 0x%x is not allowed", t))
	}
	if r.failed {
		return nil, nil, frameEncodingError(fmt.Sprintf("frame of type 0x%x is truncated", t))
	}
	return f, r.b, nil
}
