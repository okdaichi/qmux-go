package wire

import (
	"encoding/binary"
	"fmt"
	"io"
)

// FrameType is a QUIC frame type.
type FrameType uint64

const (
	FrameTypePadding            FrameType = 0x00
	FrameTypePing               FrameType = 0x01
	FrameTypeResetStream        FrameType = 0x04
	FrameTypeStopSending        FrameType = 0x05
	FrameTypeCrypto             FrameType = 0x06 // Prohibited in QMux
	FrameTypeNewToken           FrameType = 0x07 // Prohibited in QMux
	FrameTypeStream             FrameType = 0x08 // 0x08-0x0f
	FrameTypeMaxData            FrameType = 0x10
	FrameTypeMaxStreamData      FrameType = 0x11
	FrameTypeMaxStreamsBi       FrameType = 0x12
	FrameTypeMaxStreamsUni      FrameType = 0x13
	FrameTypeDataBlocked        FrameType = 0x14
	FrameTypeStreamDataBlocked  FrameType = 0x15
	FrameTypeStreamsBlockedBi   FrameType = 0x16
	FrameTypeStreamsBlockedUni  FrameType = 0x17
	FrameTypeNewConnectionID    FrameType = 0x18 // Prohibited in QMux
	FrameTypeRetireConnectionID FrameType = 0x19 // Prohibited in QMux
	FrameTypePathChallenge      FrameType = 0x1a // Prohibited in QMux
	FrameTypePathResponse       FrameType = 0x1b // Prohibited in QMux
	FrameTypeConnectionClose    FrameType = 0x1c // Transport layer
	FrameTypeApplicationClose   FrameType = 0x1d // Application layer
	FrameTypeHandshakeDone      FrameType = 0x1e // Prohibited in QMux

	// QMux specific frames
	FrameTypeQXTransportParameters FrameType = 0x3f5153300d0a0d0a
	FrameTypeQXPingRequest          FrameType = 0x348c67529ef8c7bd
	FrameTypeQXPingResponse         FrameType = 0x348c67529ef8c7be
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
	case ft == FrameTypePadding:
		return &PaddingFrame{}, nil
	case ft == FrameTypePing:
		return &PingFrame{}, nil
	case ft == FrameTypeResetStream:
		return parseResetStreamFrame(r)
	case ft == FrameTypeStopSending:
		return parseStopSendingFrame(r)
	case ft == FrameTypeCrypto || ft == FrameTypeNewToken || ft == FrameTypeNewConnectionID ||
		ft == FrameTypeRetireConnectionID || ft == FrameTypePathChallenge || ft == FrameTypePathResponse ||
		ft == FrameTypeHandshakeDone:
		return nil, fmt.Errorf("prohibited frame type: 0x%x", t)
	case ft >= FrameTypeStream && ft <= 0x0f:
		return parseStreamFrame(r, ft)
	case ft == FrameTypeMaxData:
		return parseMaxDataFrame(r)
	case ft == FrameTypeMaxStreamData:
		return parseMaxStreamDataFrame(r)
	case ft == FrameTypeMaxStreamsBi || ft == FrameTypeMaxStreamsUni:
		return parseMaxStreamsFrame(r, ft)
	case ft == FrameTypeDataBlocked:
		return parseDataBlockedFrame(r)
	case ft == FrameTypeStreamDataBlocked:
		return parseStreamDataBlockedFrame(r)
	case ft == FrameTypeStreamsBlockedBi || ft == FrameTypeStreamsBlockedUni:
		return parseStreamsBlockedFrame(r, ft)
	case ft == FrameTypeConnectionClose || ft == FrameTypeApplicationClose:
		return parseConnectionCloseFrame(r, ft)
	case ft == FrameTypeQXTransportParameters:
		return parseQXTransportParametersFrame(r)
	case ft == FrameTypeQXPingRequest || ft == FrameTypeQXPingResponse:
		return parseQXPingFrame(r, ft)
	default:
		return nil, fmt.Errorf("unknown frame type: 0x%x", t)
	}
}

// PaddingFrame is a PADDING frame.
type PaddingFrame struct{}

func (f *PaddingFrame) Type() FrameType { return FrameTypePadding }
func (f *PaddingFrame) Write(w io.Writer) error { return WriteVarInt(w, uint64(f.Type())) }
func (f *PaddingFrame) Length() uint64 { return 1 }

// PingFrame is a PING frame.
type PingFrame struct{}

func (f *PingFrame) Type() FrameType { return FrameTypePing }
func (f *PingFrame) Write(w io.Writer) error { return WriteVarInt(w, uint64(f.Type())) }
func (f *PingFrame) Length() uint64 { return 1 }

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
		t |= 0x04
	}
	t |= 0x02 // We always include length for simplicity in encoding
	if f.Fin {
		t |= 0x01
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

// DataBlockedFrame is a DATA_BLOCKED frame.
type DataBlockedFrame struct {
	MaximumData uint64
}

func (f *DataBlockedFrame) Type() FrameType { return FrameTypeDataBlocked }
func (f *DataBlockedFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	return WriteVarInt(w, f.MaximumData)
}
func (f *DataBlockedFrame) Length() uint64 {
	return uint64(VarIntLen(uint64(f.Type())) + VarIntLen(f.MaximumData))
}

func parseDataBlockedFrame(r io.Reader) (*DataBlockedFrame, error) {
	md, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	return &DataBlockedFrame{MaximumData: md}, nil
}

// StreamDataBlockedFrame is a STREAM_DATA_BLOCKED frame.
type StreamDataBlockedFrame struct {
	StreamID          uint64
	MaximumStreamData uint64
}

func (f *StreamDataBlockedFrame) Type() FrameType { return FrameTypeStreamDataBlocked }
func (f *StreamDataBlockedFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := WriteVarInt(w, f.StreamID); err != nil {
		return err
	}
	return WriteVarInt(w, f.MaximumStreamData)
}
func (f *StreamDataBlockedFrame) Length() uint64 {
	return uint64(VarIntLen(uint64(f.Type())) + VarIntLen(f.StreamID) + VarIntLen(f.MaximumStreamData))
}

func parseStreamDataBlockedFrame(r io.Reader) (*StreamDataBlockedFrame, error) {
	sid, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	msd, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	return &StreamDataBlockedFrame{StreamID: sid, MaximumStreamData: msd}, nil
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

// QXTransportParametersFrame is a QX_TRANSPORT_PARAMETERS frame.
type QXTransportParametersFrame struct {
	Parameters []TransportParameter
}

func (f *QXTransportParametersFrame) Type() FrameType { return FrameTypeQXTransportParameters }
func (f *QXTransportParametersFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	return WriteTransportParameters(w, f.Parameters)
}
func (f *QXTransportParametersFrame) Length() uint64 {
	l := uint64(8) // QX_TRANSPORT_PARAMETERS type is 8 bytes
	for _, p := range f.Parameters {
		l += uint64(VarIntLen(p.ID))
		l += uint64(VarIntLen(uint64(len(p.Value))))
		l += uint64(len(p.Value))
	}
	return l
}

func parseQXTransportParametersFrame(r io.Reader) (*QXTransportParametersFrame, error) {
	params, err := ParseTransportParameters(r)
	if err != nil {
		return nil, err
	}
	return &QXTransportParametersFrame{Parameters: params}, nil
}

// QXPingFrame is a QX_PING frame.
type QXPingFrame struct {
	TypeField FrameType
	Sequence  uint64
}

func (f *QXPingFrame) Type() FrameType { return f.TypeField }
func (f *QXPingFrame) Write(w io.Writer) error {
	if err := WriteVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], f.Sequence)
	_, err := w.Write(b[:])
	return err
}
func (f *QXPingFrame) Length() uint64 {
	return 8 + 8
}

func parseQXPingFrame(r io.Reader, ft FrameType) (*QXPingFrame, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return nil, err
	}
	return &QXPingFrame{
		TypeField: ft,
		Sequence:  binary.BigEndian.Uint64(b[:]),
	}, nil
}
