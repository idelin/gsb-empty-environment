package verenum

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// Supported serialization scope:
//
// One JSON token per enum field, either a JSON string (a name/alias) or a
// JSON integer (the stable ID). Arrays/objects are not enum scalars. Only the
// standard library is used, so decoding never executes host-supplied code
// (no plugins, no reflection-driven construction, no string eval).

const (
	// MaxNameLen bounds an accepted lexical name; checked on raw bytes
	// before any allocation for the decoded string.
	MaxNameLen = 256
	// MaxInputLen bounds a single scalar token; checked before decoding, so
	// an oversized input triggers no proportional allocation.
	MaxInputLen = 1 << 20
)

// Sentinel decode errors.
var (
	ErrInputTooLarge = fmt.Errorf("verenum: input exceeds %d bytes", MaxInputLen)
	ErrNameTooLong   = fmt.Errorf("verenum: name exceeds %d bytes", MaxNameLen)
	ErrEmptyInput    = fmt.Errorf("verenum: empty enum token")
	ErrBadToken      = fmt.Errorf("verenum: enum token must be a JSON string or integer")
)

// Value is a decoded enum value.
//
// Known is false for names/IDs absent from the target version's schema; such
// values are preserved verbatim (Raw for lexical strings, ID for numbers)
// and re-encoded byte-equivalently. Unknown numbers are never coerced to the
// first declared member.
type Value struct {
	ID    int64
	Known bool
	Raw   string // original lexical form of an unknown name
}

// Decode parses one enum token against version v.
//
// Length limits are validated against the raw buffer before json decoding or
// any string allocation.
func (r *Registry) Decode(v Version, data []byte) (Value, error) {
	// 1. validate length before allocation/decoding.
	if len(data) == 0 {
		return Value{}, ErrEmptyInput
	}
	if len(data) > MaxInputLen {
		return Value{}, ErrInputTooLarge
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return Value{}, ErrEmptyInput
	}
	switch trimmed[0] {
	case '"':
		// Account for the two quote bytes, then bound the allocation up front.
		if len(trimmed)-2 > MaxNameLen {
			return Value{}, ErrNameTooLong
		}
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return Value{}, fmt.Errorf("verenum: invalid JSON string: %w", err)
		}
		if len(name) > MaxNameLen {
			return Value{}, ErrNameTooLong
		}
		if id, ok := r.Lookup(v, name); ok {
			return Value{ID: id, Known: true}, nil
		}
		// Lexical passthrough: keep the original unknown name untouched.
		return Value{Known: false, Raw: name}, nil
	default:
		// json.Number avoids guessing float precision; reject non-integers.
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		var num json.Number
		if err := dec.Decode(&num); err != nil {
			return Value{}, fmt.Errorf("%w: %v", ErrBadToken, err)
		}
		id, err := strconv.ParseInt(string(num), 10, 64)
		if err != nil {
			return Value{}, fmt.Errorf("%w: non-integer number %q", ErrBadToken, num)
		}
		if _, ok := r.Canonical(v, id); ok {
			return Value{ID: id, Known: true}, nil
		}
		// Unknown numeric value: pass through as-is; never coerce.
		return Value{ID: id, Known: false}, nil
	}
}

// Encode emits the canonical name at version v for known values. Unknown
// values round-trip in their original lexical/numeric form.
func (r *Registry) Encode(v Version, val Value) ([]byte, error) {
	if val.Known {
		name, ok := r.Canonical(v, val.ID)
		if !ok {
			return nil, fmt.Errorf("verenum: stable id %d is not defined at v%d", val.ID, v)
		}
		out, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	if val.Raw != "" {
		return json.Marshal(val.Raw)
	}
	return []byte(strconv.FormatInt(val.ID, 10)), nil
}
