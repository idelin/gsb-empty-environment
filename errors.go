package wire

import (
	"fmt"
	"strings"
)

// ErrCode classifies parse failures.
type ErrCode int

const (
	ErrOverflow    ErrCode = iota // varint longer than 10 bytes or 10th byte > 1
	ErrFieldNumber                // field number outside 1..2^29-1
	ErrWireType                   // wire type not in {0,1,2,5}
	ErrTruncated                  // input ended in the middle of a field/message
	ErrLimit                      // a caller-supplied limit was exceeded
	ErrPacked                     // malformed packed payload (bad width, bad element)
)

func (c ErrCode) String() string {
	switch c {
	case ErrOverflow:
		return "overflow"
	case ErrFieldNumber:
		return "field-number"
	case ErrWireType:
		return "wire-type"
	case ErrTruncated:
		return "truncated"
	case ErrLimit:
		return "limit"
	case ErrPacked:
		return "packed"
	}
	return "unknown"
}

// Error describes a parse failure with the absolute byte offset of the
// offending key/length/value and the path of field numbers from the root
// message to the field being parsed when the failure occurred.
type Error struct {
	Code   ErrCode
	Offset int64
	Path   []int
	Detail string
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "wire: %s: %s at byte offset %d", e.Code, e.Detail, e.Offset)
	if len(e.Path) > 0 {
		b.WriteString(" (field path ")
		for i, f := range e.Path {
			if i > 0 {
				b.WriteByte('.')
			}
			fmt.Fprintf(&b, "%d", f)
		}
		b.WriteByte(')')
	}
	return b.String()
}
