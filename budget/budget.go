// Package budget exposes the YAML alias-expansion budget under a dedicated
// import path. It is a thin facade over the root netsec package.
package budget

import (
	core "example.org/netsec/q0040"

	"gopkg.in/yaml.v3"
)

type (
	Limits            = core.Limits
	Stats             = core.Stats
	Budget            = core.Budget
	LoadFunc          = core.LoadFunc
	Resource          = core.Resource
	LimitError        = core.LimitError
	CycleError        = core.CycleError
	InvalidAliasError = core.InvalidAliasError
	MalformedError    = core.MalformedError
)

const (
	ResourceNodes      = core.ResourceNodes
	ResourceDepth      = core.ResourceDepth
	ResourceMergeDepth = core.ResourceMergeDepth
)

var (
	ErrBudgetExceeded     = core.ErrBudgetExceeded
	ErrNodesExceeded      = core.ErrNodesExceeded
	ErrDepthExceeded      = core.ErrDepthExceeded
	ErrMergeDepthExceeded = core.ErrMergeDepthExceeded
	ErrAliasCycle         = core.ErrAliasCycle
	ErrInvalidAlias       = core.ErrInvalidAlias
)

// DefaultLimits returns the same conservative defaults as the root package.
func DefaultLimits() Limits {
	return core.DefaultLimits()
}

// NewBudget returns a reusable expansion budget.
func NewBudget(limits Limits) *Budget {
	return core.NewBudget(limits)
}

// NewDefaultBudget returns a budget using DefaultLimits.
func NewDefaultBudget() *Budget {
	return core.NewDefaultBudget()
}

// Analyze estimates expanded nodes, depth, and merge-key nesting.
func Analyze(root *yaml.Node, limits ...Limits) (Stats, error) {
	return core.Analyze(root, limits...)
}

// Check rejects a document whose estimate exceeds any configured limit.
func Check(root *yaml.Node, limits ...Limits) error {
	return core.Check(root, limits...)
}

// Validate is an alias for Check.
func Validate(root *yaml.Node, limits ...Limits) error {
	return core.Validate(root, limits...)
}

// Parse parses YAML into an unexpanded yaml.v3 Node tree.
func Parse(data []byte) (*yaml.Node, error) {
	return core.Parse(data)
}

// Preflight parses and validates without rewriting the Node tree.
func Preflight(data []byte, limits ...Limits) (*yaml.Node, Stats, error) {
	return core.Preflight(data, limits...)
}

// Decode validates before invoking the standard yaml.v3 loader.
func Decode(data []byte, out interface{}, limits ...Limits) error {
	return core.Decode(data, out, limits...)
}

// DecodeWith validates before invoking a caller-supplied loader.
func DecodeWith(data []byte, out interface{}, load LoadFunc, limits ...Limits) error {
	return core.DecodeWith(data, out, load, limits...)
}

// Load validates before invoking a caller-supplied loader. Limits and loader
// arguments may be supplied in either order.
func Load(data []byte, out interface{}, args ...interface{}) error {
	return core.Load(data, out, args...)
}
