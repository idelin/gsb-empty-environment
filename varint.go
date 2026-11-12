package netsec

import (
	"errors"
	"fmt"
)

// ErrInvalidVarint reports a malformed or truncated QUIC variable-length
// integer.
var ErrInvalidVarint = errors.New("netsec: invalid QUIC varint")

// DecodeVarint decodes one QUIC variable-length integer from the beginning of
// b. It returns the decoded value and the unconsumed suffix. Both canonical
// and non-canonical encodings are accepted, as required by QUIC.
func DecodeVarint(b []byte) (value uint64, rest []byte, err error) {
	if len(b) == 0 {
		return 0, nil, ErrInvalidVarint
	}

	length := 1 << (b[0] >> 6)
	if len(b) < length {
		return 0, nil, ErrInvalidVarint
	}

	switch length {
	case 1:
		value = uint64(b[0] & 0x3f)
	case 2:
		value = uint64(b[0]&0x3f)<<8 | uint64(b[1])
	case 4:
		value = uint64(b[0]&0x3f)<<24 |
			uint64(b[1])<<16 |
			uint64(b[2])<<8 |
			uint64(b[3])
	case 8:
		value = uint64(b[0]&0x3f)<<56 |
			uint64(b[1])<<48 |
			uint64(b[2])<<40 |
			uint64(b[3])<<32 |
			uint64(b[4])<<24 |
			uint64(b[5])<<16 |
			uint64(b[6])<<8 |
			uint64(b[7])
	default:
		return 0, nil, fmt.Errorf("%w: impossible length %d", ErrInvalidVarint, length)
	}

	return value, b[length:], nil
}

// EncodeVarint encodes v as the shortest QUIC variable-length integer.
func EncodeVarint(v uint64) ([]byte, error) {
	switch {
	case v < 1<<6:
		return []byte{byte(v)}, nil
	case v < 1<<14:
		return []byte{
			0x40 | byte(v>>8),
			byte(v),
		}, nil
	case v < 1<<30:
		return []byte{
			0x80 | byte(v>>24),
			byte(v >> 16),
			byte(v >> 8),
			byte(v),
		}, nil
	case v < 1<<62:
		return []byte{
			0xc0 | byte(v>>56),
			byte(v >> 48),
			byte(v >> 40),
			byte(v >> 32),
			byte(v >> 24),
			byte(v >> 16),
			byte(v >> 8),
			byte(v),
		}, nil
	default:
		return nil, fmt.Errorf("netsec: varint value %d exceeds 2^62-1", v)
	}
}

// EncodeQuarterStreamID encodes a Quarter Stream ID followed by payload.
func EncodeQuarterStreamID(id uint64, payload []byte) ([]byte, error) {
	if id > MaxQuarterStreamID {
		return nil, fmt.Errorf("netsec: quarter stream ID %d exceeds 2^60-1", id)
	}
	header, err := EncodeVarint(id)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(header)+len(payload))
	copy(out, header)
	copy(out[len(header):], payload)
	return out, nil
}
