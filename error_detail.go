package safeunpack

import (
	"fmt"
	"strings"
)

// Error carries the failing entry path and byte offset while preserving
// errors.Is compatibility with the exported sentinel errors.
type Error struct {
	Kind   error
	Path   string
	Offset int64
	Detail string
}

func (e *Error) Error() string {
	var b strings.Builder
	if e.Kind != nil {
		b.WriteString(e.Kind.Error())
	}
	if e.Detail != "" {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(e.Detail)
	}
	if e.Path != "" {
		fmt.Fprintf(&b, " [path=%q]", e.Path)
	}
	if e.Offset >= 0 {
		fmt.Fprintf(&b, " [offset=%d]", e.Offset)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Kind }

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
