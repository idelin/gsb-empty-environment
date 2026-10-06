package prof

// EventType discriminates start and end events of a function call.
type EventType int

const (
	EventStart EventType = iota
	EventEnd
)

// Event is a single raw profiling event. Events belonging to the same
// thread must strictly pair in stack (LIFO) order. Async child tasks are
// expected to emit their events on a different Thread.
type Event struct {
	Type   EventType
	Thread string
	Func   string
	Time   int64 // monotonic timestamp in arbitrary units (e.g. nanoseconds)
}
