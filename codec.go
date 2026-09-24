package enumkit

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Supported serialization range. Decoders must reject anything outside
// these bounds before allocating.
const (
	// MaxID is the largest supported stable ID.
	MaxID = 1<<31 - 1
	// MaxTokenLen is the largest supported lexical token in bytes.
	MaxTokenLen = 256
)

// Wire tags for the binary format.
const (
	tagID  = 0x01 // known value: uvarint stable ID follows
	tagRaw = 0x02 // passthrough value: uvarint length + raw bytes follow
)

var (
	ErrTruncated    = errors.New("enumkit: truncated input")
	ErrBadTag       = errors.New("enumkit: unknown wire tag")
	ErrRange        = errors.New("enumkit: value out of supported range")
	ErrTokenTooLong = errors.New("enumkit: token exceeds MaxTokenLen")
)

// MarshalBinary encodes a value. Known values encode as their stable
// numeric ID, so renaming a member never changes serialization
// identity. Unknown values encode as their original lexical token.
//
// The decoder is a pure byte parser: it never executes host code, uses
// no reflection and no gob, and validates every declared length against
// the remaining buffer before allocating.
func (v Value) MarshalBinary() ([]byte, error) {
	if v.Known() {
		if v.ID < 0 || v.ID > MaxID {
			return nil, fmt.Errorf("%w: id %d not in [0,%d]", ErrRange, v.ID, MaxID)
		}
		buf := []byte{tagID}
		return binary.AppendUvarint(buf, uint64(v.ID)), nil
	}
	if len(v.Raw) > MaxTokenLen {
		return nil, fmt.Errorf("%w: %d > %d", ErrTokenTooLong, len(v.Raw), MaxTokenLen)
	}
	buf := []byte{tagRaw}
	buf = binary.AppendUvarint(buf, uint64(len(v.Raw)))
	return append(buf, v.Raw...), nil
}

// UnmarshalValue decodes one value. Length prefixes are validated
// against both MaxTokenLen and the actual remaining bytes before any
// allocation happens.
func UnmarshalValue(data []byte) (Value, error) {
	if len(data) == 0 {
		return Value{}, ErrTruncated
	}
	switch data[0] {
	case tagID:
		id, n := binary.Uvarint(data[1:])
		if n <= 0 {
			return Value{}, ErrTruncated
		}
		if id > MaxID {
			return Value{}, fmt.Errorf("%w: id %d exceeds %d", ErrRange, id, MaxID)
		}
		return Value{Kind: KnownNumber, ID: int64(id), Raw: strconv.FormatInt(int64(id), 10)}, nil
	case tagRaw:
		ln, n := binary.Uvarint(data[1:])
		if n <= 0 {
			return Value{}, ErrTruncated
		}
		if ln > MaxTokenLen {
			return Value{}, fmt.Errorf("%w: declared %d > %d", ErrTokenTooLong, ln, MaxTokenLen)
		}
		rest := data[1+n:]
		if uint64(len(rest)) < ln {
			return Value{}, ErrTruncated
		}
		// Allocation happens only after both bounds checks above.
		return Value{Kind: UnknownName, Raw: string(rest[:ln])}, nil
	default:
		return Value{}, fmt.Errorf("%w: 0x%02x", ErrBadTag, data[0])
	}
}

// MarshalJSON emits the value's lexical token as a JSON string.
func (v Value) MarshalJSON() ([]byte, error) {
	if v.Known() {
		return json.Marshal(strconv.FormatInt(v.ID, 10))
	}
	return json.Marshal(v.Raw)
}

// UnmarshalJSON accepts a JSON string or number and stores it as an
// unresolved lexical token; resolve it with Registry.Decode or
// Graph.Migrate. It executes no host code and rejects oversized input.
func (v *Value) UnmarshalJSON(data []byte) error {
	if len(data) > MaxTokenLen+2 {
		return ErrTokenTooLong
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*v = Value{Kind: UnknownName, Raw: s}
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("enumkit: enum value must be string or number: %w", err)
	}
	if id, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
		*v = Value{Kind: UnknownNumber, ID: id, Raw: n.String()}
		return nil
	}
	return fmt.Errorf("enumkit: invalid numeric enum token %q", n.String())
}
