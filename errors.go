package netsec

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Resource names the budget dimension that was exceeded.
type Resource string

const (
	ResourceNodes      Resource = "nodes"
	ResourceDepth      Resource = "depth"
	ResourceMergeDepth Resource = "merge-depth"
)

var (
	// ErrBudgetExceeded matches every limit violation.
	ErrBudgetExceeded = errors.New("yaml expansion budget exceeded")
	// ErrNodesExceeded matches node-count limit violations.
	ErrNodesExceeded = errors.New("yaml expansion node limit exceeded")
	// ErrDepthExceeded matches depth limit violations.
	ErrDepthExceeded = errors.New("yaml expansion depth limit exceeded")
	// ErrMergeDepthExceeded matches merge-key nesting limit violations.
	ErrMergeDepthExceeded = errors.New("yaml merge-key depth limit exceeded")
	// ErrAliasCycle matches cyclic anchor/alias graphs.
	ErrAliasCycle = errors.New("yaml alias cycle")
	// ErrInvalidAlias matches an alias without a target.
	ErrInvalidAlias = errors.New("yaml alias has no target")
)

// LimitError describes a budget dimension that exceeded its configured limit.
type LimitError struct {
	Resource Resource
	Limit    int
	Actual   int
	Stats    Stats
	Line     int
	Column   int
}

func (e *LimitError) Error() string {
	position := ""
	if e.Line > 0 {
		position = fmt.Sprintf(" at line %d", e.Line)
		if e.Column > 0 {
			position += fmt.Sprintf(", column %d", e.Column)
		}
	}
	return fmt.Sprintf("%s: estimated %s %d exceeds limit %d%s", ErrBudgetExceeded, e.Resource, e.Actual, e.Limit, position)
}

// Unwrap exposes the dimension-specific sentinel error.
func (e *LimitError) Unwrap() error {
	switch e.Resource {
	case ResourceNodes:
		return ErrNodesExceeded
	case ResourceDepth:
		return ErrDepthExceeded
	case ResourceMergeDepth:
		return ErrMergeDepthExceeded
	default:
		return ErrBudgetExceeded
	}
}

// Is makes errors.Is(err, ErrBudgetExceeded) true for every limit error.
func (e *LimitError) Is(target error) bool {
	return target == ErrBudgetExceeded || target == e.Unwrap()
}

// CycleError describes an alias that expands back into its own expansion path.
type CycleError struct {
	Alias  *yaml.Node
	Anchor string
	Line   int
	Column int
	Path   []*yaml.Node
}

func (e *CycleError) Error() string {
	position := ""
	if e.Line > 0 {
		position = fmt.Sprintf(" at line %d", e.Line)
		if e.Column > 0 {
			position += fmt.Sprintf(", column %d", e.Column)
		}
	}
	if e.Anchor != "" {
		return fmt.Sprintf("%s involving anchor %q%s", ErrAliasCycle, e.Anchor, position)
	}
	return fmt.Sprintf("%s%s", ErrAliasCycle, position)
}

// Unwrap exposes ErrAliasCycle.
func (e *CycleError) Unwrap() error {
	return ErrAliasCycle
}

// InvalidAliasError describes an alias node whose Alias pointer is nil.
type InvalidAliasError struct {
	Alias *yaml.Node
	Line  int
}

func (e *InvalidAliasError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s at line %d", ErrInvalidAlias, e.Line)
	}
	return ErrInvalidAlias.Error()
}

// Unwrap exposes ErrInvalidAlias.
func (e *InvalidAliasError) Unwrap() error {
	return ErrInvalidAlias
}

// MalformedError describes a malformed Node graph, such as a mapping with an
// odd number of content nodes.
type MalformedError struct {
	Line   int
	Detail string
}

func (e *MalformedError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("yaml: malformed node tree at line %d: %s", e.Line, e.Detail)
	}
	return fmt.Sprintf("yaml: malformed node tree: %s", e.Detail)
}
