package wire

import (
	"io"
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
	TransportParameterMaxRecordSize                  uint64 = 0x0571c59429cd0845
)

// ParseTransportParameters parses transport parameters from a reader.
func ParseTransportParameters(r io.Reader) ([]TransportParameter, error) {
	var params []TransportParameter
	for {
		id, err := ReadVarInt(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		length, err := ReadVarInt(r)
		if err != nil {
			return nil, err
		}
		value := make([]byte, length)
		if _, err := io.ReadFull(r, value); err != nil {
			return nil, err
		}
		params = append(params, TransportParameter{ID: id, Value: value})
	}
	return params, nil
}

// WriteTransportParameters writes transport parameters to a writer.
func WriteTransportParameters(w io.Writer, params []TransportParameter) error {
	for _, p := range params {
		if err := WriteVarInt(w, p.ID); err != nil {
			return err
		}
		if err := WriteVarInt(w, uint64(len(p.Value))); err != nil {
			return err
		}
		if _, err := w.Write(p.Value); err != nil {
			return err
		}
	}
	return nil
}
