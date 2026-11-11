// Package canon implements the canonical deterministic encoding used for
// device message signatures.
//
// The wire format is the Deterministic Encoding Rules subset of CBOR (RFC 8949).
// Only five value kinds are representable: integers, byte strings, UTF-8 text,
// arrays and maps. For every logical value there is exactly one legal encoding:
//
//   - integers and all container/string lengths use the shortest possible head;
//   - map entries are sorted by the length of the encoded key and then by the
//     key bytes, so keys with identical encodings are duplicates and rejected;
//   - indefinite-length items, tags, floats, simple values and booleans are not
//     part of the grammar and are never produced.
//
// Decoding applies the same rules strictly, enforces a resource budget while
// parsing and finally re-encodes the decoded value to prove that the input is
// the unique accepted encoding of that value.
package canon

import (
	"encoding/binary"
	"math"
	"reflect"
	"sort"
	"unicode/utf8"
)

const (
	majorUint   byte = 0 << 5
	majorNegInt byte = 1 << 5
	majorBytes  byte = 2 << 5
	majorText   byte = 3 << 5
	majorArray  byte = 4 << 5
	majorMap    byte = 5 << 5
	majorTag    byte = 6 << 5
	majorSimple byte = 7 << 5
	maxDepth         = 4096
)

// Map is an ordered, canonical key/value container. Decode always returns maps
// as Map so that callers preserve the verified entry order.
type Map struct {
	entries []MapEntry
}

// MapEntry is a single key/value pair of a Map.
type MapEntry struct {
	Key   any
	Value any
}

// NewMap builds a Map from the given entries. Duplicate keys are not checked
// here; they surface when the Map is encoded.
func NewMap(entries ...MapEntry) Map {
	copied := make([]MapEntry, len(entries))
	copy(copied, entries)
	return Map{entries: copied}
}

// Len returns the number of entries.
func (m Map) Len() int { return len(m.entries) }

// Entry returns the entry at index i.
func (m Map) Entry(i int) MapEntry { return m.entries[i] }

// Get looks up a value by key. The ok result reports whether the key exists.
func (m Map) Get(key any) (value any, ok bool) {
	want, err := encodeKey(key, 0)
	if err != nil {
		return nil, false
	}
	for i := range m.entries {
		got, err := encodeKey(m.entries[i].Key, 0)
		if err != nil {
			continue
		}
		if bytesEqual(got, want) {
			return m.entries[i].Value, true
		}
	}
	return nil, false
}

// AppendEncode appends the canonical encoding of value to dst and returns the
// new slice. See Encode for the supported value types.
func AppendEncode(dst []byte, value any) ([]byte, error) {
	return appendEncode(dst, value, 0)
}

// Encode returns the unique canonical encoding of value.
//
// Supported values are int, int8/16/32/64, uint, uint8/16/32/64, string,
// []byte, []any, map[K]V (integer, string or []byte keys) and canon.Map.
// Types outside this grammar, non-UTF-8 strings and duplicate map keys are
// rejected.
func Encode(value any) ([]byte, error) {
	return appendEncode(nil, value, 0)
}

func appendEncode(dst []byte, value any, depth int) ([]byte, error) {
	if depth > maxDepth {
		return nil, ErrDepthExceeded
	}
	switch v := value.(type) {
	case Map:
		return appendMap(dst, v, depth)
	case int:
		return appendInt(dst, int64(v)), nil
	case int8:
		return appendInt(dst, int64(v)), nil
	case int16:
		return appendInt(dst, int64(v)), nil
	case int32:
		return appendInt(dst, int64(v)), nil
	case int64:
		return appendInt(dst, v), nil
	case uint:
		return appendUint(dst, majorUint, uint64(v)), nil
	case uint8:
		return appendUint(dst, majorUint, uint64(v)), nil
	case uint16:
		return appendUint(dst, majorUint, uint64(v)), nil
	case uint32:
		return appendUint(dst, majorUint, uint64(v)), nil
	case uint64:
		return appendUint(dst, majorUint, v), nil
	case string:
		if !isValidUTF8(v) {
			return nil, ErrInvalidUTF8
		}
		dst = writeHead(dst, majorText, uint64(len(v)))
		return append(dst, v...), nil
	case []byte:
		dst = writeHead(dst, majorBytes, uint64(len(v)))
		return append(dst, v...), nil
	case []any:
		dst = writeHead(dst, majorArray, uint64(len(v)))
		for _, item := range v {
			var err error
			dst, err = appendEncode(dst, item, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return dst, nil
	case nil:
		return nil, &UnsupportedTypeError{Type: reflect.TypeOf(value)}
	default:
		return appendReflect(dst, value, depth)
	}
}

func appendInt(dst []byte, v int64) []byte {
	if v >= 0 {
		return writeHead(dst, majorUint, uint64(v))
	}
	return writeHead(dst, majorNegInt, uint64(-1-v))
}

func appendUint(dst []byte, major byte, v uint64) []byte {
	return writeHead(dst, major, v)
}

// writeHead emits a CBOR head (major type plus argument) using the shortest
// representation allowed by the deterministic rules.
func writeHead(dst []byte, major byte, arg uint64) []byte {
	switch {
	case arg <= 23:
		return append(dst, major|byte(arg))
	case arg <= math.MaxUint8:
		return append(dst, major|24, byte(arg))
	case arg <= math.MaxUint16:
		dst = append(dst, major|25, 0, 0)
		binary.BigEndian.PutUint16(dst[len(dst)-2:], uint16(arg))
		return dst
	case arg <= math.MaxUint32:
		dst = append(dst, major|26, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(dst[len(dst)-4:], uint32(arg))
		return dst
	default:
		dst = append(dst, major|27, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(dst[len(dst)-8:], arg)
		return dst
	}
}

func appendMap(dst []byte, m Map, depth int) ([]byte, error) {
	type pair struct {
		key []byte
		raw MapEntry
	}
	pairs := make([]pair, 0, len(m.entries))
	for _, entry := range m.entries {
		key, err := encodeKey(entry.Key, depth)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, pair{key: key, raw: entry})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return compareKeys(pairs[i].key, pairs[j].key) < 0
	})
	for i := 1; i < len(pairs); i++ {
		if bytesEqual(pairs[i-1].key, pairs[i].key) {
			return nil, ErrDuplicateKey
		}
	}
	dst = writeHead(dst, majorMap, uint64(len(pairs)))
	for _, p := range pairs {
		dst = append(dst, p.key...)
		var err error
		dst, err = appendEncode(dst, p.raw.Value, depth+1)
		if err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func appendReflect(dst []byte, value any, depth int) ([]byte, error) {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			raw := rv.Bytes()
			dst = writeHead(dst, majorBytes, uint64(len(raw)))
			return append(dst, raw...), nil
		}
		dst = writeHead(dst, majorArray, uint64(rv.Len()))
		for i := 0; i < rv.Len(); i++ {
			var err error
			dst, err = appendEncode(dst, rv.Index(i).Interface(), depth+1)
			if err != nil {
				return nil, err
			}
		}
		return dst, nil
	case reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			dst = writeHead(dst, majorBytes, uint64(rv.Len()))
			var raw []byte
			for i := 0; i < rv.Len(); i++ {
				raw = append(raw, byte(rv.Index(i).Uint()))
			}
			return append(dst, raw...), nil
		}
		dst = writeHead(dst, majorArray, uint64(rv.Len()))
		for i := 0; i < rv.Len(); i++ {
			var err error
			dst, err = appendEncode(dst, rv.Index(i).Interface(), depth+1)
			if err != nil {
				return nil, err
			}
		}
		return dst, nil
	case reflect.Map:
		return appendReflectMap(dst, rv, depth)
	default:
		return nil, &UnsupportedTypeError{Type: rv.Type()}
	}
}

func appendReflectMap(dst []byte, rv reflect.Value, depth int) ([]byte, error) {
	type pair struct {
		key []byte
		k   reflect.Value
		v   reflect.Value
	}
	pairs := make([]pair, 0, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		key, err := encodeKey(iter.Key().Interface(), depth)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, pair{key: key, k: iter.Key(), v: iter.Value()})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return compareKeys(pairs[i].key, pairs[j].key) < 0
	})
	for i := 1; i < len(pairs); i++ {
		if bytesEqual(pairs[i-1].key, pairs[i].key) {
			return nil, ErrDuplicateKey
		}
	}
	dst = writeHead(dst, majorMap, uint64(len(pairs)))
	for _, p := range pairs {
		dst = append(dst, p.key...)
		var err error
		dst, err = appendEncode(dst, p.v.Interface(), depth+1)
		if err != nil {
			return nil, err
		}
	}
	return dst, nil
}

// encodeKey accepts exactly the scalar key grammar: integers, text and byte
// strings. Its output ordering (shorter encoding first, then byte order) is the
// required map key order.
func encodeKey(key any, depth int) ([]byte, error) {
	encoded, err := appendEncode(nil, key, depth)
	if err != nil {
		return nil, err
	}
	major := encoded[0] & 0xe0
	switch major {
	case majorUint, majorNegInt, majorBytes, majorText:
		return encoded, nil
	default:
		return nil, &UnsupportedTypeError{Type: reflect.TypeOf(key)}
	}
}

// compareKeys orders encoded map keys by encoded length first and then by
// lexicographic bytes, matching the deterministic map ordering requirement.
func compareKeys(a, b []byte) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isValidUTF8(s string) bool {
	return utf8.ValidString(s)
}
