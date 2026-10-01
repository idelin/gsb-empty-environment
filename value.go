// Package canon implements a canonical (deterministic) binary serialization
// format for tree values and rooted directed graphs with shared references
// and cycles.
//
// The encoding is designed for digesting and signing use cases: equal
// logical values always produce identical bytes, independent of map
// insertion order, graph construction order, or traversal order. The
// package only produces canonical bytes (and a SHA-256 digest helper);
// it deliberately does not implement any signature algorithm.
//
// Supported value range (anything else is rejected):
//   - Null
//   - Bool
//   - Int: signed 64-bit integers, zigzag varint, minimal form
//   - Float: finite IEEE-754 float64 only; NaN and +/-Inf are rejected,
//     -0.0 is normalized to +0.0
//   - String: valid UTF-8 only
//   - Bytes: arbitrary byte strings (distinct type from String)
//   - Array: ordered list of values (order is significant and preserved)
//   - Object: string-keyed map; keys are sorted bytewise, duplicates rejected
//   - Rooted graph: see EncodeGraph / DecodeGraph
//
// Distinct types always have distinct encodings: Int(1) != String("1").
//
// Decoding never executes host code (the format is pure data, no type
// names, no constructors) and validates every length prefix against the
// remaining input before allocating.
package canon

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"unicode/utf8"
)

// Type tags. Every value starts with exactly one tag byte.
const (
	tagNull   byte = 0x00
	tagFalse  byte = 0x01
	tagTrue   byte = 0x02
	tagInt    byte = 0x03
	tagFloat  byte = 0x04
	tagString byte = 0x05
	tagBytes  byte = 0x06
	tagArray  byte = 0x07
	tagObject byte = 0x08
	tagGraph  byte = 0x09
)

// ErrNonFiniteFloat is returned when encoding a NaN or +/-Inf float.
var ErrNonFiniteFloat = errors.New("canon: non-finite float has no canonical encoding")

// Value is a canonicalizable tree value. The set of implementations is
// sealed: Null, Bool, Int, Float, String, Bytes, Array, Object.
type Value interface{ canonValue() }

type Null struct{}
type Bool bool
type Int int64
type Float float64
type String string
type Bytes []byte
type Array []Value
type Object map[string]Value

func (Null) canonValue()   {}
func (Bool) canonValue()   {}
func (Int) canonValue()    {}
func (Float) canonValue()  {}
func (String) canonValue() {}
func (Bytes) canonValue()  {}
func (Array) canonValue()  {}
func (Object) canonValue() {}

// KV is a key/value pair used to build an Object with NewObject.
type KV struct {
	Key   string
	Value Value
}

// NewObject builds an Object from pairs in any order. Encoding sorts the
// keys, so different insertion orders yield identical bytes. Duplicate
// keys are rejected.
func NewObject(pairs ...KV) (Object, error) {
	obj := make(Object, len(pairs))
	for _, p := range pairs {
		if _, dup := obj[p.Key]; dup {
			return nil, fmt.Errorf("canon: duplicate object key %q", p.Key)
		}
		obj[p.Key] = p.Value
	}
	return obj, nil
}

// Encode returns the canonical encoding of v.
func Encode(v Value) ([]byte, error) {
	return appendValue(nil, v)
}

// Digest returns the SHA-256 digest of the canonical encoding of v.
// It is intended as input to external signature schemes; this package
// does not implement signing itself.
func Digest(v Value) ([32]byte, error) {
	enc, err := Encode(v)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(enc), nil
}

func appendValue(buf []byte, v Value) ([]byte, error) {
	switch t := v.(type) {
	case Null:
		return append(buf, tagNull), nil
	case Bool:
		if t {
			return append(buf, tagTrue), nil
		}
		return append(buf, tagFalse), nil
	case Int:
		buf = append(buf, tagInt)
		// zigzag varint, minimal by construction
		u := uint64(t)<<1 ^ uint64(t>>63)
		return binary.AppendUvarint(buf, u), nil
	case Float:
		f := float64(t)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, ErrNonFiniteFloat
		}
		if f == 0 {
			f = 0 // normalize -0.0 to +0.0
		}
		buf = append(buf, tagFloat)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], math.Float64bits(f))
		return append(buf, b[:]...), nil
	case String:
		if !utf8.ValidString(string(t)) {
			return nil, fmt.Errorf("canon: invalid UTF-8 in string")
		}
		buf = append(buf, tagString)
		buf = binary.AppendUvarint(buf, uint64(len(t)))
		return append(buf, t...), nil
	case Bytes:
		buf = append(buf, tagBytes)
		buf = binary.AppendUvarint(buf, uint64(len(t)))
		return append(buf, t...), nil
	case Array:
		buf = append(buf, tagArray)
		buf = binary.AppendUvarint(buf, uint64(len(t)))
		var err error
		for _, e := range t {
			buf, err = appendValue(buf, e)
			if err != nil {
				return nil, err
			}
		}
		return buf, nil
	case Object:
		keys := make([]string, 0, len(t))
		for k := range t {
			if !utf8.ValidString(k) {
				return nil, fmt.Errorf("canon: invalid UTF-8 in object key %q", k)
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf = append(buf, tagObject)
		buf = binary.AppendUvarint(buf, uint64(len(keys)))
		var err error
		for _, k := range keys {
			buf = append(buf, tagString)
			buf = binary.AppendUvarint(buf, uint64(len(k)))
			buf = append(buf, k...)
			buf, err = appendValue(buf, t[k])
			if err != nil {
				return nil, err
			}
		}
		return buf, nil
	default:
		return nil, fmt.Errorf("canon: unsupported value type %T", v)
	}
}
