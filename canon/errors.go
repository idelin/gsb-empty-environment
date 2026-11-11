package canon

import (
	"errors"
	"reflect"
)

// Sentinel errors returned by the encoder and decoder. Callers can use
// errors.Is to distinguish rejection reasons.
var (
	// ErrTruncated means an item declared more bytes than the input contains.
	ErrTruncated = errors.New("canon: truncated CBOR input")
	// ErrTrailingData means bytes remain after the first complete value.
	ErrTrailingData = errors.New("canon: trailing data after CBOR value")
	// ErrEmpty means the input contains no value.
	ErrEmpty = errors.New("canon: empty CBOR input")
	// ErrIndefinite means an indefinite-length head or a standalone break was
	// found; the grammar only allows definite-length items.
	ErrIndefinite = errors.New("canon: indefinite-length item not allowed")
	// ErrNonCanonical means a head argument did not use the shortest encoding.
	ErrNonCanonical = errors.New("canon: non-minimal integer or length encoding")
	// ErrReserved means a reserved additional-information value was used.
	ErrReserved = errors.New("canon: reserved additional information value")
	// ErrDuplicateKey means a map contains two keys with identical encodings.
	ErrDuplicateKey = errors.New("canon: duplicate map key encoding")
	// ErrKeyOrder means map entries are not in canonical key order.
	ErrKeyOrder = errors.New("canon: map keys out of canonical order")
	// ErrInvalidUTF8 means a text string is not valid UTF-8.
	ErrInvalidUTF8 = errors.New("canon: invalid UTF-8 text string")
	// ErrUnsupportedValue means the wire item is outside the five-type grammar
	// (tags, booleans, null, floats or other simple values).
	ErrUnsupportedValue = errors.New("canon: value outside supported grammar")
	// ErrIntegerOverflow means the encoded integer cannot fit in int64.
	ErrIntegerOverflow = errors.New("canon: integer exceeds int64 range")
	// ErrDepthExceeded means nesting passed the recursion limit.
	ErrDepthExceeded = errors.New("canon: nesting depth exceeded")
	// ErrBudgetExceeded means a configured resource budget was exhausted.
	ErrBudgetExceeded = errors.New("canon: resource budget exceeded")
)

// UnsupportedTypeError reports a Go value that cannot be encoded because its
// type is outside the supported grammar.
type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) Error() string {
	if e.Type == nil {
		return "canon: unsupported Go value"
	}
	return "canon: unsupported Go type " + e.Type.String()
}

// BudgetError identifies which resource limit was crossed. It wraps
// ErrBudgetExceeded so errors.Is continues to work.
type BudgetError struct {
	Resource string
	Limit    uint64
}

func (e *BudgetError) Error() string {
	return "canon: " + e.Resource + " budget exceeded (limit " +
		formatUint(e.Limit) + ")"
}

func (e *BudgetError) Unwrap() error { return ErrBudgetExceeded }

func formatUint(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// Limits bounds the resources Decode may consume while parsing untrusted input.
// A zero field means "no additional limit"; callers normally build limits with
// DefaultLimits and adjust individual fields.
type Limits struct {
	// MaxBytes caps the total accepted input length.
	MaxBytes uint64
	// MaxItems caps the number of encoded items (including nested items).
	MaxItems uint64
	// MaxDepth caps array/map nesting depth.
	MaxDepth uint64
	// MaxStringLen caps the payload length of each text or byte string.
	MaxStringLen uint64
	// MaxContainerLen caps the declared length of each array or map.
	MaxContainerLen uint64
}

// DefaultLimits returns conservative budgets suitable for signed device
// messages: 1 MiB input, one million items, 256 nesting levels, 64 KiB per
// string and 65535 entries per container.
func DefaultLimits() Limits {
	return Limits{
		MaxBytes:        1 << 20,
		MaxItems:        1_000_000,
		MaxDepth:        256,
		MaxStringLen:    1 << 16,
		MaxContainerLen: 1<<16 - 1,
	}
}
