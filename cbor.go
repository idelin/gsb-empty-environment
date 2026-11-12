package netsec

import (
	"encoding/binary"
	"fmt"
	"math"
	"unicode/utf8"
)

const maxCBORDepth = 64

var cborBreak = &struct{ breakMarker bool }{breakMarker: true}

type cborDecoder struct {
	data  []byte
	off   int
	depth int
}

func decodeCBOR(data []byte) (any, error) {
	d := &cborDecoder{data: data}
	v, err := d.value()
	if err != nil {
		return nil, err
	}
	if d.off != len(data) {
		return nil, fmt.Errorf("%w: %d trailing CBOR byte(s)", ErrMalformedToken, len(data)-d.off)
	}
	return v, nil
}

func (d *cborDecoder) value() (any, error) {
	if d.depth > maxCBORDepth {
		return nil, fmt.Errorf("%w: CBOR nesting too deep", ErrMalformedToken)
	}
	if d.off >= len(d.data) {
		return nil, fmt.Errorf("%w: unexpected end of CBOR input", ErrMalformedToken)
	}

	major, n, indefinite, err := d.header()
	if err != nil {
		return nil, err
	}

	switch major {
	case 0:
		if indefinite {
			return nil, fmt.Errorf("%w: indefinite unsigned integer", ErrMalformedToken)
		}
		return n, nil
	case 1:
		if indefinite {
			return nil, fmt.Errorf("%w: indefinite negative integer", ErrMalformedToken)
		}
		return -1 - int64(n), nil
	case 2:
		return d.byteString(n, indefinite)
	case 3:
		return d.textString(n, indefinite)
	case 4:
		d.depth++
		defer func() { d.depth-- }()
		return d.array(n, indefinite)
	case 5:
		d.depth++
		defer func() { d.depth-- }()
		return d.mapValue(n, indefinite)
	case 6:
		if indefinite {
			return nil, fmt.Errorf("%w: indefinite tag", ErrMalformedToken)
		}
		// Tags are transparent for the claims we consume (COSE_Sign1 tag 18
		// and CCA collection tag 399 included).
		d.depth++
		defer func() { d.depth-- }()
		return d.value()
	case 7:
		return d.simple(n, indefinite)
	default:
		return nil, fmt.Errorf("%w: unknown CBOR major type %d", ErrMalformedToken, major)
	}
}

func (d *cborDecoder) header() (major byte, n uint64, indefinite bool, err error) {
	b := d.data[d.off]
	d.off++
	major = b >> 5
	ai := b & 0x1f

	switch {
	case ai < 24:
		return major, uint64(ai), false, nil
	case ai == 24:
		if d.off+1 > len(d.data) {
			return 0, 0, false, fmt.Errorf("%w: truncated CBOR header", ErrMalformedToken)
		}
		n = uint64(d.data[d.off])
		d.off++
		return major, n, false, nil
	case ai == 25:
		if d.off+2 > len(d.data) {
			return 0, 0, false, fmt.Errorf("%w: truncated CBOR header", ErrMalformedToken)
		}
		n = uint64(binary.BigEndian.Uint16(d.data[d.off : d.off+2]))
		d.off += 2
		return major, n, false, nil
	case ai == 26:
		if d.off+4 > len(d.data) {
			return 0, 0, false, fmt.Errorf("%w: truncated CBOR header", ErrMalformedToken)
		}
		n = uint64(binary.BigEndian.Uint32(d.data[d.off : d.off+4]))
		d.off += 4
		return major, n, false, nil
	case ai == 27:
		if d.off+8 > len(d.data) {
			return 0, 0, false, fmt.Errorf("%w: truncated CBOR header", ErrMalformedToken)
		}
		n = binary.BigEndian.Uint64(d.data[d.off : d.off+8])
		d.off += 8
		return major, n, false, nil
	case ai == 31:
		return major, 0, true, nil
	default:
		return 0, 0, false, fmt.Errorf("%w: reserved CBOR additional info %d", ErrMalformedToken, ai)
	}
}

func (d *cborDecoder) byteString(n uint64, indefinite bool) (any, error) {
	if !indefinite {
		if n > uint64(len(d.data)-d.off) {
			return nil, fmt.Errorf("%w: truncated byte string", ErrMalformedToken)
		}
		out := append([]byte(nil), d.data[d.off:d.off+int(n)]...)
		d.off += int(n)
		return out, nil
	}

	var out []byte
	for {
		if d.off >= len(d.data) {
			return nil, fmt.Errorf("%w: unterminated indefinite byte string", ErrMalformedToken)
		}
		v, err := d.value()
		if err != nil {
			return nil, err
		}
		if v == cborBreak {
			return out, nil
		}
		chunk, ok := v.([]byte)
		if !ok {
			return nil, fmt.Errorf("%w: invalid chunk in indefinite byte string", ErrMalformedToken)
		}
		out = append(out, chunk...)
	}
}

func (d *cborDecoder) textString(n uint64, indefinite bool) (any, error) {
	if !indefinite {
		if n > uint64(len(d.data)-d.off) {
			return nil, fmt.Errorf("%w: truncated text string", ErrMalformedToken)
		}
		raw := d.data[d.off : d.off+int(n)]
		d.off += int(n)
		if !utf8.Valid(raw) {
			return nil, fmt.Errorf("%w: invalid UTF-8 text string", ErrMalformedToken)
		}
		return string(raw), nil
	}

	var out string
	for {
		if d.off >= len(d.data) {
			return nil, fmt.Errorf("%w: unterminated indefinite text string", ErrMalformedToken)
		}
		v, err := d.value()
		if err != nil {
			return nil, err
		}
		if v == cborBreak {
			return out, nil
		}
		chunk, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%w: invalid chunk in indefinite text string", ErrMalformedToken)
		}
		out += chunk
	}
}

func (d *cborDecoder) array(n uint64, indefinite bool) (any, error) {
	const maxItems = 1 << 20
	out := []any{}
	if !indefinite {
		if n > maxItems {
			return nil, fmt.Errorf("%w: CBOR array too large", ErrMalformedToken)
		}
		out = make([]any, 0, int(n))
		for i := uint64(0); i < n; i++ {
			v, err := d.value()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}

	for {
		if d.off >= len(d.data) {
			return nil, fmt.Errorf("%w: unterminated indefinite array", ErrMalformedToken)
		}
		v, err := d.value()
		if err != nil {
			return nil, err
		}
		if v == cborBreak {
			return out, nil
		}
		if len(out) >= maxItems {
			return nil, fmt.Errorf("%w: CBOR array too large", ErrMalformedToken)
		}
		out = append(out, v)
	}
}

func (d *cborDecoder) mapValue(n uint64, indefinite bool) (any, error) {
	const maxItems = 1 << 20
	out := make(map[any]any)

	readPair := func() error {
		k, err := d.value()
		if err != nil {
			return err
		}
		if k == cborBreak {
			return fmt.Errorf("%w: break in CBOR map key", ErrMalformedToken)
		}
		if !isComparableCBORKey(k) {
			return fmt.Errorf("%w: unsupported CBOR map key type %T", ErrMalformedToken, k)
		}
		if _, exists := out[k]; exists {
			return fmt.Errorf("%w: duplicate CBOR map key", ErrMalformedToken)
		}
		v, err := d.value()
		if err != nil {
			return err
		}
		if v == cborBreak {
			return fmt.Errorf("%w: break in CBOR map value", ErrMalformedToken)
		}
		out[k] = v
		return nil
	}

	if !indefinite {
		if n > maxItems {
			return nil, fmt.Errorf("%w: CBOR map too large", ErrMalformedToken)
		}
		for i := uint64(0); i < n; i++ {
			if err := readPair(); err != nil {
				return nil, err
			}
		}
		return out, nil
	}

	for {
		if d.off >= len(d.data) {
			return nil, fmt.Errorf("%w: unterminated indefinite map", ErrMalformedToken)
		}
		if d.data[d.off] == 0xff {
			d.off++
			return out, nil
		}
		if len(out) >= maxItems {
			return nil, fmt.Errorf("%w: CBOR map too large", ErrMalformedToken)
		}
		if err := readPair(); err != nil {
			return nil, err
		}
	}
}

func (d *cborDecoder) simple(n uint64, indefinite bool) (any, error) {
	if indefinite {
		return cborBreak, nil
	}
	switch n {
	case 20:
		return false, nil
	case 21:
		return true, nil
	case 22, 23:
		return nil, nil
	case 24:
		if d.off >= len(d.data) {
			return nil, fmt.Errorf("%w: truncated simple value", ErrMalformedToken)
		}
		sv := d.data[d.off]
		d.off++
		return sv, nil
	case 25:
		if d.off+2 > len(d.data) {
			return nil, fmt.Errorf("%w: truncated half float", ErrMalformedToken)
		}
		h := binary.BigEndian.Uint16(d.data[d.off : d.off+2])
		d.off += 2
		return halfToFloat64(h), nil
	case 26:
		if d.off+4 > len(d.data) {
			return nil, fmt.Errorf("%w: truncated float", ErrMalformedToken)
		}
		f := math.Float32frombits(binary.BigEndian.Uint32(d.data[d.off : d.off+4]))
		d.off += 4
		return float64(f), nil
	case 27:
		if d.off+8 > len(d.data) {
			return nil, fmt.Errorf("%w: truncated double", ErrMalformedToken)
		}
		f := math.Float64frombits(binary.BigEndian.Uint64(d.data[d.off : d.off+8]))
		d.off += 8
		return f, nil
	default:
		// Other simple values are not used by CCA claims.
		return n, nil
	}
}

func isComparableCBORKey(v any) bool {
	switch v.(type) {
	case string, uint64, int64, int, bool:
		return true
	default:
		return false
	}
}

func halfToFloat64(h uint16) float64 {
	sign := h >> 15
	exp := int((h >> 10) & 0x1f)
	mant := h & 0x3ff

	var v float64
	switch exp {
	case 0:
		v = math.Ldexp(float64(mant), -24)
	case 31:
		if mant == 0 {
			v = math.Inf(1)
		} else {
			v = math.NaN()
		}
	default:
		v = math.Ldexp(float64(mant+1024), exp-25)
	}
	if sign == 1 {
		v = -v
	}
	return v
}
