package wire

import (
	"encoding/binary"
	"fmt"
	"io"
)

// FrameType is a QUIC frame type.
type FrameType uint64

const (
	FrameTypeResetStream        FrameType = 0x04
	FrameTypeStopSending        FrameType = 0x05
	FrameTypeCrypto             FrameType = 0x06 // Prohibited in QMux
	FrameTypeNewToken           FrameType = 0x07 // Prohibited in QMux
	FrameTypeStream             FrameType = 0x08 // 0x08-0x0f
	FrameTypeMaxData            FrameType = 0x10
	FrameTypeMaxStreamData      FrameType = 0x11
	FrameTypeMaxStreamsBi       FrameType = 0x12
	FrameTypeMaxStreamsUni      FrameType = 0x13
	FrameTypeStreamsBlockedBi   FrameType = 0x16
	FrameTypeStreamsBlockedUni  FrameType = 0x17
	FrameTypeNewConnectionID    FrameType = 0x18 // Prohibited in QMux
	FrameTypeRetireConnectionID FrameType = 0x19 // Prohibited in QMux
	FrameTypePathChallenge      FrameType = 0x1a // Prohibited in QMux
	FrameTypePathResponse       FrameType = 0x1b // Prohibited in QMux
	FrameTypeConnectionClose    FrameType = 0x1c // Transport layer
	FrameTypeApplicationClose   FrameType = 0x1d // Application layer
	FrameTypeHandshakeDone      FrameType = 0x1e // Prohibited in QMux

	maxStreamFrameType FrameType = 0x0f

	// QMux specific frames
	FrameTypeTransportParameters FrameType = 0x3f5153300d0a0d0a
	FrameTypePingRequest          FrameType = 0x348c67529ef8c7bd
	FrameTypePingResponse         FrameType = 0x348c67529ef8c7be
)

// Frame is the interface for all frames.
type Frame interface {
	Type() FrameType
	Write(io.Writer) error
	Length() uint64
}

// ParseFrame parses a single frame.
func ParseFrame(r io.Reader) (Frame, error) {
	t, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	ft := FrameType(t)

	switch {
	case ft == FrameTypeResetStream:
		return parseResetStreamFrame(r)
	case ft == FrameTypeStopSending:
		return parseStopSendingFrame(r)
	case ft == FrameTypeCrypto || ft == FrameTypeNewToken || ft == FrameTypeNewConnectionID ||
		ft == FrameTypeRetireConnectionID || ft == FrameTypePathChallenge || ft == FrameTypePathResponse ||
		ft == FrameTypeHandshakeDone:
		return nil, fmt.Errorf("prohibited frame type: 0x%x", t)
	case ft >= FrameTypeStream && ft <= maxStreamFrameType:
		return parseStreamFrame(r, ft)
	case ft == FrameTypeMaxData:
		return parseMaxDataFrame(r)
	case ft == FrameTypeMaxStreamData:
		return parseMaxStreamDataFrame(r)
	case ft == FrameTypeMaxStreamsBi || ft == FrameTypeMaxStreamsUni:
		return parseMaxStreamsFrame(r, ft)
	case ft == FrameTypeStreamsBlockedBi || ft == FrameTypeStreamsBlockedUni:
		return parseStreamsBlockedFrame(r, ft)
	case ft == FrameTypeConnectionClose || ft == FrameTypeApplicationClose:
		return parseConnectionCloseFrame(r, ft)
	case ft == FrameTypeTransportParameters:
		return parseTransportParametersFrame(r)
	case ft == FrameTypePingRequest || ft == FrameTypePingResponse:
		return parsePingFrame(r, ft)
	default:
		return nil, fmt.Errorf("unknown frame type: 0x%x", t)
	}
}

// ResetStreamFrame is a RESET_STREAM frame.
type ResetStreamFrame struct {
	StreamID  uint64
	ErrorCode uint64
	FinalSize uint64
}

func (f *ResetStreamFrame) Type() FrameType { return FrameTypeResetStream }
func (f *ResetStreamFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := WriteVarInt(w, f.StreamID); err != nil {
		return err
	}
	if err := WriteVarInt(w, f.ErrorCode); err != nil {
		return err
	}
	return WriteVarInt(w, f.FinalSize)
}
func (f *ResetStreamFrame) Length() uint64 {
	return uint64(VarIntLen(uint64(f.Type())) + VarIntLen(f.StreamID) + VarIntLen(f.ErrorCode) + VarIntLen(f.FinalSize))
}

func parseResetStreamFrame(r io.Reader) (*ResetStreamFrame, error) {
	sid, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	ec, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	fs, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	return &ResetStreamFrame{StreamID: sid, ErrorCode: ec, FinalSize: fs}, nil
}

// StopSendingFrame is a STOP_SENDING frame.
type StopSendingFrame struct {
	StreamID  uint64
	ErrorCode uint64
}

func (f *StopSendingFrame) Type() FrameType { return FrameTypeStopSending }
func (f *StopSendingFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := WriteVarInt(w, f.StreamID); err != nil {
		return err
	}
	return WriteVarInt(w, f.ErrorCode)
}
func (f *StopSendingFrame) Length() uint64 {
	return uint64(VarIntLen(uint64(f.Type())) + VarIntLen(f.StreamID) + VarIntLen(f.ErrorCode))
}

func parseStopSendingFrame(r io.Reader) (*StopSendingFrame, error) {
	sid, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	ec, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	return &StopSendingFrame{StreamID: sid, ErrorCode: ec}, nil
}

const (
	streamTypeBitOffset = 0x04
	streamTypeBitLength = 0x02
	streamTypeBitFin    = 0x01

	streamTypeMask = 0x08
)

// StreamFrame is a STREAM frame.
type StreamFrame struct {
	StreamID uint64
	Offset   uint64
	Data     []byte
	Fin      bool
}

func (f *StreamFrame) Type() FrameType {
	t := FrameTypeStream
	if f.Offset > 0 {
		t |= streamTypeBitOffset
	}
	t |= streamTypeBitLength // We always include length for simplicity in encoding
	if f.Fin {
		t |= streamTypeBitFin
	}
	return t
}

func (f *StreamFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := WriteVarInt(w, f.StreamID); err != nil {
		return err
	}
	if f.Offset > 0 {
		if err := WriteVarInt(w, f.Offset); err != nil {
			return err
		}
	}
	if err := WriteVarInt(w, uint64(len(f.Data))); err != nil {
		return err
	}
	_, err := w.Write(f.Data)
	return err
}

func (f *StreamFrame) Length() uint64 {
	l := uint64(VarIntLen(uint64(f.Type())) + VarIntLen(f.StreamID))
	if f.Offset > 0 {
		l += uint64(VarIntLen(f.Offset))
	}
	l += uint64(VarIntLen(uint64(len(f.Data))))
	l += uint64(len(f.Data))
	return l
}

func parseStreamFrame(r io.Reader, ft FrameType) (*StreamFrame, error) {
	sid, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	var offset uint64
	if ft&0x04 != 0 {
		offset, err = ReadVarInt(r)
		if err != nil {
			return nil, err
		}
	}
	var length uint64
	if ft&0x02 != 0 {
		length, err = ReadVarInt(r)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("STREAM frame without LEN bit not yet supported")
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	return &StreamFrame{
		StreamID: sid,
		Offset:   offset,
		Data:     data,
		Fin:      ft&0x01 != 0,
	}, nil
}

// MaxDataFrame is a MAX_DATA frame.
type MaxDataFrame struct {
	MaximumData uint64
}

func (f *MaxDataFrame) Type() FrameType { return FrameTypeMaxData }
func (f *MaxDataFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	return WriteVarInt(w, f.MaximumData)
}
func (f *MaxDataFrame) Length() uint64 {
	return uint64(VarIntLen(uint64(f.Type())) + VarIntLen(f.MaximumData))
}

func parseMaxDataFrame(r io.Reader) (*MaxDataFrame, error) {
	md, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	return &MaxDataFrame{MaximumData: md}, nil
}

// MaxStreamDataFrame is a MAX_STREAM_DATA frame.
type MaxStreamDataFrame struct {
	StreamID          uint64
	MaximumStreamData uint64
}

func (f *MaxStreamDataFrame) Type() FrameType { return FrameTypeMaxStreamData }
func (f *MaxStreamDataFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := WriteVarInt(w, f.StreamID); err != nil {
		return err
	}
	return WriteVarInt(w, f.MaximumStreamData)
}
func (f *MaxStreamDataFrame) Length() uint64 {
	return uint64(VarIntLen(uint64(f.Type())) + VarIntLen(f.StreamID) + VarIntLen(f.MaximumStreamData))
}

func parseMaxStreamDataFrame(r io.Reader) (*MaxStreamDataFrame, error) {
	sid, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	msd, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	return &MaxStreamDataFrame{StreamID: sid, MaximumStreamData: msd}, nil
}

// MaxStreamsFrame is a MAX_STREAMS frame.
type MaxStreamsFrame struct {
	TypeField      FrameType
	MaximumStreams uint64
}

func (f *MaxStreamsFrame) Type() FrameType { return f.TypeField }
func (f *MaxStreamsFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	return WriteVarInt(w, f.MaximumStreams)
}
func (f *MaxStreamsFrame) Length() uint64 {
	return uint64(VarIntLen(uint64(f.Type())) + VarIntLen(f.MaximumStreams))
}

func parseMaxStreamsFrame(r io.Reader, ft FrameType) (*MaxStreamsFrame, error) {
	ms, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	return &MaxStreamsFrame{TypeField: ft, MaximumStreams: ms}, nil
}

// StreamsBlockedFrame is a STREAMS_BLOCKED frame.
type StreamsBlockedFrame struct {
	TypeField      FrameType
	MaximumStreams uint64
}

func (f *StreamsBlockedFrame) Type() FrameType { return f.TypeField }
func (f *StreamsBlockedFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	return WriteVarInt(w, f.MaximumStreams)
}
func (f *StreamsBlockedFrame) Length() uint64 {
	return uint64(VarIntLen(uint64(f.Type())) + VarIntLen(f.MaximumStreams))
}

func parseStreamsBlockedFrame(r io.Reader, ft FrameType) (*StreamsBlockedFrame, error) {
	ms, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	return &StreamsBlockedFrame{TypeField: ft, MaximumStreams: ms}, nil
}

// ConnectionCloseFrame is a CONNECTION_CLOSE frame.
type ConnectionCloseFrame struct {
	TypeField    FrameType
	ErrorCode    uint64
	FrameType    uint64
	ReasonPhrase string
}

func (f *ConnectionCloseFrame) Type() FrameType { return f.TypeField }
func (f *ConnectionCloseFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := WriteVarInt(w, f.ErrorCode); err != nil {
		return err
	}
	if f.TypeField == FrameTypeConnectionClose {
		if err := WriteVarInt(w, f.FrameType); err != nil {
			return err
		}
	}
	if err := WriteVarInt(w, uint64(len(f.ReasonPhrase))); err != nil {
		return err
	}
	_, err := w.Write([]byte(f.ReasonPhrase))
	return err
}
func (f *ConnectionCloseFrame) Length() uint64 {
	l := uint64(VarIntLen(uint64(f.Type())) + VarIntLen(f.ErrorCode))
	if f.TypeField == FrameTypeConnectionClose {
		l += uint64(VarIntLen(f.FrameType))
	}
	l += uint64(VarIntLen(uint64(len(f.ReasonPhrase))))
	l += uint64(len(f.ReasonPhrase))
	return l
}

func parseConnectionCloseFrame(r io.Reader, ft FrameType) (*ConnectionCloseFrame, error) {
	ec, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	var frameType uint64
	if ft == FrameTypeConnectionClose {
		frameType, err = ReadVarInt(r)
		if err != nil {
			return nil, err
		}
	}
	reasonLen, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	reason := make([]byte, reasonLen)
	if _, err := io.ReadFull(r, reason); err != nil {
		return nil, err
	}
	return &ConnectionCloseFrame{
		TypeField:    ft,
		ErrorCode:    ec,
		FrameType:    frameType,
		ReasonPhrase: string(reason),
	}, nil
}

// TransportParametersFrame is a QX_TRANSPORT_PARAMETERS frame.
type TransportParametersFrame struct {
	Parameters []TransportParameter
}

func (f *TransportParametersFrame) Type() FrameType { return FrameTypeTransportParameters }
func (f *TransportParametersFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	return WriteTransportParameters(w, f.Parameters)
}
func (f *TransportParametersFrame) Length() uint64 {
	l := uint64(8) // QX_TRANSPORT_PARAMETERS type is 8 bytes
	for _, p := range f.Parameters {
		l += uint64(VarIntLen(p.ID))
		l += uint64(VarIntLen(uint64(len(p.Value))))
		l += uint64(len(p.Value))
	}
	return l
}

func parseTransportParametersFrame(r io.Reader) (*TransportParametersFrame, error) {
	params, err := ParseTransportParameters(r)
	if err != nil {
		return nil, err
	}
	return &TransportParametersFrame{Parameters: params}, nil
}

// PingFrame is a QX_PING frame.
type PingFrame struct {
	TypeField FrameType
	Sequence  uint64
}

func (f *PingFrame) Type() FrameType { return f.TypeField }
func (f *PingFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], f.Sequence)
	_, err := w.Write(b[:])
	return err
}
func (f *PingFrame) Length() uint64 {
	return 8 + 8
}

func parsePingFrame(r io.Reader, ft FrameType) (*PingFrame, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return nil, err
	}
	return &PingFrame{
		TypeField: ft,
		Sequence:  binary.BigEndian.Uint64(b[:]),
	}, nil
}
