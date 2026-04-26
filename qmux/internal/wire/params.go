package wire

import (
	"io"

	"github.com/quic-go/quic-go/quicvarint"
)

// TransportParameter is a QUIC transport parameter.
type TransportParameter struct {
	ID    uint64
	Value []byte
}

const (
	TransportParameterMaxIdleTimeout                 uint64 = 0x01
	TransportParameterInitialMaxData                 uint64 = 0x04
	TransportParameterInitialMaxStreamDataBidiLocal  uint64 = 0x05
	TransportParameterInitialMaxStreamDataBidiRemote uint64 = 0x06
	TransportParameterInitialMaxStreamDataUni        uint64 = 0x07
	TransportParameterInitialMaxStreamsBidi          uint64 = 0x08
	TransportParameterInitialMaxStreamsUni           uint64 = 0x09
	TransportParameterMaxRecordSize                  uint64 = 0x3f5153300d0a0d0a
	TransportParameterMaxDatagramFrameSize           uint64 = 0x20
	TransportParameterApplicationProtocol            uint64 = 0x716d7578 // Custom: "qmux" in hex
)

// ParseTransportParameters parses transport parameters from a reader.
func ParseTransportParameters(r io.Reader) ([]TransportParameter, error) {
	qr := quicvarint.NewReader(r)
	var params []TransportParameter
	for {
		id, err := quicvarint.Read(qr)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		length, err := quicvarint.Read(qr)
		if err != nil {
			return nil, err
		}
		value := make([]byte, length)
		if _, err := io.ReadFull(qr, value); err != nil {
			return nil, err
		}
		params = append(params, TransportParameter{ID: id, Value: value})
	}
	return params, nil
}

// WriteTransportParameters writes transport parameters to a writer.
func WriteTransportParameters(w io.Writer, params []TransportParameter) error {
	var b [8]byte
	for _, p := range params {
		s := quicvarint.Append(b[:0], p.ID)
		if _, err := w.Write(s); err != nil {
			return err
		}
		s = quicvarint.Append(b[:0], uint64(len(p.Value)))
		if _, err := w.Write(s); err != nil {
			return err
		}
		if _, err := w.Write(p.Value); err != nil {
			return err
		}
	}
	return nil
}
