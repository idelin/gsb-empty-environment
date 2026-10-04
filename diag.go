package macroexp

import (
	"fmt"
	"strings"
)

// DiagKind classifies diagnostics.
type DiagKind int

const (
	DiagSyntax DiagKind = iota
	DiagArity
	DiagRecursion
	DiagBudget
)

func (k DiagKind) String() string {
	switch k {
	case DiagSyntax:
		return "syntax error"
	case DiagArity:
		return "arity error"
	case DiagRecursion:
		return "recursion error"
	case DiagBudget:
		return "budget exceeded"
	}
	return "error"
}

// Diagnostic is a reported problem. Chain holds the shortest call
// evidence: the expansion context at the moment the problem was detected
// (detection happens at first re-entry / first exhaustion, so the chain
// is minimal by construction).
type Diagnostic struct {
	Kind  DiagKind
	Pos   Pos
	Msg   string
	Chain []Frame
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s: %s: %s\n  expansion chain: %s",
		d.Pos, d.Kind, d.Msg, FormatChain(d.Chain))
}

// FormatChain renders an expansion chain as "a -> b -> c".
func FormatChain(chain []Frame) string {
	if len(chain) == 0 {
		return "<source>"
	}
	parts := make([]string, len(chain))
	for i, f := range chain {
		s := f.Macro
		if f.InArg {
			s += " (in argument)"
		}
		parts[i] = fmt.Sprintf("%s [defined %s, called %s]", s, f.DefPos, f.CallPos)
	}
	return strings.Join(parts, " -> ")
}
