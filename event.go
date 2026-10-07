package perflib

// EventKind distinguishes call boundaries.
type EventKind int

const (
	EventStart EventKind = iota
	EventEnd
)

// Event is a single start/end boundary of a call on some thread.
// Time is a monotonic timestamp in nanoseconds.
type Event struct {
	Kind   EventKind
	Name   string
	Thread uint64
	Time   int64
}

// Start returns a start boundary event.
func Start(name string, thread uint64, t int64) Event {
	return Event{Kind: EventStart, Name: name, Thread: thread, Time: t}
}

// End returns an end boundary event.
func End(name string, thread uint64, t int64) Event {
	return Event{Kind: EventEnd, Name: name, Thread: thread, Time: t}
}
