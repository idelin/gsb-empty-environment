// Package baselinepatch implements a length-prefixed binary differential
// encoding over an explicit, self-defined subset of JSON-like documents.
//
// A Patch references the SHA-256 canonical digest of its baseline document.
// Apply verifies the baseline digest before touching any data, validates every
// operation first, and only then performs the edits on a deep copy, so a
// rejected patch leaves the caller's document untouched (atomic application).
//
// Decoding never invokes host code: there are no type tags, interfaces beyond
// plain JSON values, functions, pointers or plugins. All length prefixes are
// validated against the remaining buffer and a small set of allocation caps
// before any slice or string is allocated.
package baselinepatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
)

// Supported serialization range:
//
//   - nil            (JSON null)
//   - bool
//   - float64        (finite only: NaN and ±Inf are rejected as non-portable)
//   - string         (arbitrary rune content; Go strings are valid UTF-8)
//   - []any          (array)
//   - map[string]any (object; keys canonically sorted on encode)
//
// Integer Go types, json.Number, structs, pointers, functions, channels and
// every other kind are rejected rather than coerced, so encoding behavior is
// exactly the documented subset and decoding can never construct or execute
// host-level types.

// Type tags of the canonical value encoding.
const (
	tagNull   = 0
	tagBool   = 1
	tagNumber = 2
	tagString = 3
	tagArray  = 4
	tagObject = 5
)

// Allocation caps checked before allocating, in addition to exact buffer
// bounds. They reject untrusted length prefixes before memory is touched.
const (
	maxStringBytes  = 64 << 20 // 64 MiB per string
	maxBinaryBytes  = 64 << 20 // 64 MiB length prefix cap for strings/blobs
	maxArrayMembers = 1 << 22  // ~4.19M members
	maxObjectKeys   = 1 << 22
	maxPathSegments = 4096
	maxOps          = 1 << 20
	// maxLCSCells bounds O(n*m) array differencing work; larger arrays are
	// replaced wholesale with a single splice instead of running full LCS.
	maxLCSCells = 1 << 24 // ~16.7M DP cells
)

// ErrUnsupportedValue is returned when a value lies outside the supported
// serialization range (e.g. int, json.Number, NaN, or a foreign map kind).
var ErrUnsupportedValue = errors.New("baselinepatch: unsupported value type")

// canonicalEncode writes v to a self-delimited canonical binary form.
// Container lengths are validated before allocation on decode; on encode the
// values already exist in memory, but their counts are range-checked too.
func canonicalEncode(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteByte(tagNull)
	case bool:
		buf.WriteByte(tagBool)
		if x {
			buf.WriteByte(1)
		} else {
			buf.WriteByte(0)
		}
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Errorf("%w: non-finite number", ErrUnsupportedValue)
		}
		buf.WriteByte(tagNumber)
		var bits [8]byte
		binary.BigEndian.PutUint64(bits[:], math.Float64bits(x))
		buf.Write(bits[:])
	case string:
		if len(x) > maxStringBytes {
			return fmt.Errorf("%w: string exceeds %d bytes", ErrUnsupportedValue, maxStringBytes)
		}
		buf.WriteByte(tagString)
		writeUvarint(buf, uint64(len(x)))
		buf.WriteString(x)
	case []any:
		if len(x) > maxArrayMembers {
			return fmt.Errorf("%w: array exceeds %d members", ErrUnsupportedValue, maxArrayMembers)
		}
		buf.WriteByte(tagArray)
		writeUvarint(buf, uint64(len(x)))
		for _, member := range x {
			if err := canonicalEncode(buf, member); err != nil {
				return err
			}
		}
	case map[string]any:
		if len(x) > maxObjectKeys {
			return fmt.Errorf("%w: object exceeds %d keys", ErrUnsupportedValue, maxObjectKeys)
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte(tagObject)
		writeUvarint(buf, uint64(len(keys)))
		for _, k := range keys {
			writeUvarint(buf, uint64(len(k)))
			buf.WriteString(k)
			if err := canonicalEncode(buf, x[k]); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedValue, v)
	}
	return nil
}

func writeUvarint(buf *bytes.Buffer, n uint64) {
	var tmp [binary.MaxVarintLen64]byte
	m := binary.PutUvarint(tmp[:], n)
	buf.Write(tmp[:m])
}

// canonicalBytes returns the canonical serialization of v.
func canonicalBytes(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := canonicalEncode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Digest is the SHA-256 of the canonical encoding of v.
//
// The digest is an identity statement over the exact supported value:
// different float bits (e.g. -0.0 vs +0.0) differ, object key order does not,
// and a deleted key differs from a key explicitly set to JSON null.
func Digest(v any) ([32]byte, error) {
	b, err := canonicalBytes(v)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(b), nil
}

// equalValues reports canonical identity of two supported values.
func equalValues(a, b any) (bool, error) {
	ab, err := canonicalBytes(a)
	if err != nil {
		return false, err
	}
	bb, err := canonicalBytes(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ab, bb), nil
}

// cloneValue deep-copies a supported value. Every encountered Go type is
// validated, so cloning foreign types fails closed instead of aliasing them.
func cloneValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, float64, string:
		return v, nil
	case []any:
		out := make([]any, len(x))
		for i, member := range x {
			c, err := cloneValue(member)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, member := range x {
			c, err := cloneValue(member)
			if err != nil {
				return nil, err
			}
			out[k] = c
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedValue, v)
	}
}

// validateValue clones, which simultaneously proves the whole tree is inside
// the supported serialization range.
func validateValue(v any) error {
	_, err := cloneValue(v)
	return err
}
