package configeval

import (
	"fmt"
	"strings"
)

// ErrorKind classifies a RefError.
type ErrorKind int

const (
	// MissingError: a referenced variable or environment key does not exist.
	MissingError ErrorKind = iota
	// CycleError: a cyclic variable reference was detected.
	CycleError
	// TypeError: an operand has the wrong type for its operator.
	TypeError
)

func (k ErrorKind) String() string {
	switch k {
	case MissingError:
		return "missing reference"
	case CycleError:
		return "cyclic reference"
	case TypeError:
		return "type error"
	}
	return "unknown error"
}

// RefError describes a failed evaluation. Chain is the full reference chain
// from the requested variable to the failing node, e.g.
// ["var:app", "var:db", "env:DB_HOST"].
type RefError struct {
	Kind   ErrorKind
	Chain  []string
	Detail string
}

func (e *RefError) Error() string {
	return fmt.Sprintf("%s: %s (chain: %s)", e.Kind, e.Detail, strings.Join(e.Chain, " -> "))
}

func newError(kind ErrorKind, chain []string, format string, args ...interface{}) *RefError {
	full := make([]string, len(chain))
	copy(full, chain)
	return &RefError{Kind: kind, Chain: full, Detail: fmt.Sprintf(format, args...)}
}
