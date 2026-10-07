package hb

import (
	"fmt"
	"strings"
)

// ConflictError reports two contradictory definitions of the same event ID,
// or two different events claiming the same (Node, Seq) slot.
type ConflictError struct {
	ID       string
	Existing Event
	Incoming Event
	Reason   string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflicting event %q: %s (have %v, got %v)",
		e.ID, e.Reason, e.Existing, e.Incoming)
}

// CycleError reports a causality cycle with the evidence path.
// Path lists event IDs forming the cycle (first == last); Why gives the
// human-readable reason for each edge along the path.
type CycleError struct {
	Path []string
	Why  []string
}

func (e *CycleError) Error() string {
	var b strings.Builder
	b.WriteString("causality cycle detected: ")
	b.WriteString(strings.Join(e.Path, " -> "))
	for i, why := range e.Why {
		fmt.Fprintf(&b, "\n  %s -> %s: %s", e.Path[i], e.Path[i+1], why)
	}
	return b.String()
}

// InvariantError reports a violated message-graph invariant.
type InvariantError struct {
	Rule   string
	Detail string
}

func (e *InvariantError) Error() string {
	return fmt.Sprintf("invariant %s violated: %s", e.Rule, e.Detail)
}
