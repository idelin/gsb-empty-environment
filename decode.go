package baselinepatch

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

var (
	errTruncated       = errors.New("baselinepatch: truncated encoding")
	errUnknownTag      = errors.New("baselinepatch: unknown type tag")
	errLengthPrefix    = errors.New("baselinepatch: invalid length prefix")
	errDuplicateKey    = errors.New("baselinepatch: duplicate object key")
	errAllocationLimit = errors.New("baselinepatch: declared length exceeds allocation limit")
)

// reader is a bounds-checked cursor over a fixed buffer.
type reader struct {
	data []byte
	pos  int
}

func (r *reader) byte() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, errTruncated
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.data) {
		return nil, errTruncated
	}
	out := r.data[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

// readUvarint decodes a binary Uvarint and rejects encodings longer than the
// canonical maximum (10 bytes) or with a trailing overflow-indicating byte.
func (r *reader) readUvarint() (uint64, error) {
	var x uint64
	var s uint
	for i := 0; i < binary.MaxVarintLen64; i++ {
		if r.pos >= len(r.data) {
			return 0, errTruncated
		}
		b := r.data[r.pos]
		r.pos++
		if b < 0x80 {
			if i == binary.MaxVarintLen64-1 && b > 1 {
				return 0, errLengthPrefix
			}
			return x | uint64(b)<<s, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, errLengthPrefix
}

// readLength is the "validate length before allocate" gate shared by every
// sized payload: it proves (a) the declared count/byte-length is within the
// hard cap and (b) the buffer actually contains that many bytes right now.
// Allocation happens only after both checks pass.
func (r *reader) readLength(capN int, what string) (int, error) {
	n64, err := r.readUvarint()
	if err != nil {
		return 0, err
	}
	if n64 > uint64(capN) {
		return 0, fmt.Errorf("%w: %s length %d > %d", errAllocationLimit, what, n64, capN)
	}
	n := int(n64)
	if n > len(r.data)-r.pos {
		return 0, errTruncated
	}
	return n, nil
}

func decodeValue(r *reader) (any, error) {
	tag, err := r.byte()
	if err != nil {
		return nil, err
	}
	switch tag {
	case tagNull:
		return nil, nil
	case tagBool:
		b, err := r.byte()
		if err != nil {
			return nil, err
		}
		switch b {
		case 0:
			return false, nil
		case 1:
			return true, nil
		default:
			return nil, fmt.Errorf("baselinepatch: invalid bool byte %d", b)
		}
	case tagNumber:
		raw, err := r.bytes(8)
		if err != nil {
			return nil, err
		}
		bits := uint64(raw[0])<<56 | uint64(raw[1])<<48 | uint64(raw[2])<<40 |
			uint64(raw[3])<<32 | uint64(raw[4])<<24 | uint64(raw[5])<<16 |
			uint64(raw[6])<<8 | uint64(raw[7])
		f := math.Float64frombits(bits)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%w: non-finite number", ErrUnsupportedValue)
		}
		return f, nil
	case tagString:
		n, err := r.readLength(maxBinaryBytes, "string")
		if err != nil {
			return nil, err
		}
		raw, err := r.bytes(n)
		if err != nil {
			return nil, err
		}
		return string(raw), nil
	case tagArray:
		count, err := r.readLength(maxArrayMembers, "array")
		if err != nil {
			return nil, err
		}
		out := make([]any, count)
		for i := 0; i < count; i++ {
			out[i], err = decodeValue(r)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case tagObject:
		countKey, err := r.readLength(maxObjectKeys, "object")
		if err != nil {
			return nil, err
		}
		out := make(map[string]any, countKey)
		var lastKey string
		for i := 0; i < countKey; i++ {
			kn, err := r.readLength(maxBinaryBytes, "key")
			if err != nil {
				return nil, err
			}
			kraw, err := r.bytes(kn)
			if err != nil {
				return nil, err
			}
			key := string(kraw)
			if i > 0 && key <= lastKey {
				return nil, errDuplicateKey
			}
			lastKey = key
			val, err := decodeValue(r)
			if err != nil {
				return nil, err
			}
			out[key] = val
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: tag %d", errUnknownTag, tag)
	}
}
