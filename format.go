package lpc

import (
	"encoding/binary"
	"hash/crc32"
)

// On-disk code header (all multi-byte integers are big-endian):
//
//	offset  size  field
//	0       4     magic "LCPC"
//	4       1     format version (currently 1)
//	5       1     maximum code length L
//	6       1     reserved, must be 0
//	7       4     symbol count N
//	11      N     one code length per symbol, symbol order
//	11+N    4     CRC-32/IEEE over bytes [0, 11+N)
//
// Version, every length and the checksum are explicit; code words are not
// stored because they are canonical and derived from the lengths.

// Marshal serializes the code header with an explicit CRC-32 checksum.
func (c *Code) Marshal() ([]byte, error) {
	if c == nil || c.Version != FormatVersion {
		return nil, ErrBadHeader
	}
	n := len(c.Lengths)
	if n > 0xffffffff {
		return nil, ErrBadHeader
	}
	if c.MaxLen > MaxLength {
		return nil, ErrBadHeader
	}

	buf := make([]byte, 11+n+4)
	copy(buf[0:4], Magic)
	buf[4] = FormatVersion
	buf[5] = byte(c.MaxLen)
	buf[6] = 0
	binary.BigEndian.PutUint32(buf[7:11], uint32(n))
	for i, l := range c.Lengths {
		if l < 0 || l > c.MaxLen {
			return nil, ErrBadHeader
		}
		buf[11+i] = byte(l)
	}
	sum := crc32.ChecksumIEEE(buf[:11+n])
	binary.BigEndian.PutUint32(buf[11+n:], sum)
	return buf, nil
}

// UnmarshalCode parses and validates a code header, verifying magic,
// version, reserved byte, length bounds, Kraft equality and checksum.
func UnmarshalCode(data []byte) (*Code, error) {
	if len(data) < 15 {
		return nil, ErrBadHeader
	}
	if string(data[0:4]) != Magic {
		return nil, ErrBadHeader
	}
	if data[4] != FormatVersion {
		return nil, ErrBadHeader
	}
	if data[6] != 0 {
		return nil, ErrBadHeader
	}
	maxLen := int(data[5])
	if maxLen > MaxLength {
		return nil, ErrBadHeader
	}
	n := int(binary.BigEndian.Uint32(data[7:11]))
	if len(data) != 11+n+4 {
		return nil, ErrBadHeader
	}

	stored := binary.BigEndian.Uint32(data[11+n:])
	if crc32.ChecksumIEEE(data[:11+n]) != stored {
		return nil, ErrChecksum
	}

	lengths := make([]int, n)
	for i := range lengths {
		l := int(data[11+i])
		if l > maxLen {
			return nil, ErrBadHeader
		}
		lengths[i] = l
	}

	switch {
	case n == 0:
		return nil, ErrBadHeader
	case n == 1:
		// Only the empty code word (length 0) is prefix-valid for an
		// alphabet of one.
		if lengths[0] != 0 {
			return nil, ErrBadHeader
		}
	default:
		if maxLen == 0 {
			return nil, ErrBadHeader
		}
		if !feasible(n, maxLen) {
			return nil, ErrBadHeader
		}
		for _, l := range lengths {
			if l < 1 {
				return nil, ErrBadHeader
			}
		}
	}

	c, err := makeCode(maxLen, lengths)
	if err != nil {
		// Any structural failure (Kraft inequality, overflow, ...) means
		// the stored lengths are not a valid complete prefix code.
		return nil, ErrBadHeader
	}
	return c, nil
}
