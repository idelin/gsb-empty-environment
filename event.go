package main

// Kind identifies the role of an event in a packet's causal history.
type Kind int

const (
	KindEnqueue Kind = iota
	KindDequeue
	KindSendStart
	KindSendEnd
	KindRecv
	KindTimeout
	KindDrop
	KindDuplicate
	KindDeliver
	KindDiscard
)

func (k Kind) String() string {
	switch k {
	case KindEnqueue:
		return "enqueue"
	case KindDequeue:
		return "dequeue"
	case KindSendStart:
		return "send_start"
	case KindSendEnd:
		return "send_end"
	case KindRecv:
		return "recv"
	case KindTimeout:
		return "timeout"
	case KindDrop:
		return "drop"
	case KindDuplicate:
		return "duplicate"
	case KindDeliver:
		return "deliver"
	case KindDiscard:
		return "discard"
	}
	return "unknown"
}

// Event is one node in the causal DAG. Parents holds the IDs of events that
// caused this one (duplication => 1 parent, merge => many parents).
type Event struct {
	ID      int
	Tick    int64
	Kind    Kind
	Packet  int
	Attempt int
	Link    string
	Parents []int
	Note    string
}

// Trace is an incrementally extendable set of events with causal edges.
type Trace struct {
	events map[int]Event
	nextID int
}

func NewTrace() *Trace {
	return &Trace{events: make(map[int]Event), nextID: 1}
}

// Add appends an event and returns its assigned ID. IDs are stable and never
// reused, so previously recorded parent references stay valid.
func (t *Trace) Add(e Event) int {
	e.ID = t.nextID
	t.nextID++
	t.events[e.ID] = e
	return e.ID
}

func (t *Trace) Get(id int) (Event, bool) {
	e, ok := t.events[id]
	return e, ok
}

func (t *Trace) Len() int { return len(t.events) }

// Events returns all events sorted by ID (insertion order).
func (t *Trace) Events() []Event {
	out := make([]Event, 0, len(t.events))
	for id := 1; id < t.nextID; id++ {
		if e, ok := t.events[id]; ok {
			out = append(out, e)
		}
	}
	return out
}
