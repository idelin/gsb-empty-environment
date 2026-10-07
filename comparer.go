package dfeq

import (
	"container/heap"
	"sync"
)

// Comparer incrementally compares two automata whose external
// fragments are filled in asynchronously. Exploration state is reused
// across fragment resolutions; every result is stamped with the DFA
// versions it is bound to.
type Comparer struct {
	mu  sync.Mutex
	a   *DFA
	b   *DFA
	exp *exploration
	// versions the exploration has absorbed so far
	seenA, seenB uint64
	started      bool
}

// NewComparer creates an incremental comparer for a and b.
func NewComparer(a, b *DFA) *Comparer {
	return &Comparer{a: a, b: b}
}

// Compare continues the product-graph exploration and returns the
// current result. It never claims equivalence while reachable
// fragments remain unexpanded.
func (c *Comparer) Compare() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		c.exp = newExploration()
		heap.Push(&c.exp.queue, frontierItem{node: pair{c.a.Start, c.b.Start}})
		c.started = true
		c.seenA, c.seenB = c.a.Version, c.b.Version
	} else if c.a.Version != c.seenA || c.b.Version != c.seenB {
		// Fragments were resolved since the last run: resume the
		// previously blocked nodes incrementally.
		c.exp.resume(c.a, c.b)
		c.seenA, c.seenB = c.a.Version, c.b.Version
	}
	c.exp.step(c.a, c.b, unionAlphabet(c.a, c.b))
	return c.exp.result(c.a, c.b)
}

// ResolveFragment supplies the expansion of a fragment on side
// (0 = first automaton, 1 = second). It is safe to call from other
// goroutines; the next Compare continues incrementally.
func (c *Comparer) ResolveFragment(side int, host, fragmentID string, exp Expansion) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.a
	if side == 1 {
		d = c.b
	}
	return d.ResolveFragment(host, fragmentID, exp)
}

// Query is a comparison bound to the DFA versions at creation time.
// Later fragment resolutions do not affect it.
type Query struct {
	a, b *DFA
}

// NewQuery snapshots both automata at their current versions.
func (c *Comparer) NewQuery() *Query {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &Query{a: c.a.Snapshot(), b: c.b.Snapshot()}
}

// Result computes the comparison for the snapshotted versions.
func (q *Query) Result() Result {
	return Compare(q.a, q.b)
}

// Versions returns the DFA versions this query is bound to.
func (q *Query) Versions() [2]uint64 {
	return [2]uint64{q.a.Version, q.b.Version}
}
