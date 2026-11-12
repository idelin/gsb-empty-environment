package netsec

import "strconv"

// SyntaxError reports a trusted template that does not use the only accepted
// reference syntax ${NAME}, or contains an unescaped '$'.
type SyntaxError struct {
	Name     string
	Position int
	Template string
}

func (e *SyntaxError) Error() string {
	return "netsec: syntax error in " + e.Name + " at byte " +
		strconv.Itoa(e.Position) + ": only ${NAME} and $$ escapes are allowed"
}

// NameError reports an invalid environment variable name.
type NameError struct {
	Name string
}

func (e *NameError) Error() string {
	return "netsec: invalid variable name " + strconv.Quote(e.Name)
}

// DuplicateError reports a variable that was defined more than once.
type DuplicateError struct {
	Name string
}

func (e *DuplicateError) Error() string {
	return "netsec: duplicate variable definition " + strconv.Quote(e.Name)
}

// ImportError reports a malformed ambient environment entry.
type ImportError struct {
	Entry string
}

func (e *ImportError) Error() string {
	return "netsec: malformed environment entry " + strconv.Quote(e.Entry)
}

// UndefinedError reports a ${NAME} reference with no matching definition.
type UndefinedError struct {
	Ref string
	In  string
}

func (e *UndefinedError) Error() string {
	return "netsec: undefined reference ${" + e.Ref + "} in " + e.In
}

// CycleError reports a reference cycle. Path lists the cycle starting and
// ending at the same variable.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return "netsec: reference cycle: " + joinPath(e.Path)
}

func joinPath(path []string) string {
	out := ""
	for i, name := range path {
		if i > 0 {
			out += " -> "
		}
		out += name
	}
	return out
}

// SensitiveError reports that a sensitive variable expanded to a value
// containing bytes from an untrusted origin.
type SensitiveError struct {
	Name string
}

func (e *SensitiveError) Error() string {
	return "netsec: sensitive variable " + strconv.Quote(e.Name) +
		" contains untrusted bytes"
}

// BudgetExceededError reports that an expansion budget limit was reached.
type BudgetExceededError struct {
	Kind  string
	Limit int
	Name  string
}

func (e *BudgetExceededError) Error() string {
	return "netsec: " + e.Kind + " budget of " + strconv.Itoa(e.Limit) +
		" exceeded while expanding " + strconv.Quote(e.Name)
}

// BudgetError reports an invalid budget configuration.
type BudgetError struct {
	Reason string
}

func (e *BudgetError) Error() string {
	return "netsec: invalid budget: " + e.Reason
}
