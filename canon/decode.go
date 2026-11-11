package canon

import (
	"encoding/binary"
	"math"
	"reflect"
	"unicode/utf8"
)

// Decode parses data using DefaultLimits. It returns the decoded value, where
// maps are represented by Map, integers by int64, byte strings by []byte, text
// by string and arrays by []any.
func Decode(data []byte) (any, error) {
	return DecodeWithLimits(data, DefaultLimits())
}

// DecodeWithLimits behaves like Decode but enforces the supplied resource
// budgets before the decoded object is returned.
func DecodeWithLimits(data []byte, limits Limits) (any, error) {
	if len(data) == 0 {
		return nil, ErrEmpty
	}
	if limits.MaxBytes != 0 && uint64(len(data)) > limits.MaxBytes {
		return nil, &BudgetError{Resource: "bytes", Limit: limits.MaxBytes}
	}
	d := &decoder{data: data, limits: limits}
	value, err := d.item(0)
	if err != nil {
		return nil, err
	}
	if d.off != len(d.data) {
		return nil, ErrTrailingData
	}
	// Defense in depth: the accepted wire form must be the exact unique
	// encoding of the decoded value. Any ordering or minimality mistake that
	// slipped past explicit checks fails here.
	reencoded, err := Encode(value)
	if err != nil {
		return nil, err
	}
	if !bytesEqual(reencoded, data) {
		return nil, ErrNonCanonical
	}
	return value, nil
}

type decoder struct {
	data   []byte
	off    int
	items  uint64
	limits Limits
}

// maxIntForPlatform is the largest value representable by an int. All
// supported targets are 64-bit; the constant keeps length conversions explicit.
const maxIntForPlatform = uint64(1<<63 - 1)

func (d *decoder) item(depth uint64) (any, error) {
	if d.limits.MaxDepth != 0 && depth > d.limits.MaxDepth {
		return nil, &BudgetError{Resource: "depth", Limit: d.limits.MaxDepth}
	}
	if depth > maxDepth {
		return nil, ErrDepthExceeded
	}
	d.items++
	if d.limits.MaxItems != 0 && d.items > d.limits.MaxItems {
		return nil, &BudgetError{Resource: "items", Limit: d.limits.MaxItems}
	}
	if d.off >= len(d.data) {
		return nil, ErrTruncated
	}

	head := d.data[d.off]
	d.off++
	major := head & 0xe0
	info := head & 0x1f

	arg, err := d.argument(info)
	if err != nil {
		return nil, err
	}

	switch major {
	case majorUint:
		// Values above the int64 ceiling stay uint64 so the full unsigned
		// integer domain round-trips without loss.
		if arg > math.MaxInt64 {
			return arg, nil
		}
		return int64(arg), nil

	case majorNegInt:
		if arg > math.MaxInt64 {
			return nil, ErrIntegerOverflow
		}
		return -1 - int64(arg), nil

	case majorBytes:
		fallthrough
	case majorText:
		if d.limits.MaxStringLen != 0 && arg > d.limits.MaxStringLen {
			return nil, &BudgetError{Resource: "string length", Limit: d.limits.MaxStringLen}
		}
		payload, err := d.take(arg)
		if err != nil {
			return nil, err
		}
		if major == majorText && !utf8.Valid(payload) {
			return nil, ErrInvalidUTF8
		}
		if major == majorBytes {
			cp := make([]byte, len(payload))
			copy(cp, payload)
			return cp, nil
		}
		return string(payload), nil

	case majorArray:
		if d.limits.MaxContainerLen != 0 && arg > d.limits.MaxContainerLen {
			return nil, &BudgetError{Resource: "array length", Limit: d.limits.MaxContainerLen}
		}
		elements := make([]any, 0, boundedCap(arg))
		for i := uint64(0); i < arg; i++ {
			element, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			elements = append(elements, element)
		}
		return elements, nil

	case majorMap:
		if d.limits.MaxContainerLen != 0 && arg > d.limits.MaxContainerLen {
			return nil, &BudgetError{Resource: "map length", Limit: d.limits.MaxContainerLen}
		}
		entries := make([]MapEntry, 0, boundedCap(arg))
		var previousKey []byte
		for i := uint64(0); i < arg; i++ {
			keyStart := d.off
			key, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			if !validKey(key) {
				return nil, &UnsupportedTypeError{Type: reflect.TypeOf(key)}
			}
			keyBytes := d.data[keyStart:d.off]
			if previousKey != nil {
				if cmp := compareKeys(previousKey, keyBytes); cmp > 0 {
					return nil, ErrKeyOrder
				} else if cmp == 0 {
					return nil, ErrDuplicateKey
				}
			}
			value, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			entries = append(entries, MapEntry{Key: key, Value: value})
			previousKey = keyBytes
		}
		return Map{entries: entries}, nil

	case majorTag:
		return nil, ErrUnsupportedValue

	default:
		// Major 7 covers booleans, null, undefined, floats and simple values.
		return nil, ErrUnsupportedValue
	}
}

// argument reads the head argument and enforces the shortest-encoding rule.
// Additional information 28-30 is reserved and 31 marks an indefinite head or
// a standalone break, none of which belong to the supported grammar.
func (d *decoder) argument(info byte) (uint64, error) {
	switch {
	case info <= 23:
		return uint64(info), nil
	case info == 24:
		if d.off >= len(d.data) {
			return 0, ErrTruncated
		}
		value := d.data[d.off]
		d.off++
		if value <= 23 {
			return 0, ErrNonCanonical
		}
		return uint64(value), nil
	case info == 25:
		if d.off+2 > len(d.data) {
			return 0, ErrTruncated
		}
		value := binary.BigEndian.Uint16(d.data[d.off:])
		d.off += 2
		if value <= math.MaxUint8 {
			return 0, ErrNonCanonical
		}
		return uint64(value), nil
	case info == 26:
		if d.off+4 > len(d.data) {
			return 0, ErrTruncated
		}
		value := binary.BigEndian.Uint32(d.data[d.off:])
		d.off += 4
		if value <= math.MaxUint16 {
			return 0, ErrNonCanonical
		}
		return uint64(value), nil
	case info == 27:
		if d.off+8 > len(d.data) {
			return 0, ErrTruncated
		}
		value := binary.BigEndian.Uint64(d.data[d.off:])
		d.off += 8
		if value <= math.MaxUint32 {
			return 0, ErrNonCanonical
		}
		return value, nil
	case info == 31:
		return 0, ErrIndefinite
	default: // 28, 29, 30
		return 0, ErrReserved
	}
}

func (d *decoder) take(length uint64) ([]byte, error) {
	remaining := uint64(len(d.data) - d.off)
	if remaining < length || length > uint64(maxIntForPlatform) {
		return nil, ErrTruncated
	}
	payload := d.data[d.off : d.off+int(length)]
	d.off += int(length)
	return payload, nil
}

func validKey(key any) bool {
	switch key.(type) {
	case int64, uint64, string, []byte:
		return true
	default:
		return false
	}
}

// boundedCap keeps allocations proportional to the remaining input even when a
// hostile message declares a huge container length.
func boundedCap(declared uint64) int {
	const maxAllocation = 1 << 16
	if declared > maxAllocation {
		return maxAllocation
	}
	return int(declared)
}
