package iac

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorCode is a stable machine-readable planning/execution error code.
type ErrorCode string

const (
	ErrorCBDConflict     ErrorCode = "CBD_CONFLICT"
	ErrorPreventDestroy  ErrorCode = "PREVENT_DESTROY"
	ErrorDependencyCycle ErrorCode = "DEPENDENCY_CYCLE"
	ErrorExecutionCycle  ErrorCode = "EXECUTION_CYCLE"
	ErrorUnknownResource ErrorCode = "UNKNOWN_RESOURCE"
	ErrorInvalidAction   ErrorCode = "INVALID_ACTION"
	ErrorInvalidGraph    ErrorCode = "INVALID_GRAPH"
)

// Violation describes one prevent_destroy blocking action and its trigger
// chain. The chain starts at the original cause and ends at Address.
type Violation struct {
	Address string     `json:"address"`
	Action  ActionKind `json:"action"`
	Chain   []string   `json:"chain"`
}

// PlanError is returned for all planning-stage safety violations.
type PlanError struct {
	Code       ErrorCode   `json:"code"`
	Message    string      `json:"message"`
	Chain      []string    `json:"chain,omitempty"`
	Chains     [][]string  `json:"chains,omitempty"`
	Cycle      []string    `json:"cycle,omitempty"`
	Violations []Violation `json:"violations,omitempty"`
}

func (e *PlanError) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Code))
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if len(e.Cycle) > 0 {
		b.WriteString(" [")
		b.WriteString(strings.Join(e.Cycle, " -> "))
		b.WriteString("]")
	}
	if len(e.Chain) > 0 {
		b.WriteString(" [")
		b.WriteString(strings.Join(e.Chain, " -> "))
		b.WriteString("]")
	}
	if len(e.Violations) > 0 {
		for _, v := range e.Violations {
			b.WriteString(fmt.Sprintf(" {%s %s: %s}", v.Address, v.Action, strings.Join(v.Chain, " -> ")))
		}
	}
	return b.String()
}

// HasErrorCode reports whether err is (or wraps) a PlanError with code.
func HasErrorCode(err error, code ErrorCode) bool {
	var pe *PlanError
	return errors.As(err, &pe) && pe.Code == code
}

func cbdConflictError(paths [][]string) *PlanError {
	paths = sortChains(dedupeChains(paths))
	pe := &PlanError{
		Code:    ErrorCBDConflict,
		Message: "create_before_destroy propagation reached a forbid_cbd resource",
		Chains:  paths,
	}
	if len(paths) > 0 {
		pe.Chain = append([]string(nil), paths[0]...)
	}
	return pe
}

func preventDestroyError(vs []Violation) *PlanError {
	return &PlanError{
		Code:       ErrorPreventDestroy,
		Message:    "prevent_destroy is violated by at least one planned destroy action",
		Violations: vs,
	}
}

func dependencyCycleError(cycle []string) *PlanError {
	return &PlanError{
		Code:    ErrorDependencyCycle,
		Message: "resource dependency graph contains a cycle",
		Cycle:   cycle,
	}
}

func executionCycleError(cycle []string) *PlanError {
	return &PlanError{
		Code:    ErrorExecutionCycle,
		Message: "compiled action graph contains a cycle",
		Cycle:   cycle,
	}
}

func dedupeChains(chains [][]string) [][]string {
	seen := make(map[string]bool)
	out := make([][]string, 0, len(chains))
	for _, c := range chains {
		key := strings.Join(c, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, append([]string(nil), c...))
	}
	return out
}

func sortChains(chains [][]string) [][]string {
	// Shortest chain first, lexicographic as a deterministic tie-breaker.
	for i := 1; i < len(chains); i++ {
		for j := i; j > 0 && chainLess(chains[j], chains[j-1]); j-- {
			chains[j], chains[j-1] = chains[j-1], chains[j]
		}
	}
	return chains
}

func chainLess(a, b []string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
