package streamdec

import "fmt"

// Error is a decode failure. Offset is the absolute byte offset in the
// stream at which the problem was detected.
type Error struct {
	Offset int64
	Msg    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("decode error at offset %d: %s", e.Offset, e.Msg)
}

func errf(off int64, format string, args ...any) *Error {
	return &Error{Offset: off, Msg: fmt.Sprintf(format, args...)}
}
