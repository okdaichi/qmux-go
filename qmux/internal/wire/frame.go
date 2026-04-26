package wire

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/quicvarint"
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
	FrameTypeDatagram           FrameType = 0x30 // 0x30-0x31

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
	Recycle()
}

// ParseFrame parses a single frame.
func ParseFrame(r io.Reader) (Frame, error) {
	qr := quicvarint.NewReader(r)
	t, err := quicvarint.Read(qr)
	if err != nil {
		return nil, err
	}
	ft := FrameType(t)

	switch {
	case ft == FrameTypePadding:
		return &PaddingFrame{}, nil
	case ft == FrameTypePing:
		return &StandardPingFrame{}, nil
	case ft == FrameTypeResetStream:
		return parseResetStreamFrame(qr)
	case ft == FrameTypeStopSending:
		return parseStopSendingFrame(qr)
	case ft == FrameTypeCrypto || ft == FrameTypeNewToken || ft == FrameTypeNewConnectionID ||
		ft == FrameTypeRetireConnectionID || ft == FrameTypePathChallenge || ft == FrameTypePathResponse ||
		ft == FrameTypeHandshakeDone:
		return nil, &quic.TransportError{ErrorCode: quic.ProtocolViolation, ErrorMessage: fmt.Sprintf("prohibited frame type: 0x%x", t)}
	case ft >= FrameTypeStream && ft <= maxStreamFrameType:
		return parseStreamFrame(qr, ft)
	case ft == FrameTypeMaxData:
		return parseMaxDataFrame(qr)
	case ft == FrameTypeMaxStreamData:
		return parseMaxStreamDataFrame(qr)
	case ft == FrameTypeMaxStreamsBi || ft == FrameTypeMaxStreamsUni:
		return parseMaxStreamsFrame(qr, ft)
	case ft == FrameTypeDataBlocked:
		return parseDataBlockedFrame(qr)
	case ft == FrameTypeStreamDataBlocked:
		return parseStreamDataBlockedFrame(qr)
	case ft == FrameTypeStreamsBlockedBi || ft == FrameTypeStreamsBlockedUni:
		return parseStreamsBlockedFrame(qr, ft)
	case ft == FrameTypeConnectionClose || ft == FrameTypeApplicationClose:
		return parseConnectionCloseFrame(qr, ft)
	case ft == FrameTypeDatagram || ft == FrameTypeDatagram+1:
		return parseDatagramFrame(qr, ft)
	case ft == FrameTypeTransportParameters:
		return parseTransportParametersFrame(qr)
	case ft == FrameTypePingRequest || ft == FrameTypePingResponse:
		return parsePingFrame(qr, ft)
	default:
		return nil, &quic.TransportError{ErrorCode: quic.ProtocolViolation, ErrorMessage: fmt.Sprintf("unknown frame type: 0x%x", t)}
	}
}

func writeVarInt(w io.Writer, i uint64) error {
	if buf, ok := w.(*bytes.Buffer); ok {
		b := buf.AvailableBuffer()
		b = quicvarint.Append(b, i)
		_, err := buf.Write(b)
		return err
	}
	var b [8]byte
	s := quicvarint.Append(b[:0], i)
	_, err := w.Write(s)
	return err
}

// PaddingFrame is a PADDING frame.
type PaddingFrame struct{}

func (f *PaddingFrame) Type() FrameType { return FrameTypePadding }
func (f *PaddingFrame) Write(w io.Writer) error { return writeVarInt(w, uint64(f.Type())) }
func (f *PaddingFrame) Length() uint64 { return 1 }
func (f *PaddingFrame) Recycle() {}

// StandardPingFrame is a standard QUIC PING frame.
type StandardPingFrame struct{}

func (f *StandardPingFrame) Type() FrameType { return FrameTypePing }
func (f *StandardPingFrame) Write(w io.Writer) error { return writeVarInt(w, uint64(f.Type())) }
func (f *StandardPingFrame) Length() uint64 { return 1 }
func (f *StandardPingFrame) Recycle() {}

// ResetStreamFrame is a RESET_STREAM frame.
type ResetStreamFrame struct {
	StreamID  uint64
	ErrorCode uint64
	FinalSize uint64
}

func (f *ResetStreamFrame) Type() FrameType { return FrameTypeResetStream }
func (f *ResetStreamFrame) Write(w io.Writer) error {
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := writeVarInt(w, f.StreamID); err != nil {
		return err
	}
	if err := writeVarInt(w, f.ErrorCode); err != nil {
		return err
	}
	return writeVarInt(w, f.FinalSize)
}
func (f *ResetStreamFrame) Length() uint64 {
	return uint64(quicvarint.Len(uint64(f.Type())) + quicvarint.Len(f.StreamID) + quicvarint.Len(f.ErrorCode) + quicvarint.Len(f.FinalSize))
}
func (f *ResetStreamFrame) Recycle() {}

func parseResetStreamFrame(r quicvarint.Reader) (*ResetStreamFrame, error) {
	sid, err := quicvarint.Read(r)
	if err != nil {
		return nil, err
	}
	ec, err := quicvarint.Read(r)
	if err != nil {
		return nil, err
	}
	fs, err := quicvarint.Read(r)
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
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := writeVarInt(w, f.StreamID); err != nil {
		return err
	}
	return writeVarInt(w, f.ErrorCode)
}
func (f *StopSendingFrame) Length() uint64 {
	return uint64(quicvarint.Len(uint64(f.Type())) + quicvarint.Len(f.StreamID) + quicvarint.Len(f.ErrorCode))
}
func (f *StopSendingFrame) Recycle() {}

func parseStopSendingFrame(r quicvarint.Reader) (*StopSendingFrame, error) {
	sid, err := quicvarint.Read(r)
	if err != nil {
		return nil, err
	}
	ec, err := quicvarint.Read(r)
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
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := writeVarInt(w, f.StreamID); err != nil {
		return err
	}
	if f.Offset > 0 {
		if err := writeVarInt(w, f.Offset); err != nil {
			return err
		}
	}
	if err := writeVarInt(w, uint64(len(f.Data))); err != nil {
		return err
	}
	_, err := w.Write(f.Data)
	return err
}

func (f *StreamFrame) Length() uint64 {
	l := uint64(quicvarint.Len(uint64(f.Type())) + quicvarint.Len(f.StreamID))
	if f.Offset > 0 {
		l += uint64(quicvarint.Len(f.Offset))
	}
	l += uint64(quicvarint.Len(uint64(len(f.Data))))
	l += uint64(len(f.Data))
	return l
}

var streamFramePool = sync.Pool{
	New: func() any {
		return &StreamFrame{}
	},
}

var bytePool = sync.Pool{
	New: func() any {
		// Default size for records is 16KB
		b := make([]byte, 16384)
		return &b
	},
}

// GetStreamFrame gets a StreamFrame from the pool.
func GetStreamFrame() *StreamFrame {
	return streamFramePool.Get().(*StreamFrame)
}

// Recycle puts the frame back into the pool if it's a StreamFrame.
func (f *StreamFrame) Recycle() {
	if f.Data != nil {
		// Only recycle if it's exactly our pooled size
		if cap(f.Data) == 16384 {
			b := f.Data[:16384]
			bytePool.Put(&b)
		}
		f.Data = nil
	}
	streamFramePool.Put(f)
}

func parseStreamFrame(r quicvarint.Reader, ft FrameType) (*StreamFrame, error) {
	sid, err := quicvarint.Read(r)
	if err != nil {
		return nil, err
	}
	var offset uint64
	if ft&0x04 != 0 {
		offset, err = quicvarint.Read(r)
		if err != nil {
			return nil, err
		}
	}
	var length uint64
	if ft&0x02 != 0 {
		length, err = quicvarint.Read(r)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, &quic.TransportError{ErrorCode: quic.ProtocolViolation, ErrorMessage: "STREAM frame without LEN bit not yet supported"}
	}

	f := GetStreamFrame()
	f.StreamID = sid
	f.Offset = offset
	f.Fin = ft&0x01 != 0
	
	if length <= 16384 {
		pb := bytePool.Get().(*[]byte)
		f.Data = (*pb)[:length]
	} else {
		f.Data = make([]byte, length)
	}

	if _, err := io.ReadFull(r, f.Data); err != nil {
		f.Recycle()
		return nil, err
	}

	return f, nil
}

// MaxDataFrame is a MAX_DATA frame.
type MaxDataFrame struct {
	MaximumData uint64
}

func (f *MaxDataFrame) Type() FrameType { return FrameTypeMaxData }
func (f *MaxDataFrame) Write(w io.Writer) error {
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	return writeVarInt(w, f.MaximumData)
}
func (f *MaxDataFrame) Length() uint64 {
	return uint64(quicvarint.Len(uint64(f.Type())) + quicvarint.Len(f.MaximumData))
}
func (f *MaxDataFrame) Recycle() {}

func parseMaxDataFrame(r quicvarint.Reader) (*MaxDataFrame, error) {
	md, err := quicvarint.Read(r)
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
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := writeVarInt(w, f.StreamID); err != nil {
		return err
	}
	return writeVarInt(w, f.MaximumStreamData)
}
func (f *MaxStreamDataFrame) Length() uint64 {
	return uint64(quicvarint.Len(uint64(f.Type())) + quicvarint.Len(f.StreamID) + quicvarint.Len(f.MaximumStreamData))
}
func (f *MaxStreamDataFrame) Recycle() {}

func parseMaxStreamDataFrame(r quicvarint.Reader) (*MaxStreamDataFrame, error) {
	sid, err := quicvarint.Read(r)
	if err != nil {
		return nil, err
	}
	msd, err := quicvarint.Read(r)
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
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	return writeVarInt(w, f.MaximumStreams)
}
func (f *MaxStreamsFrame) Length() uint64 {
	return uint64(quicvarint.Len(uint64(f.Type())) + quicvarint.Len(f.MaximumStreams))
}
func (f *MaxStreamsFrame) Recycle() {}

func parseMaxStreamsFrame(r quicvarint.Reader, ft FrameType) (*MaxStreamsFrame, error) {
	ms, err := quicvarint.Read(r)
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
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	return writeVarInt(w, f.MaximumData)
}
func (f *DataBlockedFrame) Length() uint64 {
	return uint64(quicvarint.Len(uint64(f.Type())) + quicvarint.Len(f.MaximumData))
}
func (f *DataBlockedFrame) Recycle() {}

func parseDataBlockedFrame(r quicvarint.Reader) (*DataBlockedFrame, error) {
	md, err := quicvarint.Read(r)
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
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := writeVarInt(w, f.StreamID); err != nil {
		return err
	}
	return writeVarInt(w, f.MaximumStreamData)
}
func (f *StreamDataBlockedFrame) Length() uint64 {
	return uint64(quicvarint.Len(uint64(f.Type())) + quicvarint.Len(f.StreamID) + quicvarint.Len(f.MaximumStreamData))
}
func (f *StreamDataBlockedFrame) Recycle() {}

func parseStreamDataBlockedFrame(r quicvarint.Reader) (*StreamDataBlockedFrame, error) {
	sid, err := quicvarint.Read(r)
	if err != nil {
		return nil, err
	}
	msd, err := quicvarint.Read(r)
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
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	return writeVarInt(w, f.MaximumStreams)
}
func (f *StreamsBlockedFrame) Length() uint64 {
	return uint64(quicvarint.Len(uint64(f.Type())) + quicvarint.Len(f.MaximumStreams))
}
func (f *StreamsBlockedFrame) Recycle() {}

func parseStreamsBlockedFrame(r quicvarint.Reader, ft FrameType) (*StreamsBlockedFrame, error) {
	ms, err := quicvarint.Read(r)
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
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := writeVarInt(w, f.ErrorCode); err != nil {
		return err
	}
	if f.TypeField == FrameTypeConnectionClose {
		if err := writeVarInt(w, f.FrameType); err != nil {
			return err
		}
	}
	if err := writeVarInt(w, uint64(len(f.ReasonPhrase))); err != nil {
		return err
	}
	_, err := w.Write([]byte(f.ReasonPhrase))
	return err
}
func (f *ConnectionCloseFrame) Length() uint64 {
	l := uint64(quicvarint.Len(uint64(f.Type())) + quicvarint.Len(f.ErrorCode))
	if f.TypeField == FrameTypeConnectionClose {
		l += uint64(quicvarint.Len(f.FrameType))
	}
	l += uint64(quicvarint.Len(uint64(len(f.ReasonPhrase))))
	l += uint64(len(f.ReasonPhrase))
	return l
}
func (f *ConnectionCloseFrame) Recycle() {}

func parseConnectionCloseFrame(r quicvarint.Reader, ft FrameType) (*ConnectionCloseFrame, error) {
	ec, err := quicvarint.Read(r)
	if err != nil {
		return nil, err
	}
	var frameType uint64
	if ft == FrameTypeConnectionClose {
		frameType, err = quicvarint.Read(r)
		if err != nil {
			return nil, err
		}
	}
	reasonLen, err := quicvarint.Read(r)
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
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	return WriteTransportParameters(w, f.Parameters)
}
func (f *TransportParametersFrame) Length() uint64 {
	l := uint64(8) // QX_TRANSPORT_PARAMETERS type is 8 bytes
	for _, p := range f.Parameters {
		l += uint64(quicvarint.Len(p.ID))
		l += uint64(quicvarint.Len(uint64(len(p.Value))))
		l += uint64(len(p.Value))
	}
	return l
}
func (f *TransportParametersFrame) Recycle() {}

func parseTransportParametersFrame(r quicvarint.Reader) (*TransportParametersFrame, error) {
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
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
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
func (f *PingFrame) Recycle() {}

func parsePingFrame(r quicvarint.Reader, ft FrameType) (*PingFrame, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return nil, err
	}
	return &PingFrame{
		TypeField: ft,
		Sequence:  binary.BigEndian.Uint64(b[:]),
	}, nil
}

// DatagramFrame is a DATAGRAM frame.
type DatagramFrame struct {
	Data []byte
}

func (f *DatagramFrame) Type() FrameType { return FrameTypeDatagram }
func (f *DatagramFrame) Write(w io.Writer) error {
	if err := writeVarInt(w, uint64(f.Type())); err != nil {
		return err
	}
	if err := writeVarInt(w, uint64(len(f.Data))); err != nil {
		return err
	}
	_, err := w.Write(f.Data)
	return err
}
func (f *DatagramFrame) Length() uint64 {
	return uint64(quicvarint.Len(uint64(f.Type())) + quicvarint.Len(uint64(len(f.Data))) + len(f.Data))
}
func (f *DatagramFrame) Recycle() {}

func parseDatagramFrame(r quicvarint.Reader, ft FrameType) (*DatagramFrame, error) {
	length, err := quicvarint.Read(r)
	if err != nil {
		return nil, err
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return &DatagramFrame{Data: data}, nil
}
