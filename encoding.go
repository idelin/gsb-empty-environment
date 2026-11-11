package parquetcol

import (
	"encoding/binary"
	"math"
)

// plainEncode encodes defined values using the PLAIN encoding.
func plainEncode(t PhysicalType, vals []Value) []byte {
	switch t {
	case TypeBoolean:
		out := make([]byte, (len(vals)+7)/8)
		for i, v := range vals {
			if v.BoolValue() {
				out[i/8] |= 1 << (i % 8)
			}
		}
		return out
	case TypeInt32:
		out := make([]byte, 4*len(vals))
		for i, v := range vals {
			binary.LittleEndian.PutUint32(out[i*4:], uint32(v.Int32Value()))
		}
		return out
	case TypeInt64:
		out := make([]byte, 8*len(vals))
		for i, v := range vals {
			binary.LittleEndian.PutUint64(out[i*8:], uint64(v.Int64Value()))
		}
		return out
	case TypeFloat:
		out := make([]byte, 4*len(vals))
		for i, v := range vals {
			binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(v.Float32Value()))
		}
		return out
	case TypeDouble:
		out := make([]byte, 8*len(vals))
		for i, v := range vals {
			binary.LittleEndian.PutUint64(out[i*8:], math.Float64bits(v.Float64Value()))
		}
		return out
	case TypeByteArray:
		var size int
		for _, v := range vals {
			size += 4 + len(plainBytes(v))
		}
		out := make([]byte, 0, size)
		for _, v := range vals {
			b := plainBytes(v)
			var lenBuf [4]byte
			binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(b)))
			out = append(out, lenBuf[:]...)
			out = append(out, b...)
		}
		return out
	default:
		panic("parquetcol: unknown physical type")
	}
}

func plainBytes(v Value) []byte {
	if v.kind == kindString {
		return []byte(v.str)
	}
	return v.bs
}

// plainDecode decodes count PLAIN-encoded values of the given type.
func plainDecode(t PhysicalType, data []byte, count int, isString bool) ([]Value, error) {
	out := make([]Value, 0, count)
	switch t {
	case TypeBoolean:
		for i := 0; i < count; i++ {
			out = append(out, Bool(data[i/8]&(1<<(i%8)) != 0))
		}
	case TypeInt32:
		if len(data) < 4*count {
			return nil, errCorrupt("INT32 plain data too short")
		}
		for i := 0; i < count; i++ {
			out = append(out, Int32(int32(binary.LittleEndian.Uint32(data[i*4:]))))
		}
	case TypeInt64:
		if len(data) < 8*count {
			return nil, errCorrupt("INT64 plain data too short")
		}
		for i := 0; i < count; i++ {
			out = append(out, Int64(int64(binary.LittleEndian.Uint64(data[i*8:]))))
		}
	case TypeFloat:
		if len(data) < 4*count {
			return nil, errCorrupt("FLOAT plain data too short")
		}
		for i := 0; i < count; i++ {
			out = append(out, Float32(math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))))
		}
	case TypeDouble:
		if len(data) < 8*count {
			return nil, errCorrupt("DOUBLE plain data too short")
		}
		for i := 0; i < count; i++ {
			out = append(out, Float64(math.Float64frombits(binary.LittleEndian.Uint64(data[i*8:]))))
		}
	case TypeByteArray:
		pos := 0
		for i := 0; i < count; i++ {
			if pos+4 > len(data) {
				return nil, errCorrupt("BYTE_ARRAY length truncated")
			}
			n := int(binary.LittleEndian.Uint32(data[pos:]))
			pos += 4
			if pos+n > len(data) {
				return nil, errCorrupt("BYTE_ARRAY value truncated")
			}
			if isString {
				out = append(out, String(string(data[pos:pos+n])))
			} else {
				b := make([]byte, n)
				copy(b, data[pos:pos+n])
				out = append(out, Bytes(b))
			}
			pos += n
		}
	default:
		return nil, errCorrupt("unknown physical type in page")
	}
	return out, nil
}

// dictionaryBuilder builds a PLAIN dictionary and maps values to indices.
type dictionaryBuilder struct {
	indices []int
	dict    []Value
	lookup  map[string]int
	t       PhysicalType
}

func newDictionaryBuilder(t PhysicalType) *dictionaryBuilder {
	return &dictionaryBuilder{t: t, lookup: map[string]int{}}
}

// add inserts a defined value and returns its dictionary index.
func (b *dictionaryBuilder) add(v Value) int {
	key := string(plainEncode(b.t, []Value{v}))
	if idx, ok := b.lookup[key]; ok {
		b.indices = append(b.indices, idx)
		return idx
	}
	idx := len(b.dict)
	b.lookup[key] = idx
	b.dict = append(b.dict, v)
	b.indices = append(b.indices, idx)
	return idx
}

func (b *dictionaryBuilder) size() int { return len(b.dict) }

// stats accumulates column chunk statistics over defined values.
type stats struct {
	nullCount int64
	distinct  map[string]struct{}
	min, max  []byte
	hasValue  bool
	t         PhysicalType
}

func newStats(t PhysicalType) *stats {
	return &stats{t: t, distinct: map[string]struct{}{}}
}

func (s *stats) addNull() { s.nullCount++ }

func (s *stats) addValue(v Value) {
	raw := plainEncode(s.t, []Value{v})
	s.distinct[string(raw)] = struct{}{}
	// Statistics carry the raw logical value; BYTE_ARRAY's PLAIN length prefix
	// must not be part of min/max.
	cmp := raw
	if s.t == TypeByteArray {
		cmp = plainBytes(v)
	}
	if !s.hasValue {
		s.min = append([]byte(nil), cmp...)
		s.max = append([]byte(nil), cmp...)
		s.hasValue = true
		return
	}
	if comparePlain(s.t, cmp, s.min) < 0 {
		s.min = append(s.min[:0], cmp...)
	}
	if comparePlain(s.t, cmp, s.max) > 0 {
		s.max = append(s.max[:0], cmp...)
	}
}

// comparePlain compares two single-value PLAIN encodings using signed numeric
// ordering and unsigned byte ordering.
func comparePlain(t PhysicalType, a, b []byte) int {
	switch t {
	case TypeBoolean:
		ai, bi := a[0]&1, b[0]&1
		return int(ai) - int(bi)
	case TypeInt32:
		ai := int32(binary.LittleEndian.Uint32(a))
		bi := int32(binary.LittleEndian.Uint32(b))
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		default:
			return 0
		}
	case TypeInt64:
		ai := int64(binary.LittleEndian.Uint64(a))
		bi := int64(binary.LittleEndian.Uint64(b))
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		default:
			return 0
		}
	case TypeFloat:
		ai := math.Float32frombits(binary.LittleEndian.Uint32(a))
		bi := math.Float32frombits(binary.LittleEndian.Uint32(b))
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		default:
			return 0
		}
	case TypeDouble:
		ai := math.Float64frombits(binary.LittleEndian.Uint64(a))
		bi := math.Float64frombits(binary.LittleEndian.Uint64(b))
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		default:
			return 0
		}
	default: // BYTE_ARRAY: unsigned lexicographic
		if string(a) < string(b) {
			return -1
		}
		if string(a) > string(b) {
			return 1
		}
		return 0
	}
}
