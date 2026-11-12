package netsec

import (
	"errors"
	"fmt"
	"math"
)

// Default limits are deliberately conservative. A caller can raise or lower
// every limit, but zero-valued fields in Limits are replaced with these values
// by normalize.
const (
	DefaultMaxDepth                  = 128
	DefaultMaxTagDepth               = 16
	DefaultMaxTag24Depth             = 8
	DefaultMaxIndefiniteStringChunks = 256
	DefaultMaxDeclaredLengthRatio    = 1.0
	DefaultMaxBigNumBytes            = 1024
	DefaultMaxInputBytes             = 8 << 20
	DefaultMaxContainerElements      = 1 << 16
)

// Limits contains the caller-controlled anti-amplification budgets used while
// decoding a request. Zero values select the corresponding Default* value.
//
// MaxDeclaredLengthRatio is compared against the number of bytes remaining in
// the input after the length header. For example, a ratio of 1 means the
// declared byte or element count may not exceed the remaining input size before
// its contents are read. A value below 1 is stricter; a value above 1 permits
// more slack but is still safe because actual reads are bounds-checked.
type Limits struct {
	// MaxDepth bounds overall nesting of arrays, maps, tags and tag 24
	// recursion. It is a backstop against stack exhaustion.
	MaxDepth int

	// MaxTagDepth bounds the number of consecutive tag wrappers in one tag
	// chain (tag(tag(...))).
	MaxTagDepth int

	// MaxTag24Depth bounds recursive decoding of tag 24 embedded CBOR.
	MaxTag24Depth int

	// MaxIndefiniteStringChunks bounds the total number of chunks across all
	// indefinite-length byte and text strings in a decode, including strings
	// reached through tag 24 recursion.
	MaxIndefiniteStringChunks int

	// MaxIndefStringChunks is an alias for MaxIndefiniteStringChunks.
	MaxIndefStringChunks int

	// MaxDeclaredLengthRatio bounds a declared length (or array/map element
	// count) relative to the bytes remaining after its header.
	MaxDeclaredLengthRatio float64

	// MaxLengthRatio is an alias for MaxDeclaredLengthRatio.
	MaxLengthRatio float64

	// MaxBigNumBytes bounds the byte string payload of tags 2 and 3.
	MaxBigNumBytes int

	// MaxBignumBytes is an alias for MaxBigNumBytes.
	MaxBignumBytes int

	// MaxInputBytes bounds input consumed by DecodeReader. It is ignored by
	// Decode and Reader because their input is already in memory.
	MaxInputBytes int

	// MaxContainerElements bounds the number of entries in an array or map.
	// It catches amplification from containers that are small on the wire but
	// large after being converted to Go values.
	MaxContainerElements int
}

// Options is an alias kept for callers that prefer the name Options.
type Options = Limits

// DefaultLimits returns a fresh copy of the default limits.
func DefaultLimits() Limits {
	return Limits{
		MaxDepth:                  DefaultMaxDepth,
		MaxTagDepth:               DefaultMaxTagDepth,
		MaxTag24Depth:             DefaultMaxTag24Depth,
		MaxIndefiniteStringChunks: DefaultMaxIndefiniteStringChunks,
		MaxDeclaredLengthRatio:    DefaultMaxDeclaredLengthRatio,
		MaxBigNumBytes:            DefaultMaxBigNumBytes,
		MaxInputBytes:             DefaultMaxInputBytes,
		MaxContainerElements:      DefaultMaxContainerElements,
	}
}

func (l Limits) normalize() Limits {
	defaults := DefaultLimits()
	if l.MaxDepth <= 0 {
		l.MaxDepth = defaults.MaxDepth
	}
	if l.MaxTagDepth <= 0 {
		l.MaxTagDepth = defaults.MaxTagDepth
	}
	if l.MaxTag24Depth <= 0 {
		l.MaxTag24Depth = defaults.MaxTag24Depth
	}
	if l.MaxIndefiniteStringChunks <= 0 {
		if l.MaxIndefStringChunks > 0 {
			l.MaxIndefiniteStringChunks = l.MaxIndefStringChunks
		} else {
			l.MaxIndefiniteStringChunks = defaults.MaxIndefiniteStringChunks
		}
	}
	if l.MaxDeclaredLengthRatio <= 0 ||
		math.IsNaN(l.MaxDeclaredLengthRatio) ||
		math.IsInf(l.MaxDeclaredLengthRatio, 0) {
		if l.MaxLengthRatio > 0 &&
			!math.IsNaN(l.MaxLengthRatio) &&
			!math.IsInf(l.MaxLengthRatio, 0) {
			l.MaxDeclaredLengthRatio = l.MaxLengthRatio
		} else {
			l.MaxDeclaredLengthRatio = defaults.MaxDeclaredLengthRatio
		}
	}
	if l.MaxBigNumBytes <= 0 {
		if l.MaxBignumBytes > 0 {
			l.MaxBigNumBytes = l.MaxBignumBytes
		} else {
			l.MaxBigNumBytes = defaults.MaxBigNumBytes
		}
	}
	if l.MaxInputBytes <= 0 {
		l.MaxInputBytes = defaults.MaxInputBytes
	}
	if l.MaxContainerElements <= 0 {
		l.MaxContainerElements = defaults.MaxContainerElements
	}
	return l
}

var (
	// ErrLimitExceeded is matched by every budget violation.
	ErrLimitExceeded = errors.New("cbor: limit exceeded")
	// ErrInvalidCBOR is matched by malformed input.
	ErrInvalidCBOR = errors.New("cbor: invalid CBOR")
	// ErrDuplicateMapKey is returned when a map repeats a key.
	ErrDuplicateMapKey = errors.New("cbor: duplicate map key")
	// ErrTrailingData is returned when bytes remain after one decoded item.
	ErrTrailingData = errors.New("cbor: trailing data")
	// ErrInputTooLarge is returned by DecodeReader when the input budget is hit.
	ErrInputTooLarge = errors.New("cbor: input too large")

	ErrTagDepthExceeded               = errors.New("cbor: tag nesting limit exceeded")
	ErrTag24DepthExceeded             = errors.New("cbor: tag 24 recursion limit exceeded")
	ErrIndefiniteStringChunks         = errors.New("cbor: indefinite string chunk limit exceeded")
	ErrDeclaredLengthExceeded         = errors.New("cbor: declared length ratio limit exceeded")
	ErrBigNumTooLarge                 = errors.New("cbor: bignum byte length limit exceeded")
	ErrNestingTooDeep                 = errors.New("cbor: nesting depth limit exceeded")
	ErrContainerElementsExceeded      = errors.New("cbor: container element limit exceeded")
	ErrIndefiniteStringChunksExceeded = ErrIndefiniteStringChunks
	ErrDuplicateKey                   = ErrDuplicateMapKey
	ErrNestingDepthExceeded           = ErrNestingTooDeep
	ErrTag24RecursionExceeded         = ErrTag24DepthExceeded
	ErrChunkLimitExceeded             = ErrIndefiniteStringChunks
	ErrLengthRatioExceeded            = ErrDeclaredLengthExceeded
	ErrBigNumBytesExceeded            = ErrBigNumTooLarge
)

// LimitError describes a specific budget violation. errors.Is can match it
// against ErrLimitExceeded and against the specific exported sentinel.
type LimitError struct {
	Kind     string
	Limit    float64
	Got      uint64
	Offset   int
	specific error
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("cbor: %s limit exceeded at byte %d (got %d, max %v)",
		e.Kind, e.Offset, e.Got, e.Limit)
}

// Unwrap makes errors.Is(err, ErrLimitExceeded) work.
func (e *LimitError) Unwrap() error { return ErrLimitExceeded }

// Is also matches the specific limit sentinel.
func (e *LimitError) Is(target error) bool {
	return target == ErrLimitExceeded || target == e.specific
}

func limitError(kind string, max, got uint64, offset int, specific error) error {
	return &LimitError{
		Kind:     kind,
		Limit:    float64(max),
		Got:      got,
		Offset:   offset,
		specific: specific,
	}
}

// SyntaxError describes malformed CBOR at a byte offset.
type SyntaxError struct {
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("cbor: invalid input at byte %d: %s", e.Offset, e.Msg)
}

func (e *SyntaxError) Is(target error) bool { return target == ErrInvalidCBOR }

func syntaxError(offset int, format string, args ...any) error {
	return &SyntaxError{Offset: offset, Msg: fmt.Sprintf(format, args...)}
}
