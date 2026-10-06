package binparse

import "fmt"

// ErrKind classifies parse failures.
type ErrKind int

const (
	ErrTruncated ErrKind = iota + 1
	ErrBadMagic
	ErrBadByteOrder
	ErrUnsupportedVersion
	ErrLengthMismatch
	ErrBitmap
	ErrOutOfBounds
	ErrTrailingData
	ErrNestingTooDeep
)

func (k ErrKind) String() string {
	switch k {
	case ErrTruncated:
		return "truncated input"
	case ErrBadMagic:
		return "bad magic"
	case ErrBadByteOrder:
		return "bad byte-order flag"
	case ErrUnsupportedVersion:
		return "unsupported version"
	case ErrLengthMismatch:
		return "field length mismatch"
	case ErrBitmap:
		return "bitmap inconsistency"
	case ErrOutOfBounds:
		return "field exceeds enclosing body"
	case ErrTrailingData:
		return "trailing data"
	case ErrNestingTooDeep:
		return "nesting too deep"
	}
	return "unknown error"
}

// Error describes a parse failure at a byte offset in the input.
type Error struct {
	Kind   ErrKind
	Offset int
	Msg    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("binparse: %s at offset %d: %s", e.Kind, e.Offset, e.Msg)
}

func fail(kind ErrKind, offset int, format string, args ...interface{}) *Error {
	return &Error{Kind: kind, Offset: offset, Msg: fmt.Sprintf(format, args...)}
}
