// Package netsec decodes binary messages written with the
// StandardMessageCodec wire format used across the plugin/isolate boundary.
//
// The decoder is hardened against hostile input: declared lengths are never
// trusted for allocation, nesting and total-element budgets are enforced,
// duplicate map keys, non-zero alignment padding, unknown type bytes and
// trailing bytes are all rejected.
package netsec

import (
	"encoding/binary"
	"errors"
	"math"
	"reflect"
)

// Wire type bytes understood by the decoder.
const (
	typeNull      = 0
	typeTrue      = 1
	typeFalse     = 2
	typeInt32     = 3
	typeInt64     = 4
	typeFloat64   = 5
	typeString    = 7
	typeUint8List = 8
	typeList      = 12
	typeMap       = 13
)

const (
	// maxDepth is the maximum allowed container nesting. The root container is
	// at depth 1, so a message nested 17 levels deep is rejected.
	maxDepth = 16
	// maxElements bounds the total number of values decoded from one message.
	// Every scalar, container entry and uint8-list byte counts toward it.
	maxElements = 100_000
	// maxPrefix is the largest length that may appear in a size prefix. It
	// keeps prefix arithmetic and allocations bounded on 32-bit hosts too.
	maxPrefix = math.MaxInt32
)

var (
	// ErrTruncated is returned when the message ends before a complete value
	// can be decoded, including declared lengths that exceed the remaining
	// bytes.
	ErrTruncated = errors.New("netsec: truncated message or declared length exceeds remaining bytes")
	// ErrDepth is returned when container nesting is deeper than maxDepth.
	ErrDepth = errors.New("netsec: nesting depth exceeds 16")
	// ErrTooMany is returned when a message contains more than maxElements
	// values or uint8-list bytes.
	ErrTooMany = errors.New("netsec: element count exceeds 100000")
	// ErrDuplicateKey is returned when a map contains a repeated key.
	ErrDuplicateKey = errors.New("netsec: duplicate map key")
	// ErrPadding is returned when 8-byte alignment padding contains a
	// non-zero byte.
	ErrPadding = errors.New("netsec: non-zero alignment padding")
	// ErrTrailing is returned when bytes remain after the root value.
	ErrTrailing = errors.New("netsec: trailing bytes after root value")
	// ErrUnknownType is returned when an unrecognised type byte is met.
	ErrUnknownType = errors.New("netsec: unknown type byte")
)

// Map is an ordered collection of decoded key/value pairs. It is used instead
// of a Go map because the wire format allows non-comparable keys (slices and
// nested containers) and preserves insertion order. Duplicate keys are rejected
// during decoding.
type Map struct {
	keys   []any
	values []any
}

// Len returns the number of key/value pairs.
func (m *Map) Len() int { return len(m.keys) }

// Get returns the value associated with key and whether it was present. Keys
// are compared with reflect.DeepEqual semantics (NaN equals NaN).
func (m *Map) Get(key any) (any, bool) {
	for i, existing := range m.keys {
		if keyEqual(existing, key) {
			return m.values[i], true
		}
	}
	return nil, false
}

// Keys returns a copy of the keys in insertion order.
func (m *Map) Keys() []any {
	out := make([]any, len(m.keys))
	copy(out, m.keys)
	return out
}

// Values returns a copy of the values in insertion order.
func (m *Map) Values() []any {
	out := make([]any, len(m.values))
	copy(out, m.values)
	return out
}

// Pair returns the key and value at position i. It panics if i is out of
// range.
func (m *Map) Pair(i int) (any, any) {
	return m.keys[i], m.values[i]
}

// decoder walks a single message without retaining any allocation sized by an
// attacker-controlled length.
type decoder struct {
	buf      []byte
	pos      int
	depth    int
	elements int
}

// Decode parses one StandardMessageCodec message and returns its root value.
// Possible concrete result types are nil, bool, int32, int64, float64,
// string, []byte, []any or *Map.
func Decode(data []byte) (any, error) {
	if len(data) == 0 {
		return nil, ErrTruncated
	}
	d := &decoder{buf: data}
	value, err := d.readValue()
	if err != nil {
		return nil, err
	}
	if d.pos != len(data) {
		return nil, ErrTrailing
	}
	return value, nil
}

func (d *decoder) readValue() (any, error) {
	tag, err := d.take(1)
	if err != nil {
		return nil, err
	}
	if err := d.tally(); err != nil {
		return nil, err
	}

	switch tag[0] {
	case typeNull:
		return nil, nil
	case typeTrue:
		return true, nil
	case typeFalse:
		return false, nil
	case typeInt32:
		raw, err := d.take(4)
		if err != nil {
			return nil, err
		}
		return int32(binary.LittleEndian.Uint32(raw)), nil
	case typeInt64:
		// The reference codec 8-byte aligns 64-bit scalars before their bytes.
		if err := d.align8(); err != nil {
			return nil, err
		}
		raw, err := d.take(8)
		if err != nil {
			return nil, err
		}
		return int64(binary.LittleEndian.Uint64(raw)), nil
	case typeFloat64:
		if err := d.align8(); err != nil {
			return nil, err
		}
		raw, err := d.take(8)
		if err != nil {
			return nil, err
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(raw)), nil
	case typeString:
		length, err := d.readSize()
		if err != nil {
			return nil, err
		}
		raw, err := d.take(length)
		if err != nil {
			return nil, err
		}
		// Copy so the result never aliases the caller's mutable input.
		return string(raw), nil
	case typeUint8List:
		length, err := d.readSize()
		if err != nil {
			return nil, err
		}
		// Each byte occupies one slot of the element budget.
		if err := d.tallyN(length); err != nil {
			return nil, err
		}
		raw, err := d.take(length)
		if err != nil {
			return nil, err
		}
		out := make([]byte, length)
		copy(out, raw)
		return out, nil
	case typeList:
		return d.readList()
	case typeMap:
		return d.readMap()
	default:
		return nil, ErrUnknownType
	}
}

func (d *decoder) readList() (any, error) {
	length, err := d.readSize()
	if err != nil {
		return nil, err
	}
	d.depth++
	if d.depth > maxDepth {
		return nil, ErrDepth
	}
	// Start with a nil slice: the declared length never sizes the backing
	// array, entries are appended only as they are actually decoded.
	var out []any
	for i := 0; i < length; i++ {
		entry, err := d.readValue()
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	d.depth--
	return out, nil
}

func (d *decoder) readMap() (any, error) {
	length, err := d.readSize()
	if err != nil {
		return nil, err
	}
	d.depth++
	if d.depth > maxDepth {
		return nil, ErrDepth
	}
	out := &Map{}
	for i := 0; i < length; i++ {
		key, err := d.readValue()
		if err != nil {
			return nil, err
		}
		value, err := d.readValue()
		if err != nil {
			return nil, err
		}
		if _, exists := out.Get(key); exists {
			return nil, ErrDuplicateKey
		}
		out.keys = append(out.keys, key)
		out.values = append(out.values, value)
	}
	d.depth--
	return out, nil
}

// readSize decodes the compact, little-endian length prefix:
// a byte below 254 is literal; 254 precedes a uint16; 255 precedes a uint32.
func (d *decoder) readSize() (int, error) {
	marker, err := d.take(1)
	if err != nil {
		return 0, err
	}
	switch marker[0] {
	case 254:
		raw, err := d.take(2)
		if err != nil {
			return 0, err
		}
		size := int(binary.LittleEndian.Uint16(raw))
		return size, nil
	case 255:
		raw, err := d.take(4)
		if err != nil {
			return 0, err
		}
		size := binary.LittleEndian.Uint32(raw)
		if uint64(size) > maxPrefix {
			return 0, ErrTruncated
		}
		return int(size), nil
	default:
		return int(marker[0]), nil
	}
}

// take returns the next n bytes without copying and advances the cursor. It
// fails with ErrTruncated if the buffer cannot satisfy the request, so no
// out-of-bounds indexing is ever reachable.
func (d *decoder) take(n int) ([]byte, error) {
	if n < 0 || d.pos > len(d.buf)-n {
		return nil, ErrTruncated
	}
	start := d.pos
	d.pos += n
	return d.buf[start:d.pos], nil
}

// align8 inserts zero padding until the cursor is 8-byte aligned relative to
// the start of the message. Any non-zero padding byte is an error.
func (d *decoder) align8() error {
	pad := (8 - d.pos%8) % 8
	if pad == 0 {
		return nil
	}
	raw, err := d.take(pad)
	if err != nil {
		return err
	}
	for _, b := range raw {
		if b != 0 {
			return ErrPadding
		}
	}
	return nil
}

// tally charges one decoded value against the element budget.
func (d *decoder) tally() error {
	return d.tallyN(1)
}

// tallyN charges n units against the element budget using 64-bit arithmetic to
// avoid overflow on pathological inputs.
func (d *decoder) tallyN(n int) error {
	if n < 0 {
		return ErrTruncated
	}
	total := uint64(d.elements) + uint64(n)
	if total > maxElements {
		return ErrTooMany
	}
	d.elements = int(total)
	return nil
}

// keyEqual compares map keys, treating float NaN values as equal so duplicate
// NaN keys cannot bypass the check, and supporting non-comparable keys.
func keyEqual(a, b any) bool {
	af, aIsFloat := a.(float64)
	bf, bIsFloat := b.(float64)
	if aIsFloat != bIsFloat {
		return reflect.DeepEqual(a, b)
	}
	if aIsFloat {
		return math.IsNaN(af) && math.IsNaN(bf) || af == bf
	}
	return reflect.DeepEqual(a, b)
}
