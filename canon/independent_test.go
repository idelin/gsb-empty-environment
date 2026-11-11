package canon

import (
	"sort"
	"unicode/utf8"
)

// This file contains a deliberately independent, hand-rolled encoder. It shares
// no helper with the production implementation so that round-trip tests do not
// accidentally validate the package against itself.

type indepMap struct {
	pairs []indepPair
}

type indepPair struct {
	key   any
	value any
}

type indepBuffer struct{ b []byte }

func (buf *indepBuffer) byte8(v byte) { buf.b = append(buf.b, v) }

// head writes a major type + argument in the shortest CBOR form.
func (buf *indepBuffer) head(major byte, n uint64) {
	major <<= 5
	switch {
	case n < 24:
		buf.byte8(major | byte(n))
	case n < 1<<8:
		buf.byte8(major | 24)
		buf.byte8(byte(n))
	case n < 1<<16:
		buf.byte8(major | 25)
		buf.byte8(byte(n >> 8))
		buf.byte8(byte(n))
	case n < 1<<32:
		buf.byte8(major | 26)
		for shift := 24; shift >= 0; shift -= 8 {
			buf.byte8(byte(n >> shift))
		}
	default:
		buf.byte8(major | 27)
		for shift := 56; shift >= 0; shift -= 8 {
			buf.byte8(byte(n >> shift))
		}
	}
}

func indepEncode(v any) ([]byte, error) {
	buf := &indepBuffer{}
	if err := indepWrite(buf, v); err != nil {
		return nil, err
	}
	return buf.b, nil
}

func indepWrite(buf *indepBuffer, v any) error {
	switch t := v.(type) {
	case int64:
		indepInt(buf, t)
	case int:
		indepInt(buf, int64(t))
	case uint64:
		buf.head(0, t)
	case uint:
		buf.head(0, uint64(t))
	case string:
		if !utf8.ValidString(t) {
			return ErrInvalidUTF8
		}
		buf.head(3, uint64(len(t)))
		buf.b = append(buf.b, t...)
	case []byte:
		buf.head(2, uint64(len(t)))
		buf.b = append(buf.b, t...)
	case []any:
		buf.head(4, uint64(len(t)))
		for _, child := range t {
			if err := indepWrite(buf, child); err != nil {
				return err
			}
		}
	case indepMap:
		return indepWriteMap(buf, t)
	case Map:
		m := indepMap{}
		for i := 0; i < t.Len(); i++ {
			e := t.Entry(i)
			m.pairs = append(m.pairs, indepPair{key: e.Key, value: e.Value})
		}
		return indepWriteMap(buf, m)
	default:
		return &UnsupportedTypeError{}
	}
	return nil
}

func indepInt(buf *indepBuffer, n int64) {
	if n >= 0 {
		buf.head(0, uint64(n))
		return
	}
	buf.head(1, uint64(-1-n))
}

func indepWriteMap(buf *indepBuffer, m indepMap) error {
	type encoded struct {
		raw []byte
		p   indepPair
	}
	all := make([]encoded, 0, len(m.pairs))
	for _, p := range m.pairs {
		raw, err := indepEncode(p.key)
		if err != nil {
			return err
		}
		if major := raw[0] & 0xe0; major > 3<<5 {
			return &UnsupportedTypeError{}
		}
		all = append(all, encoded{raw: raw, p: p})
	}
	sort.Slice(all, func(i, j int) bool {
		if len(all[i].raw) != len(all[j].raw) {
			return len(all[i].raw) < len(all[j].raw)
		}
		for k := range all[i].raw {
			if all[i].raw[k] != all[j].raw[k] {
				return all[i].raw[k] < all[j].raw[k]
			}
		}
		return false
	})
	buf.head(5, uint64(len(all)))
	seen := map[string]bool{}
	for _, e := range all {
		if seen[string(e.raw)] {
			return ErrDuplicateKey
		}
		seen[string(e.raw)] = true
		buf.b = append(buf.b, e.raw...)
		if err := indepWrite(buf, e.p.value); err != nil {
			return err
		}
	}
	return nil
}
