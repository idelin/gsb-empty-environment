package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

var (
	ErrIndefiniteLength = errors.New("netsec: indefinite-length item rejected")
	ErrFloat            = errors.New("netsec: floating-point value rejected")
	ErrUndefined        = errors.New("netsec: undefined value rejected")
	ErrDuplicateKey     = errors.New("netsec: duplicate map key rejected")
	ErrTrailingData     = errors.New("netsec: trailing data after top-level item")
	ErrUnexpectedEOF    = errors.New("netsec: unexpected end of input")
	ErrUnexpectedBreak  = errors.New("netsec: break outside indefinite-length item")
	ErrOverflow         = errors.New("netsec: integer value overflows int64")
	ErrMaxDepth         = errors.New("netsec: nesting depth limit exceeded")
)

const maxDepth = 64

// Tag is a decoded CBOR tag (major type 6).
type Tag struct {
	Number  uint64
	Content any
}

// SimpleValue is a CBOR simple value (major type 7, additional info 24)
// other than false, null and undefined.
type SimpleValue uint8

// Decode parses exactly one CBOR data item under strict rules: any
// indefinite-length item, floating-point value or undefined value is
// rejected, so the decoded value re-encodes to identical boundaries.
func Decode(data []byte) (any, error) {
	d := &decoder{data: data}
	v, err := d.item(0)
	if err != nil {
		return nil, err
	}
	if d.off != len(d.data) {
		return nil, ErrTrailingData
	}
	return v, nil
}

type decoder struct {
	data []byte
	off  int
}

func (d *decoder) byte() (byte, error) {
	if d.off >= len(d.data) {
		return 0, ErrUnexpectedEOF
	}
	b := d.data[d.off]
	d.off++
	return b, nil
}

func (d *decoder) read(n int) ([]byte, error) {
	if n < 0 || len(d.data)-d.off < n {
		return nil, ErrUnexpectedEOF
	}
	b := d.data[d.off : d.off+n]
	d.off += n
	return b, nil
}

// argument reads the argument of a head byte given its additional info.
// It reports ErrIndefiniteLength for additional info 31.
func (d *decoder) argument(ai byte) (uint64, error) {
	switch {
	case ai < 24:
		return uint64(ai), nil
	case ai == 24:
		b, err := d.byte()
		if err != nil {
			return 0, err
		}
		return uint64(b), nil
	case ai == 25:
		b, err := d.read(2)
		if err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint16(b)), nil
	case ai == 26:
		b, err := d.read(4)
		if err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint32(b)), nil
	case ai == 27:
		b, err := d.read(8)
		if err != nil {
			return 0, err
		}
		return binary.BigEndian.Uint64(b), nil
	case ai == 31:
		return 0, ErrIndefiniteLength
	default:
		return 0, fmt.Errorf("netsec: reserved additional information %d", ai)
	}
}

func (d *decoder) item(depth int) (any, error) {
	if depth > maxDepth {
		return nil, ErrMaxDepth
	}
	head, err := d.byte()
	if err != nil {
		return nil, err
	}
	major, ai := head>>5, head&0x1f

	switch major {
	case 0:
		return d.argument(ai)
	case 1:
		n, err := d.argument(ai)
		if err != nil {
			return nil, err
		}
		if n > math.MaxInt64 {
			return nil, ErrOverflow
		}
		return -1 - int64(n), nil
	case 2:
		n, err := d.argument(ai)
		if err != nil {
			return nil, err
		}
		if n > uint64(len(d.data)-d.off) {
			return nil, ErrUnexpectedEOF
		}
		b, err := d.read(int(n))
		if err != nil {
			return nil, err
		}
		out := make([]byte, len(b))
		copy(out, b)
		return out, nil
	case 3:
		n, err := d.argument(ai)
		if err != nil {
			return nil, err
		}
		if n > uint64(len(d.data)-d.off) {
			return nil, ErrUnexpectedEOF
		}
		b, err := d.read(int(n))
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case 4:
		n, err := d.argument(ai)
		if err != nil {
			return nil, err
		}
		if n > uint64(len(d.data)-d.off) {
			return nil, ErrUnexpectedEOF
		}
		arr := make([]any, 0, n)
		for i := uint64(0); i < n; i++ {
			v, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	case 5:
		n, err := d.argument(ai)
		if err != nil {
			return nil, err
		}
		if n > uint64(len(d.data)-d.off)/2 {
			return nil, ErrUnexpectedEOF
		}
		m := make(map[any]any, n)
		seen := make(map[string]struct{}, n)
		for i := uint64(0); i < n; i++ {
			k, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			fp := fmt.Sprintf("%#v", k)
			if _, dup := seen[fp]; dup {
				return nil, fmt.Errorf("%w: %v", ErrDuplicateKey, k)
			}
			seen[fp] = struct{}{}
			v, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			m[k] = v
		}
		return m, nil
	case 6:
		n, err := d.argument(ai)
		if err != nil {
			return nil, err
		}
		c, err := d.item(depth + 1)
		if err != nil {
			return nil, err
		}
		return Tag{Number: n, Content: c}, nil
	default: // major 7
		switch ai {
		case 20:
			return false, nil
		case 21:
			return true, nil
		case 22:
			return nil, nil
		case 23:
			return nil, ErrUndefined
		case 24:
			b, err := d.byte()
			if err != nil {
				return nil, err
			}
			if b < 32 {
				return nil, fmt.Errorf("netsec: malformed simple value %d", b)
			}
			return SimpleValue(b), nil
		case 25, 26, 27:
			return nil, ErrFloat
		case 31:
			return nil, ErrUnexpectedBreak
		default:
			return nil, fmt.Errorf("netsec: reserved additional information %d", ai)
		}
	}
}
