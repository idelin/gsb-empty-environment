package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
)

// cborTag represents a CBOR tagged value (major type 6).
type cborTag struct {
	num   uint64
	value any
}

// rawValue is a decoded CBOR value together with its exact encoded bytes.
type rawValue struct {
	value any
	raw   []byte
}

type cborDecoder struct {
	data []byte
	off  int
}

func decodeCBOR(data []byte) (any, error) {
	d := &cborDecoder{data: data}
	v, _, err := d.value()
	if err != nil {
		return nil, err
	}
	if d.off != len(data) {
		return nil, fmt.Errorf("cbor: %d trailing bytes", len(data)-d.off)
	}
	return v, nil
}

// cborMapEntries decodes data, which must be a single CBOR map, and returns
// its entries keyed by decoded key. Each entry value retains its raw bytes.
func cborMapEntries(data []byte) (map[any]rawValue, error) {
	d := &cborDecoder{data: data}
	head, err := d.read(1)
	if err != nil {
		return nil, err
	}
	if head[0]>>5 != 5 {
		return nil, errors.New("cbor: expected map")
	}
	n, err := d.argument(head[0] & 0x1f)
	if err != nil {
		return nil, err
	}
	entries := make(map[any]rawValue, n)
	for i := uint64(0); i < n; i++ {
		k, _, err := d.value()
		if err != nil {
			return nil, err
		}
		v, raw, err := d.value()
		if err != nil {
			return nil, err
		}
		entries[k] = rawValue{value: v, raw: raw}
	}
	if d.off != len(data) {
		return nil, fmt.Errorf("cbor: %d trailing bytes", len(data)-d.off)
	}
	return entries, nil
}

// cborArrayItems decodes data, which must be a single CBOR array, and returns
// its items. Each item retains its raw bytes.
func cborArrayItems(data []byte) ([]rawValue, error) {
	d := &cborDecoder{data: data}
	head, err := d.read(1)
	if err != nil {
		return nil, err
	}
	if head[0]>>5 != 4 {
		return nil, errors.New("cbor: expected array")
	}
	n, err := d.argument(head[0] & 0x1f)
	if err != nil {
		return nil, err
	}
	items := make([]rawValue, 0, n)
	for i := uint64(0); i < n; i++ {
		v, raw, err := d.value()
		if err != nil {
			return nil, err
		}
		items = append(items, rawValue{value: v, raw: raw})
	}
	if d.off != len(data) {
		return nil, fmt.Errorf("cbor: %d trailing bytes", len(data)-d.off)
	}
	return items, nil
}

// value decodes one CBOR data item and also returns its exact encoded bytes.
func (d *cborDecoder) value() (any, []byte, error) {
	start := d.off
	v, err := d.valueInner()
	if err != nil {
		return nil, nil, err
	}
	return v, d.data[start:d.off], nil
}

func (d *cborDecoder) valueInner() (any, error) {
	head, err := d.read(1)
	if err != nil {
		return nil, err
	}
	major, ai := head[0]>>5, head[0]&0x1f
	switch major {
	case 0:
		n, err := d.argument(ai)
		if err != nil {
			return nil, err
		}
		return n, nil
	case 1:
		n, err := d.argument(ai)
		if err != nil {
			return nil, err
		}
		if n > math.MaxInt64 {
			return nil, errors.New("cbor: negative integer overflow")
		}
		return int64(-1) - int64(n), nil
	case 2, 3:
		n, err := d.argument(ai)
		if err != nil {
			return nil, err
		}
		if n > uint64(len(d.data)-d.off) {
			return nil, errors.New("cbor: unexpected end of data")
		}
		b, err := d.read(int(n))
		if err != nil {
			return nil, err
		}
		if major == 2 {
			out := make([]byte, len(b))
			copy(out, b)
			return out, nil
		}
		return string(b), nil
	case 4:
		n, err := d.argument(ai)
		if err != nil {
			return nil, err
		}
		arr := make([]any, 0, n)
		for i := uint64(0); i < n; i++ {
			v, err := d.valueInner()
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
		m := make(map[any]any, n)
		for i := uint64(0); i < n; i++ {
			k, err := d.valueInner()
			if err != nil {
				return nil, err
			}
			v, err := d.valueInner()
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
		v, err := d.valueInner()
		if err != nil {
			return nil, err
		}
		return cborTag{num: n, value: v}, nil
	case 7:
		switch ai {
		case 20:
			return false, nil
		case 21:
			return true, nil
		case 22, 23:
			return nil, nil
		case 25:
			b, err := d.read(2)
			if err != nil {
				return nil, err
			}
			return halfToFloat(binary.BigEndian.Uint16(b)), nil
		case 26:
			b, err := d.read(4)
			if err != nil {
				return nil, err
			}
			return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), nil
		case 27:
			b, err := d.read(8)
			if err != nil {
				return nil, err
			}
			return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
		default:
			return nil, fmt.Errorf("cbor: unsupported simple/float additional info %d", ai)
		}
	default:
		return nil, fmt.Errorf("cbor: unsupported major type %d", major)
	}
}

func (d *cborDecoder) read(n int) ([]byte, error) {
	if n < 0 || len(d.data)-d.off < n {
		return nil, errors.New("cbor: unexpected end of data")
	}
	b := d.data[d.off : d.off+n]
	d.off += n
	return b, nil
}

func (d *cborDecoder) argument(ai byte) (uint64, error) {
	switch {
	case ai < 24:
		return uint64(ai), nil
	case ai == 24:
		b, err := d.read(1)
		if err != nil {
			return 0, err
		}
		return uint64(b[0]), nil
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
	default:
		return 0, fmt.Errorf("cbor: unsupported additional information %d", ai)
	}
}

func halfToFloat(h uint16) float64 {
	sign := uint32(h>>15) & 0x1
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h) & 0x3ff
	var f float64
	switch {
	case exp == 0:
		f = math.Ldexp(float64(mant), -24)
	case exp == 0x1f:
		if mant == 0 {
			f = math.Inf(1)
		} else {
			f = math.NaN()
		}
	default:
		f = math.Ldexp(float64(mant)+1024, int(exp)-25)
	}
	if sign == 1 {
		return -f
	}
	return f
}

// encodeCBOR encodes v using deterministic (canonical) CBOR. Supported types:
// nil, bool, int, int64, uint64, float64, []byte, string, []any,
// map[any]any and cborTag.
func encodeCBOR(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeValue(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeHead(buf *bytes.Buffer, major byte, n uint64) {
	switch {
	case n < 24:
		buf.WriteByte(major<<5 | byte(n))
	case n <= math.MaxUint8:
		buf.WriteByte(major<<5 | 24)
		buf.WriteByte(byte(n))
	case n <= math.MaxUint16:
		buf.WriteByte(major<<5 | 25)
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(n))
		buf.Write(b[:])
	case n <= math.MaxUint32:
		buf.WriteByte(major<<5 | 26)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(n))
		buf.Write(b[:])
	default:
		buf.WriteByte(major<<5 | 27)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], n)
		buf.Write(b[:])
	}
}

func encodeValue(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteByte(0xf6)
	case bool:
		if t {
			buf.WriteByte(0xf5)
		} else {
			buf.WriteByte(0xf4)
		}
	case int:
		return encodeValue(buf, int64(t))
	case int64:
		if t >= 0 {
			encodeHead(buf, 0, uint64(t))
		} else {
			encodeHead(buf, 1, uint64(-1-t))
		}
	case uint64:
		encodeHead(buf, 0, t)
	case float64:
		buf.WriteByte(0xfb)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], math.Float64bits(t))
		buf.Write(b[:])
	case []byte:
		encodeHead(buf, 2, uint64(len(t)))
		buf.Write(t)
	case string:
		encodeHead(buf, 3, uint64(len(t)))
		buf.WriteString(t)
	case []any:
		encodeHead(buf, 4, uint64(len(t)))
		for _, item := range t {
			if err := encodeValue(buf, item); err != nil {
				return err
			}
		}
	case map[any]any:
		type kv struct {
			key    []byte
			keyVal any
			valVal any
		}
		pairs := make([]kv, 0, len(t))
		for k, val := range t {
			kb, err := encodeCBOR(k)
			if err != nil {
				return err
			}
			pairs = append(pairs, kv{key: kb, keyVal: k, valVal: val})
		}
		sort.Slice(pairs, func(i, j int) bool {
			return bytes.Compare(pairs[i].key, pairs[j].key) < 0
		})
		encodeHead(buf, 5, uint64(len(pairs)))
		for _, p := range pairs {
			buf.Write(p.key)
			if err := encodeValue(buf, p.valVal); err != nil {
				return err
			}
		}
	case cborTag:
		encodeHead(buf, 6, t.num)
		return encodeValue(buf, t.value)
	default:
		return fmt.Errorf("cbor: cannot encode type %T", v)
	}
	return nil
}
