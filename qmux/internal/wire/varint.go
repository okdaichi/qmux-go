package wire

import (
	"errors"
	"io"
)

const (
	maxVarInt1 = 0x3f
	maxVarInt2 = 0x3fff
	maxVarInt4 = 0x3fffffff
	maxVarInt8 = 0x3fffffffffffffff

	maskVarInt1 = 0x00
	maskVarInt2 = 0x40
	maskVarInt4 = 0x80
	maskVarInt8 = 0xc0
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
	if i <= maxVarInt1 {
		_, err := w.Write([]byte{byte(i) | maskVarInt1})
		return err
	} else if i <= maxVarInt2 {
		_, err := w.Write([]byte{byte(i>>8) | maskVarInt2, byte(i)})
		return err
	} else if i <= maxVarInt4 {
		_, err := w.Write([]byte{
			byte(i>>24) | maskVarInt4,
			byte(i >> 16),
			byte(i >> 8),
			byte(i),
		})
		return err
	} else if i <= maxVarInt8 {
		_, err := w.Write([]byte{
			byte(i>>56) | maskVarInt8,
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
	if i <= maxVarInt1 {
		return 1
	} else if i <= maxVarInt2 {
		return 2
	} else if i <= maxVarInt4 {
		return 4
	} else if i <= maxVarInt8 {
		return 8
	}
	return 0
}
