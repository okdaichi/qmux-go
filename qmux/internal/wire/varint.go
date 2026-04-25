package wire

import (
	"errors"
	"io"
)

// ReadVarInt reads a variable-length integer.
func ReadVarInt(r io.Reader) (uint64, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:1]); err != nil {
		return 0, err
	}

	tag := b[0] >> 6
	length := 1 << tag
	if length > 1 {
		if _, err := io.ReadFull(r, b[1:length]); err != nil {
			return 0, err
		}
	}

	var val uint64
	switch tag {
	case 0:
		val = uint64(b[0] & 0x3f)
	case 1:
		val = uint64(b[0]&0x3f)<<8 | uint64(b[1])
	case 2:
		val = uint64(b[0]&0x3f)<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
	case 3:
		val = uint64(b[0]&0x3f)<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
			uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
	}

	return val, nil
}

// WriteVarInt writes a variable-length integer.
func WriteVarInt(w io.Writer, i uint64) error {
	if i <= 0x3f {
		_, err := w.Write([]byte{byte(i)})
		return err
	} else if i <= 0x3fff {
		_, err := w.Write([]byte{byte(i>>8) | 0x40, byte(i)})
		return err
	} else if i <= 0x3fffffff {
		_, err := w.Write([]byte{
			byte(i>>24) | 0x80,
			byte(i >> 16),
			byte(i >> 8),
			byte(i),
		})
		return err
	} else if i <= 0x3fffffffffffffff {
		_, err := w.Write([]byte{
			byte(i>>56) | 0xc0,
			byte(i >> 48),
			byte(i >> 40),
			byte(i >> 32),
			byte(i >> 24),
			byte(i >> 16),
			byte(i >> 8),
			byte(i),
		})
		return err
	}
	return errors.New("varint too large")
}

// VarIntLen returns the length of a variable-length integer in bytes.
func VarIntLen(i uint64) int {
	if i <= 0x3f {
		return 1
	} else if i <= 0x3fff {
		return 2
	} else if i <= 0x3fffffff {
		return 4
	} else if i <= 0x3fffffffffffffff {
		return 8
	}
	return 0
}
