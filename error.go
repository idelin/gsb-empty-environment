package taskpkg

import "fmt"

type ruleError struct{ msg string }

func (e *ruleError) Error() string { return e.msg }

func errf(format string, args ...any) error {
	return &ruleError{msg: fmt.Sprintf(format, args...)}
}
