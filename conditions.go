package netsec

import "fmt"

// Condition is a branch predicate evaluated against the current route
// state, including any tag mutations performed by earlier rules.
type Condition interface {
	Match(route Route) bool
	validate() error
}

// PathLenRange matches when the AS-path length lies in [Min, Max].
// A zero bound means that side is unbounded.
type PathLenRange struct {
	Min int
	Max int
}

func (c PathLenRange) Match(r Route) bool {
	if c.Min > 0 && r.PathLen < c.Min {
		return false
	}
	if c.Max > 0 && r.PathLen > c.Max {
		return false
	}
	return true
}

func (c PathLenRange) validate() error {
	if c.Min < 0 || c.Max < 0 {
		return fmt.Errorf("path length bounds must be non-negative: %+v", c)
	}
	if c.Min > 0 && c.Max > 0 && c.Min > c.Max {
		return fmt.Errorf("path length min %d > max %d", c.Min, c.Max)
	}
	return nil
}

// TagOp selects the set predicate used by TagCondition.
type TagOp uint8

const (
	// TagAll requires every tag in Tags to be present.
	TagAll TagOp = iota
	// TagAny requires at least one tag in Tags to be present.
	TagAny
	// TagNone requires none of the tags in Tags to be present.
	TagNone
)

// TagCondition matches the route's current community tag set.
type TagCondition struct {
	Op   TagOp
	Tags TagSet
}

func (c TagCondition) Match(r Route) bool {
	switch c.Op {
	case TagAll:
		return r.Tags.Intersect(c.Tags) == c.Tags && !c.Tags.Empty()
	case TagAny:
		return !r.Tags.Intersect(c.Tags).Empty()
	case TagNone:
		return r.Tags.Intersect(c.Tags).Empty()
	default:
		return false
	}
}

func (c TagCondition) validate() error {
	if c.Op > TagNone {
		return fmt.Errorf("unknown tag condition op %d", c.Op)
	}
	if c.Tags.Empty() {
		return fmt.Errorf("tag condition requires at least one tag")
	}
	return nil
}

// AndConds matches when every child condition matches.
type AndConds []Condition

func (c AndConds) Match(r Route) bool {
	for _, sub := range c {
		if !sub.Match(r) {
			return false
		}
	}
	return true
}

func (c AndConds) validate() error {
	if len(c) == 0 {
		return fmt.Errorf("AND condition requires children")
	}
	for _, sub := range c {
		if sub == nil {
			return fmt.Errorf("nil child condition")
		}
		if err := sub.validate(); err != nil {
			return err
		}
	}
	return nil
}

// OrConds matches when at least one child condition matches.
type OrConds []Condition

func (c OrConds) Match(r Route) bool {
	for _, sub := range c {
		if sub.Match(r) {
			return true
		}
	}
	return false
}

func (c OrConds) validate() error {
	if len(c) == 0 {
		return fmt.Errorf("OR condition requires children")
	}
	for _, sub := range c {
		if sub == nil {
			return fmt.Errorf("nil child condition")
		}
		if err := sub.validate(); err != nil {
			return err
		}
	}
	return nil
}

// NotConds matches when the child condition does not match.
type NotConds struct {
	Inner Condition
}

func (c NotConds) Match(r Route) bool { return !c.Inner.Match(r) }

func (c NotConds) validate() error {
	if c.Inner == nil {
		return fmt.Errorf("NOT condition requires an inner condition")
	}
	return c.Inner.validate()
}
