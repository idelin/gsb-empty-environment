package traj

import "fmt"

// Error is the error type returned by this package. Kind holds one of
// the package-level sentinel values so callers can classify failures
// programmatically (Go 1.11 has no errors.Is/As).
type Error struct {
	Kind    error
	Message string
}

func (e *Error) Error() string { return e.Message }

// IsKind reports whether err is a package Error with the given
// sentinel kind.
func IsKind(err, kind error) bool {
	if err == kind {
		return true
	}
	te, ok := err.(*Error)
	return ok && te.Kind == kind
}

func newError(kind error, format string, args ...interface{}) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}
