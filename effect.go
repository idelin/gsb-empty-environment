package signalguard

// Effect runs fn immediately and re-runs it whenever any dependency read
// during the previous run changes.
type Effect struct {
	name string
	fn   func()
	g    *Graph
}

// NewEffect creates an Effect and runs it once.
func NewEffect(g *Graph, name string, fn func()) *Effect {
	e := &Effect{name: name, fn: fn, g: g}
	g.run(e)
	return e
}

// Name implements observer.
func (e *Effect) Name() string { return "Effect[" + e.name + "]" }

func (e *Effect) run() { e.fn() }

func (e *Effect) invalidate(src signalNode) { e.g.schedule(e) }
