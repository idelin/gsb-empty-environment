// Package eventlog implements a state machine event log with verifiable
// compression. A compressed segment keeps the initial and final state
// summaries plus every public observation point, and carries a replayable
// list of the original event summaries so a verifier can replay the segment
// and compare the observed values.
package eventlog

// State is the state machine state.
type State struct {
	Value int
}

// OpKind identifies the operation an event performs on the state.
type OpKind int

const (
	OpAdd OpKind = iota
	OpSub
	OpMul
	OpSet
)

func (k OpKind) String() string {
	switch k {
	case OpAdd:
		return "add"
	case OpSub:
		return "sub"
	case OpMul:
		return "mul"
	case OpSet:
		return "set"
	}
	return "unknown"
}

// Event is a single state machine event.
type Event struct {
	Op  OpKind
	Arg int
}

// Apply returns the state after the event.
func (e Event) Apply(s State) State {
	switch e.Op {
	case OpAdd:
		s.Value += e.Arg
	case OpSub:
		s.Value -= e.Arg
	case OpMul:
		s.Value *= e.Arg
	case OpSet:
		s.Value = e.Arg
	}
	return s
}

// Entry is one logged event. Public entries are observation points: Observe
// records the state value right after the event is applied.
type Entry struct {
	Event   Event
	Public  bool
	Observe int
}

// Execute applies events to initial and returns the resulting log entries
// and the final state. Events for which publicFn returns true become public
// observation points.
func Execute(initial State, events []Event, publicFn func(i int, e Event) bool) ([]Entry, State) {
	entries := make([]Entry, 0, len(events))
	state := initial
	for i, e := range events {
		state = e.Apply(state)
		entry := Entry{Event: e}
		if publicFn != nil && publicFn(i, e) {
			entry.Public = true
			entry.Observe = state.Value
		}
		entries = append(entries, entry)
	}
	return entries, state
}
