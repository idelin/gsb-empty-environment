package signalguard

// Signal is a writable reactive value.
type Signal[T any] struct {
	name  string
	value T
	g     *Graph
}

// NewSignal creates a Signal holding initial.
func NewSignal[T any](g *Graph, name string, initial T) *Signal[T] {
	return &Signal[T]{name: name, value: initial, g: g}
}

// Name implements signalNode.
func (s *Signal[T]) Name() string { return "Signal[" + s.name + "]" }

// Get reads the value and registers it as a dependency of the running observer.
func (s *Signal[T]) Get() T {
	s.g.trackRead(s)
	return s.value
}

// Set writes the value and notifies subscribers.
func (s *Signal[T]) Set(v T) {
	s.value = v
	s.g.notify(s, s.g.current())
}
