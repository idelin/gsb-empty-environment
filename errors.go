package netsec

import "fmt"

// NodeError reports a malformed probabilistic node: a missing identifier, a
// non-positive weight, or a weight sum different from exactly one.
type NodeError struct {
	NodeID string
	Reason string
}

func (e *NodeError) Error() string {
	if e.NodeID == "" {
		return fmt.Sprintf("probabilistic node: %s", e.Reason)
	}
	return fmt.Sprintf("probabilistic node %q: %s", e.NodeID, e.Reason)
}

// LimitError reports that a bounded loop exceeded its iteration limit.
type LimitError struct {
	Limit int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("loop exceeded iteration limit of %d", e.Limit)
}

// EvalError reports an undefined variable or malformed program.
type EvalError struct {
	Reason string
}

func (e *EvalError) Error() string { return "evaluation error: " + e.Reason }
