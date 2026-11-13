package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"
)

// This file implements a minimal CBOR (RFC 8949) decoder plus a canonical
// (deterministic) re-encoder. The verifier accepts any well-formed CBOR on
// decode, but Canonicalize re-encodes using deterministic encoding rules:
// definite lengths only, shortest-form integers and lengths, and map keys
// sorted length-first then bytewise. Comparing the re-encoded output with
// the original bytes detects any non-canonical encoding.

var (
	errTrailingData   = errors.New("cbor: trailing data after item")
	errUnexpectedEOF  = errors.New("cbor: unexpected end of data")
	errIndefinite     = errors.New("cbor: indefinite-length items are not canonical")
	errNonMinimal     = errors.New("cbor: non-minimal length or integer encoding")
	errDuplicateKey   = errors.New("cbor: duplicate map key")
	errUnsortedKeys   = errors.New("cbor: map keys not in canonical order")
	errUnsupportedVal = errors.New("cbor: unsupported value type")
)

// MapEntry is a single key/value pair of a decoded CBOR map.
type MapEntry struct {
	Key   any
	Value any
}

// Map preserves the decoded order of a CBOR map so canonical ordering can be
// checked and duplicate keys detected.
type Map []MapEntry

// decodeItem decodes one CBOR data item from b and returns the value and the
// remaining bytes. Decoding is lenient about canonical form; canonicality is
// enforced by Canonicalize via re-encoding and byte comparison.
func decodeItem(b []byte) (any, []byte, error) {
	if len(b) == 0 {
		return nil, nil, errUnexpectedEOF
	}
	ib := b[0]
	major := ib >> 5
	info := ib & 0x1f
	rest := b[1:]

	if info == 31 {
		return nil, nil, errIndefinite
	}

	arg := func() (uint64, []byte, error) {
		return readArgument(info, rest)
	}

	switch major {
	case 0: // unsigned integer
		v, r, err := arg()
		return v, r, err
	case 1: // negative integer
		v, r, err := arg()
		if err != nil {
			return nil, nil, err
		}
		if v > math.MaxInt64-1 {
			return nil, nil, errors.New("cbor: negative integer overflow")
		}
		return int64(-1) - int64(v), r, nil
	case 2: // byte string
		n, r, err := arg()
		if err != nil {
			return nil, nil, err
		}
		if n > uint64(len(r)) {
			return nil, nil, errUnexpectedEOF
		}
		out := make([]byte, n)
		copy(out, r[:n])
		return out, r[n:], nil
	case 3: // text string
		n, r, err := arg()
		if err != nil {
			return nil, nil, err
		}
		if n > uint64(len(r)) {
			return nil, nil, errUnexpectedEOF
		}
		return string(r[:n]), r[n:], nil
	case 4: // array
		n, r, err := arg()
		if err != nil {
			return nil, nil, err
		}
		if n > uint64(len(r)) {
			return nil, nil, errUnexpectedEOF
		}
		arr := make([]any, 0, n)
		for i := uint64(0); i < n; i++ {
			var v any
			v, r, err = decodeItem(r)
			if err != nil {
				return nil, nil, err
			}
			arr = append(arr, v)
		}
		return arr, r, nil
	case 5: // map
		n, r, err := arg()
		if err != nil {
			return nil, nil, err
		}
		if n > uint64(len(r))/2 {
			return nil, nil, errUnexpectedEOF
		}
		m := make(Map, 0, n)
		for i := uint64(0); i < n; i++ {
			var k, v any
			k, r, err = decodeItem(r)
			if err != nil {
				return nil, nil, err
			}
			v, r, err = decodeItem(r)
			if err != nil {
				return nil, nil, err
			}
			m = append(m, MapEntry{Key: k, Value: v})
		}
		return m, r, nil
	case 7: // simple values and floats
		switch {
		case info == 20:
			return false, rest, nil
		case info == 21:
			return true, rest, nil
		case info == 22, info == 23:
			return nil, rest, nil
		case info == 25: // float16
			if len(rest) < 2 {
				return nil, nil, errUnexpectedEOF
			}
			bits := uint16(rest[0])<<8 | uint16(rest[1])
			return float16To64(bits), rest[2:], nil
		case info == 26: // float32
			if len(rest) < 4 {
				return nil, nil, errUnexpectedEOF
			}
			bits := uint32(rest[0])<<24 | uint32(rest[1])<<16 | uint32(rest[2])<<8 | uint32(rest[3])
			return float64(math.Float32frombits(bits)), rest[4:], nil
		case info == 27: // float64
			if len(rest) < 8 {
				return nil, nil, errUnexpectedEOF
			}
			var bits uint64
			for i := 0; i < 8; i++ {
				bits = bits<<8 | uint64(rest[i])
			}
			return math.Float64frombits(bits), rest[8:], nil
		}
		return nil, nil, fmt.Errorf("cbor: unsupported simple value %d", info)
	}
	return nil, nil, fmt.Errorf("cbor: unsupported major type %d", major)
}

// readArgument reads the argument of an initial byte per its additional
// information, rejecting non-minimal (longer than necessary) encodings.
func readArgument(info byte, b []byte) (uint64, []byte, error) {
	switch {
	case info < 24:
		return uint64(info), b, nil
	case info == 24:
		if len(b) < 1 {
			return 0, nil, errUnexpectedEOF
		}
		if b[0] < 24 {
			return 0, nil, errNonMinimal
		}
		return uint64(b[0]), b[1:], nil
	case info == 25:
		if len(b) < 2 {
			return 0, nil, errUnexpectedEOF
		}
		v := uint64(b[0])<<8 | uint64(b[1])
		if v <= math.MaxUint8 {
			return 0, nil, errNonMinimal
		}
		return v, b[2:], nil
	case info == 26:
		if len(b) < 4 {
			return 0, nil, errUnexpectedEOF
		}
		v := uint64(b[0])<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
		if v <= math.MaxUint16 {
			return 0, nil, errNonMinimal
		}
		return v, b[4:], nil
	case info == 27:
		if len(b) < 8 {
			return 0, nil, errUnexpectedEOF
		}
		var v uint64
		for i := 0; i < 8; i++ {
			v = v<<8 | uint64(b[i])
		}
		if v <= math.MaxUint32 {
			return 0, nil, errNonMinimal
		}
		return v, b[8:], nil
	}
	return 0, nil, fmt.Errorf("cbor: invalid additional information %d", info)
}

// encodeCanonical appends the deterministic (canonical) encoding of v to buf.
func encodeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case uint64:
		encodeHead(buf, 0, t)
	case int64:
		if t >= 0 {
			encodeHead(buf, 0, uint64(t))
		} else {
			encodeHead(buf, 1, uint64(-1-t))
		}
	case []byte:
		encodeHead(buf, 2, uint64(len(t)))
		buf.Write(t)
	case string:
		encodeHead(buf, 3, uint64(len(t)))
		buf.WriteString(t)
	case []any:
		encodeHead(buf, 4, uint64(len(t)))
		for _, e := range t {
			if err := encodeCanonical(buf, e); err != nil {
				return err
			}
		}
	case Map:
		entries := make([]MapEntry, len(t))
		copy(entries, t)
		sort.SliceStable(entries, func(i, j int) bool {
			ki, _ := canonicalKeyBytes(entries[i].Key)
			kj, _ := canonicalKeyBytes(entries[j].Key)
			return canonicalKeyLess(ki, kj)
		})
		encodeHead(buf, 5, uint64(len(entries)))
		for _, e := range entries {
			if err := encodeCanonical(buf, e.Key); err != nil {
				return err
			}
			if err := encodeCanonical(buf, e.Value); err != nil {
				return err
			}
		}
	case bool:
		if t {
			buf.WriteByte(0xf5)
		} else {
			buf.WriteByte(0xf4)
		}
	case nil:
		buf.WriteByte(0xf6)
	case float64:
		buf.WriteByte(0xfb)
		var b [8]byte
		bits := math.Float64bits(t)
		for i := 0; i < 8; i++ {
			b[i] = byte(bits >> (56 - 8*i))
		}
		buf.Write(b[:])
	default:
		return errUnsupportedVal
	}
	return nil
}

// encodeHead writes a major-type head with the shortest-form argument.
func encodeHead(buf *bytes.Buffer, major byte, arg uint64) {
	switch {
	case arg < 24:
		buf.WriteByte(major<<5 | byte(arg))
	case arg <= math.MaxUint8:
		buf.WriteByte(major<<5 | 24)
		buf.WriteByte(byte(arg))
	case arg <= math.MaxUint16:
		buf.WriteByte(major<<5 | 25)
		buf.WriteByte(byte(arg >> 8))
		buf.WriteByte(byte(arg))
	case arg <= math.MaxUint32:
		buf.WriteByte(major<<5 | 26)
		for i := 3; i >= 0; i-- {
			buf.WriteByte(byte(arg >> (8 * i)))
		}
	default:
		buf.WriteByte(major<<5 | 27)
		for i := 7; i >= 0; i-- {
			buf.WriteByte(byte(arg >> (8 * i)))
		}
	}
}

func canonicalKeyBytes(k any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeCanonical(&buf, k); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// canonicalKeyLess orders keys per RFC 8949 deterministic encoding: shorter
// encodings first, then bytewise lexicographic order.
func canonicalKeyLess(a, b []byte) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return bytes.Compare(a, b) < 0
}

// Canonicalize decodes exactly one CBOR item from b and returns its canonical
// re-encoding. It rejects trailing data, indefinite-length items, non-minimal
// arguments, duplicate map keys, and non-canonically-ordered map keys.
func Canonicalize(b []byte) ([]byte, error) {
	v, rest, err := decodeItem(b)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errTrailingData
	}
	if err := checkNestedMaps(v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := encodeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// checkNestedMaps validates map key uniqueness and canonical ordering for
// maps nested anywhere inside v.
func checkNestedMaps(v any) error {
	switch t := v.(type) {
	case Map:
		if err := checkMapCanonical(t); err != nil {
			return err
		}
	case []any:
		for _, e := range t {
			if err := checkNestedMaps(e); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkMapCanonical(m Map) error {
	var prev []byte
	for i, e := range m {
		kb, err := canonicalKeyBytes(e.Key)
		if err != nil {
			return err
		}
		if i > 0 {
			if bytes.Equal(prev, kb) {
				return errDuplicateKey
			}
			if !canonicalKeyLess(prev, kb) {
				return errUnsortedKeys
			}
		}
		prev = kb
		if err := checkNestedMaps(e.Value); err != nil {
			return err
		}
	}
	return nil
}

func float16To64(bits uint16) float64 {
	sign := uint64(bits>>15) & 1
	exp := uint64(bits>>10) & 0x1f
	frac := uint64(bits) & 0x3ff
	if exp == 0 {
		f := float64(frac) * math.Ldexp(1, -24)
		if sign == 1 {
			f = -f
		}
		return f
	}
	var out uint64
	if exp == 0x1f {
		if frac == 0 {
			out = sign<<63 | 0x7ff<<52
		} else {
			out = sign<<63 | 0x7ff<<52 | frac<<42 | 1 // quiet NaN
		}
	} else {
		out = sign<<63 | (exp-15+1023)<<52 | frac<<42
	}
	return math.Float64frombits(out)
}
