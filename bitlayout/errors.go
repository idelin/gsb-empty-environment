package bitlayout

import "fmt"

// ErrKind classifies failures produced by compiling, encoding and decoding.
type ErrKind int

const (
	// ErrNeedInput means the decoder ran out of input bits. It is
	// recoverable: feed more bytes and run the session again.
	ErrNeedInput ErrKind = iota
	// ErrReservedBits means alignment padding bits were not zero.
	ErrReservedBits
	// ErrIllegalValue means a field value is out of range, a tag has no
	// matching union case, or a checksum does not match.
	ErrIllegalValue
	// ErrQuota means a decode quota (elements, bits or depth) was exceeded,
	// including length multiplication overflow.
	ErrQuota
	// ErrCapacity means the destination buffer cannot hold the record.
	ErrCapacity
	// ErrSchema means the schema itself is invalid (compile time).
	ErrSchema
)

// Error is the single error type produced by this package. Path is a schema
// path (e.g. "telemetry.samples[2].v") and Bit is the absolute input bit
// offset at which the problem was detected.
type Error struct {
	Kind ErrKind
	Path string
	Bit  uint64
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("bitlayout: %s: %s (bit %d): %s", kindName(e.Kind), e.Path, e.Bit, e.Msg)
}

func kindName(k ErrKind) string {
	switch k {
	case ErrNeedInput:
		return "need input"
	case ErrReservedBits:
		return "illegal reserved bits"
	case ErrIllegalValue:
		return "illegal value"
	case ErrQuota:
		return "quota exceeded"
	case ErrCapacity:
		return "insufficient capacity"
	case ErrSchema:
		return "invalid schema"
	}
	return "error"
}

// IsNeedInput reports whether err is a recoverable short-input error.
func IsNeedInput(err error) bool {
	e, ok := err.(*Error)
	return ok && e.Kind == ErrNeedInput
}

func schemaErr(path, format string, args ...any) *Error {
	return &Error{Kind: ErrSchema, Path: path, Msg: fmt.Sprintf(format, args...)}
}
