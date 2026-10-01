package reactiveguard

import "fmt"

// nodeKind distinguishes the two reactive node types in reports.
type nodeKind string

const (
	kindEffect   nodeKind = "effect"
	kindComputed nodeKind = "computed"
)

// source is anything a node can depend on: a Signal or a Computed.
type source struct {
	kind string // "signal" or "computed"
	name string
	subs map[*node]bool
}

// node is an Effect or Computed registered on a Guard.
type node struct {
	guard *Guard
	kind  nodeKind
	name  string
	fn    func() interface{} // recompute body; result used by Computed only
	deps  map[*source]bool   // sources read during the last evaluation
	dirty bool               // Computed only: needs recomputation
	src   *source            // Computed only: itself as a dependency target
}

func (n *node) label() string {
	return fmt.Sprintf("%s %q", n.kind, n.name)
}

// run evaluates the node, detecting call-stack self re-entry, refreshing
// its dependency subscriptions and recording the evaluation.
func (n *node) run() interface{} {
	g := n.guard
	for _, onStack := range g.stack {
		if onStack == n {
			panic(g.stackCycleError(n))
		}
	}
	// Re-subscribe from scratch: dependencies may change between runs.
	for src := range n.deps {
		delete(src.subs, n)
	}
	n.deps = make(map[*source]bool)

	g.stack = append(g.stack, n)
	defer func() { g.stack = g.stack[:len(g.stack)-1] }()
	return n.fn()
}

// Signal is a writable reactive value. Reads inside an Effect/Computed
// are tracked as dependencies; writes notify subscribers through the
// Guard's cascade scheduler.
type Signal struct {
	guard *Guard
	src   *source
	value interface{}
}

// NewSignal creates a Signal with the given name (used in reports) and
// initial value.
func NewSignal(g *Guard, name string, initial interface{}) *Signal {
	return &Signal{
		guard: g,
		src:   &source{kind: "signal", name: name, subs: make(map[*node]bool)},
		value: initial,
	}
}

// Get reads the current value, registering the read as a dependency of
// the Effect/Computed currently evaluating (if any).
func (s *Signal) Get() interface{} {
	s.guard.trackRead(s.src)
	return s.value
}

// Set writes a new value and notifies subscribers. Writing a signal that
// the writer itself depends on is recorded in the causal chain and, once
// the resulting cascade exceeds the iteration threshold, blocked by the
// guard with a CircularSignalUpdate panic.
func (s *Signal) Set(v interface{}) {
	s.value = v
	s.guard.write(s.src)
}

// Name returns the signal's report name.
func (s *Signal) Name() string { return s.src.name }

// Computed is a lazily evaluated, cached derivation. Its value is
// recomputed on demand when any transitive dependency changed. A Computed
// that (transitively) reads itself is blocked by the guard's call-stack
// cycle detection.
type Computed struct {
	guard *Guard
	node  *node
	value interface{}
}

// NewComputed creates a Computed with the given name and derivation.
// The derivation runs lazily on first Get.
func NewComputed(g *Guard, name string, derive func() interface{}) *Computed {
	c := &Computed{guard: g}
	src := &source{kind: "computed", name: name, subs: make(map[*node]bool)}
	c.node = &node{
		guard: g,
		kind:  kindComputed,
		name:  name,
		deps:  make(map[*source]bool),
		dirty: true,
		src:   src,
	}
	c.node.fn = func() interface{} { return derive() }
	return c
}

// Get returns the cached value, recomputing first if any dependency
// changed. Re-entering the same Computed while it evaluates panics with
// a CircularSignalUpdate call-stack cycle report.
func (c *Computed) Get() interface{} {
	c.guard.trackRead(c.node.src)
	if c.node.dirty {
		// dirty is cleared only after a successful recompute, so a
		// re-entrant Get (circular dependency) re-enters run(), where
		// the guard's call-stack cycle detection blocks it.
		v := c.node.run()
		c.value = v
		c.node.dirty = false
	}
	return c.value
}

// Name returns the computed's report name.
func (c *Computed) Name() string { return c.node.src.name }

// Effect registers a reactive side-effect on the Guard and runs it
// immediately. The effect re-runs whenever any dependency read during
// its last run is written. If the effect keeps re-triggering itself the
// guard blocks it at the iteration threshold.
func (g *Guard) Effect(name string, fn func()) *node {
	n := &node{
		guard: g,
		kind:  kindEffect,
		name:  name,
		deps:  make(map[*source]bool),
	}
	n.fn = func() interface{} {
		fn()
		return nil
	}
	n.run()
	g.maybeFlush()
	return n
}
