package signalguard

import (
	"fmt"
	"strings"
)

// CircularSignalUpdate is raised (via panic) when the guard detects a cyclic
// reactive update: either an observer re-entering itself on the call stack,
// or an observer being re-triggered beyond the iteration threshold.
type CircularSignalUpdate struct {
	Reason     string   // why the guard fired
	Source     string   // observer that triggered the guard
	Chain      []string // causal chain, oldest step first
	Iterations int      // executions observed when the guard fired (0 for call-stack cycles)
	Limit      int      // configured iteration limit (0 for call-stack cycles)
}

func (e *CircularSignalUpdate) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "CircularSignalUpdate: %s", e.Reason)
	if e.Limit > 0 {
		fmt.Fprintf(&b, " (iterations: %d, limit: %d)", e.Iterations, e.Limit)
	}
	fmt.Fprintf(&b, "\ntrigger source: %s", e.Source)
	b.WriteString("\ncausal chain:")
	for i, step := range e.Chain {
		fmt.Fprintf(&b, "\n  %d. %s", i+1, step)
	}
	return b.String()
}
