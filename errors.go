package bitlayout

import "fmt"

// ErrKind classifies encode/decode failures so callers can react
// programmatically instead of parsing message strings.
type ErrKind int

const (
	// ErrNeedMoreInput means the input ended before the record was complete.
	// It is not a hard failure: feed more bytes and try again.
	ErrNeedMoreInput ErrKind = iota
	// ErrReservedBits means padding/reserved bits were not zero.
	ErrReservedBits
	// ErrValue means a field value was illegal: out of range, an undefined
	// union tag, a checksum mismatch, or a schema/value type mismatch.
	ErrValue
	// ErrQuota means a decode quota (elements, bits, or depth) was exceeded.
	ErrQuota
	// ErrCapacity means the caller-provided buffer cannot hold the record.
	ErrCapacity
	// ErrOverflow means a length/size computation overflowed 64 bits.
	ErrOverflow
)

func (k ErrKind) String() string {
	switch k {
	case ErrNeedMoreInput:
		return "need-more-input"
	case ErrReservedBits:
		return "reserved-bits"
	case ErrValue:
		return "illegal-value"
	case ErrQuota:
		return "quota-exceeded"
	case ErrCapacity:
		return "capacity"
	case ErrOverflow:
		return "overflow"
	}
	return "unknown"
}

// Error is the single error type produced by encoding and decoding.
// Path is a schema path (e.g. /body/samples[2]) and Bit is the input or
// output bit offset at which the problem was detected.
type Error struct {
	Kind ErrKind
	Path string
	Bit  int64
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s at %s (bit %d): %s", e.Kind, e.Path, e.Bit, e.Msg)
}

func errf(kind ErrKind, path string, bit int64, format string, args ...any) *Error {
	return &Error{Kind: kind, Path: path, Bit: bit, Msg: fmt.Sprintf(format, args...)}
}
