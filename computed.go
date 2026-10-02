package signalguard

// Computed is a lazily evaluated, memoized derivation. It is both an
// observer (it re-runs when its dependencies change) and a signalNode
// (other observers can depend on it).
type Computed[T any] struct {
	name  string
	fn    func() T
	g     *Graph
	value T
	dirty bool
}

// NewComputed creates a Computed; fn is evaluated on first Get.
func NewComputed[T any](g *Graph, name string, fn func() T) *Computed[T] {
	return &Computed[T]{name: name, fn: fn, g: g, dirty: true}
}

// Name implements both observer and signalNode.
func (c *Computed[T]) Name() string { return "Computed[" + c.name + "]" }

// Get returns the memoized value, recomputing when stale.
func (c *Computed[T]) Get() T {
	c.g.trackRead(c)
	if c.dirty {
		c.g.run(c)
	}
	return c.value
}

func (c *Computed[T]) run() {
	c.value = c.fn()
	c.dirty = false
}

func (c *Computed[T]) invalidate(src signalNode) {
	if c.dirty {
		return // already stale; invalidation has been propagated
	}
	c.dirty = true
	c.g.notify(c, c.g.current())
}
