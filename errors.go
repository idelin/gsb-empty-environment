package netsec

import (
	"fmt"
)

// Sentinel errors returned by Decode. Callers can use errors.Is to classify
// failures without depending on error text.
var (
	ErrProtocol       = fmt.Errorf("unsupported pickle protocol")
	ErrProtocolOrder  = fmt.Errorf("PROTO must be the first opcode")
	ErrOpcode         = fmt.Errorf("unsupported pickle opcode")
	ErrTruncated      = fmt.Errorf("truncated pickle input")
	ErrFrame          = fmt.Errorf("invalid pickle frame")
	ErrStack          = fmt.Errorf("invalid pickle stack operand")
	ErrStackLimit     = fmt.Errorf("pickle stack limit exceeded")
	ErrMemoLimit      = fmt.Errorf("pickle memo limit exceeded")
	ErrMemoUndefined  = fmt.Errorf("undefined pickle memo reference")
	ErrDepthLimit     = fmt.Errorf("pickle nesting depth limit exceeded")
	ErrTrailingData   = fmt.Errorf("trailing bytes after STOP")
	ErrInvalidUnicode = fmt.Errorf("invalid UTF-8 string")
	ErrDictKey        = fmt.Errorf("unsupported dictionary key type")
	ErrCycle          = fmt.Errorf("recursive pickle value")
)

// DecodeError identifies the byte offset at which decoding failed.
type DecodeError struct {
	Offset int
	Err    error
}

func (e *DecodeError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return fmt.Sprintf("pickle decode error at byte %d", e.Offset)
	}
	return fmt.Sprintf("%v at byte %d", e.Err, e.Offset)
}

// Unwrap permits errors.Is/errors.As to inspect the underlying sentinel error.
func (e *DecodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
