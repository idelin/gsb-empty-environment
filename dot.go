package ormap

import "sort"

// Dot uniquely identifies a single assignment event: the replica that
// produced it and that replica's logical counter at the time.
type Dot struct {
	Replica string
	Counter uint64
}

// Less orders dots by logical time (counter) first, then by replica ID.
// It is a total order, used only for deterministic display resolution;
// it never causes values to be dropped.
func (d Dot) Less(o Dot) bool {
	if d.Counter != o.Counter {
		return d.Counter < o.Counter
	}
	return d.Replica < o.Replica
}

// CausalContext is the compact "tombstone" structure. It records every dot
// a replica has ever observed (live or removed), so that re-delivered old
// operations whose dots are already covered cannot resurrect values.
//
// Storage is compressed: the contiguous prefix of each replica's dot stream
// is folded into Max (a version vector); only out-of-order dots beyond the
// prefix are kept individually in Extra. Compaction preserves exactly the
// same causal information: Contains is invariant under compact().
type CausalContext struct {
	Max   map[string]uint64
	Extra map[Dot]struct{}
}

func NewCausalContext() *CausalContext {
	return &CausalContext{
		Max:   make(map[string]uint64),
		Extra: make(map[Dot]struct{}),
	}
}

// Contains reports whether the dot is covered by this causal context.
func (c *CausalContext) Contains(d Dot) bool {
	if d.Counter <= c.Max[d.Replica] {
		return true
	}
	_, ok := c.Extra[d]
	return ok
}

// Insert records a dot and compacts contiguous runs into the version
// vector, keeping the representation minimal without losing causality.
func (c *CausalContext) Insert(d Dot) {
	if d.Counter <= c.Max[d.Replica] {
		return
	}
	c.Extra[d] = struct{}{}
	c.compact()
}

// Merge folds another context into this one (idempotent, commutative,
// associative).
func (c *CausalContext) Merge(o *CausalContext) {
	for replica, max := range o.Max {
		if max > c.Max[replica] {
			c.Max[replica] = max
		}
	}
	for d := range o.Extra {
		if !c.Contains(d) {
			c.Extra[d] = struct{}{}
		}
	}
	c.compact()
}

// compact folds Extra dots that extend a replica's contiguous prefix into
// Max, and drops Extra dots already covered by Max.
func (c *CausalContext) compact() {
	for d := range c.Extra {
		if d.Counter <= c.Max[d.Replica] {
			delete(c.Extra, d)
		}
	}
	for {
		extended := false
		for d := range c.Extra {
			if d.Counter == c.Max[d.Replica]+1 {
				c.Max[d.Replica] = d.Counter
				delete(c.Extra, d)
				extended = true
			}
		}
		if !extended {
			return
		}
	}
}

func (c *CausalContext) Clone() *CausalContext {
	out := NewCausalContext()
	for replica, max := range c.Max {
		out.Max[replica] = max
	}
	for d := range c.Extra {
		out.Extra[d] = struct{}{}
	}
	return out
}

func (c *CausalContext) Equal(o *CausalContext) bool {
	if len(c.Max) != len(o.Max) || len(c.Extra) != len(o.Extra) {
		return false
	}
	for replica, max := range c.Max {
		if o.Max[replica] != max {
			return false
		}
	}
	for d := range c.Extra {
		if _, ok := o.Extra[d]; !ok {
			return false
		}
	}
	return true
}

// Dots returns all covered dots in sorted order (for inspection/debugging).
func (c *CausalContext) Dots() []Dot {
	var out []Dot
	for replica, max := range c.Max {
		for counter := uint64(1); counter <= max; counter++ {
			out = append(out, Dot{Replica: replica, Counter: counter})
		}
	}
	for d := range c.Extra {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}
