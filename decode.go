package canon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// MaxDepth bounds nesting of arrays/objects during decoding.
const MaxDepth = 512

var (
	ErrTrailingData  = errors.New("canon: trailing bytes after value")
	ErrNonCanonical  = errors.New("canon: non-canonical encoding")
	ErrDepthLimit    = errors.New("canon: nesting depth limit exceeded")
	errTruncated     = errors.New("canon: unexpected end of input")
	errUnknownTag    = errors.New("canon: unknown type tag")
	errNonFiniteWire = errors.New("canon: non-finite float on the wire")
)

// Decode parses one canonical value and requires it to consume all of data.
// It never executes host code: the format carries no type names or
// constructors, only plain data. Every length prefix is validated against
// the remaining input before any allocation happens.
func Decode(data []byte) (Value, error) {
	d := &decoder{buf: data}
	v, err := d.value(0)
	if err != nil {
		return nil, err
	}
	if len(d.buf) != 0 {
		return nil, ErrTrailingData
	}
	return v, nil
}

type decoder struct {
	buf []byte
}

func (d *decoder) readByte() (byte, error) {
	if len(d.buf) == 0 {
		return 0, errTruncated
	}
	b := d.buf[0]
	d.buf = d.buf[1:]
	return b, nil
}

// readUvarint reads a uvarint and rejects non-minimal (non-canonical) forms.
func (d *decoder) readUvarint() (uint64, error) {
	u, n := binary.Uvarint(d.buf)
	if n == 0 {
		return 0, errTruncated
	}
	if n < 0 {
		return 0, fmt.Errorf("canon: varint overflows 64 bits")
	}
	var tmp [binary.MaxVarintLen64]byte
	m := binary.PutUvarint(tmp[:], u)
	if m != n || !bytes.Equal(tmp[:m], d.buf[:n]) {
		return 0, ErrNonCanonical
	}
	d.buf = d.buf[n:]
	return u, nil
}

// readBytes validates n against the remaining input BEFORE slicing, so a
// hostile length prefix can never trigger a huge or out-of-range read.
func (d *decoder) readBytes(n uint64) ([]byte, error) {
	if n > uint64(len(d.buf)) {
		return nil, errTruncated
	}
	b := d.buf[:n]
	d.buf = d.buf[n:]
	return b, nil
}

// readCount validates a container element count before allocation. Every
// element needs at least one byte, so count > remaining bytes is impossible.
func (d *decoder) readCount() (uint64, error) {
	n, err := d.readUvarint()
	if err != nil {
		return 0, err
	}
	if n > uint64(len(d.buf)) {
		return 0, errTruncated
	}
	return n, nil
}

func (d *decoder) value(depth int) (Value, error) {
	if depth > MaxDepth {
		return nil, ErrDepthLimit
	}
	tag, err := d.readByte()
	if err != nil {
		return nil, err
	}
	switch tag {
	case tagNull:
		return Null{}, nil
	case tagFalse:
		return Bool(false), nil
	case tagTrue:
		return Bool(true), nil
	case tagInt:
		u, err := d.readUvarint()
		if err != nil {
			return nil, err
		}
		return Int(int64(u>>1) ^ -int64(u&1)), nil
	case tagFloat:
		b, err := d.readBytes(8)
		if err != nil {
			return nil, err
		}
		f := math.Float64frombits(binary.BigEndian.Uint64(b))
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, errNonFiniteWire
		}
		if f == 0 && math.Signbit(f) {
			return nil, ErrNonCanonical // -0.0 must be encoded as +0.0
		}
		return Float(f), nil
	case tagString:
		n, err := d.readUvarint()
		if err != nil {
			return nil, err
		}
		b, err := d.readBytes(n)
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(b) {
			return nil, fmt.Errorf("canon: invalid UTF-8 in string")
		}
		return String(string(b)), nil
	case tagBytes:
		n, err := d.readUvarint()
		if err != nil {
			return nil, err
		}
		b, err := d.readBytes(n)
		if err != nil {
			return nil, err
		}
		out := make(Bytes, len(b))
		copy(out, b)
		return out, nil
	case tagArray:
		n, err := d.readCount()
		if err != nil {
			return nil, err
		}
		arr := make(Array, 0, n)
		for i := uint64(0); i < n; i++ {
			e, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, e)
		}
		return arr, nil
	case tagObject:
		n, err := d.readCount()
		if err != nil {
			return nil, err
		}
		obj := make(Object, n)
		prev := ""
		for i := uint64(0); i < n; i++ {
			ktag, err := d.readByte()
			if err != nil {
				return nil, err
			}
			if ktag != tagString {
				return nil, fmt.Errorf("canon: object key must be a string")
			}
			klen, err := d.readUvarint()
			if err != nil {
				return nil, err
			}
			kb, err := d.readBytes(klen)
			if err != nil {
				return nil, err
			}
			if !utf8.Valid(kb) {
				return nil, fmt.Errorf("canon: invalid UTF-8 in object key")
			}
			k := string(kb)
			if i > 0 && k <= prev {
				return nil, ErrNonCanonical // keys must be sorted and unique
			}
			prev = k
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			obj[k] = v
		}
		return obj, nil
	default:
		return nil, fmt.Errorf("%w: 0x%02x", errUnknownTag, tag)
	}
}
