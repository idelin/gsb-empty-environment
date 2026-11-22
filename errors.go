package destplan

import "fmt"

// Error marks a malformed problem description. An infeasible request is not
// an error: Solve reports it through the returned Infeasibility proof.
type Error struct {
	msg string
}

func (e *Error) Error() string { return e.msg }

func errf(format string, args ...any) error {
	return &Error{msg: fmt.Sprintf(format, args...)}
}
