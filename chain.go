package netsec

import "fmt"

// Chain is an immutable, ordered stack of filters. It is safe to share a
// Chain between threads; Append returns a new chain and never mutates the
// receiver.
type Chain struct {
	filters []Filter
}

// FilterChain is an alias for Chain.
type FilterChain = Chain

// NewChain validates and copies filters into a new immutable chain.
func NewChain(filters ...Filter) (*Chain, error) {
	cloned := make([]Filter, len(filters))
	for i, filter := range filters {
		if err := filter.Validate(); err != nil {
			return nil, fmt.Errorf("%w: filter %d: %w", ErrInvalidFilter, i, err)
		}
		cloned[i] = filter.clone()
	}
	return &Chain{filters: cloned}, nil
}

// MustNewChain is NewChain for tests and static configuration.
func MustNewChain(filters ...Filter) *Chain {
	chain, err := NewChain(filters...)
	if err != nil {
		panic(err)
	}
	return chain
}

// Compose is an alias for NewChain.
func Compose(filters ...Filter) (*Chain, error) {
	return NewChain(filters...)
}

// NewFilterChain is an alias for NewChain.
func NewFilterChain(filters ...Filter) (*Chain, error) {
	return NewChain(filters...)
}

// Append validates filters and returns a new chain with them installed after
// the existing ones. The receiver is unchanged.
//
// Because evaluation takes the highest-priority decision across the whole
// chain, an appended filter can only preserve or narrow the final behavior.
func (c *Chain) Append(filters ...Filter) (*Chain, error) {
	if len(filters) == 0 {
		if c == nil {
			return &Chain{}, nil
		}
		return c, nil
	}

	var base []Filter
	if c != nil {
		base = c.filters
	}
	combined := make([]Filter, 0, len(base)+len(filters))
	combined = append(combined, base...)
	for i, filter := range filters {
		if err := filter.Validate(); err != nil {
			return nil, fmt.Errorf("%w: appended filter %d: %w", ErrInvalidFilter, i, err)
		}
		combined = append(combined, filter.clone())
	}
	return &Chain{filters: combined}, nil
}

// With is an alias for Append.
func (c *Chain) With(filters ...Filter) (*Chain, error) {
	return c.Append(filters...)
}

// AppendFilter appends one filter.
func (c *Chain) AppendFilter(filter Filter) (*Chain, error) {
	return c.Append(filter)
}

// Evaluate evaluates every filter and composes the results. The highest
// priority action wins; on a tie, the most recently installed filter supplies
// the data.
func (c *Chain) Evaluate(syscall uint32, args [6]uint64) Decision {
	result := Decision{Action: ActionAllow}
	if c == nil {
		return result
	}
	for _, filter := range c.filters {
		result = ComposeDecisions(result, filter.Evaluate(syscall, args))
	}
	return result
}

// Decide is an alias for Evaluate.
func (c *Chain) Decide(syscall uint32, args [6]uint64) Decision {
	return c.Evaluate(syscall, args)
}

// EvaluateArgs is a convenience form that fills omitted arguments with zero.
func (c *Chain) EvaluateArgs(syscall uint32, args ...uint64) Decision {
	var fixed [6]uint64
	copy(fixed[:], args)
	return c.Evaluate(syscall, fixed)
}

// EvaluatePoint evaluates a Point produced by Enumerate.
func (c *Chain) EvaluatePoint(p Point) Decision {
	return c.Evaluate(p.Syscall, p.Args)
}

// Len returns the number of installed filters.
func (c *Chain) Len() int {
	if c == nil {
		return 0
	}
	return len(c.filters)
}

// Filters returns a deep copy of the chain's filters.
func (c *Chain) Filters() []Filter {
	if c == nil || len(c.filters) == 0 {
		return nil
	}
	out := make([]Filter, len(c.filters))
	for i, filter := range c.filters {
		out[i] = filter.clone()
	}
	return out
}
