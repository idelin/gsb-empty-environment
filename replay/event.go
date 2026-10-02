package replay

// EventType classifies recorded log entries.
type EventType uint8

const (
	// EventSeed records the random seed that drives all kernel-internal
	// nondeterminism. It is always the first entry of a log.
	EventSeed EventType = iota
	// EventPacketArrival is an external input: a network packet arrival.
	EventPacketArrival
	// EventTimeout is a clock interrupt fired by a retransmission timer.
	EventTimeout
)

func (t EventType) String() string {
	switch t {
	case EventSeed:
		return "Seed"
	case EventPacketArrival:
		return "PacketArrival"
	case EventTimeout:
		return "Timeout"
	}
	return "Unknown"
}

// Event is a single entry in the deterministic event log. Everything the
// kernel needs to reproduce a state transition is captured here.
type Event struct {
	Tick    uint64    // virtual clock value at which the event fires
	Type    EventType // event classification
	Seq     uint64    // packet sequence number / timer id
	Payload []byte    // packet bytes or seed bytes
}

// EventLog is the append-only record of every external input, clock
// interrupt and random seed observed by the kernel.
type EventLog struct {
	Events []Event
}

// Append records e at the end of the log.
func (l *EventLog) Append(e Event) {
	l.Events = append(l.Events, e)
}

// Len returns the number of recorded events.
func (l *EventLog) Len() int {
	return len(l.Events)
}

// Count returns how many events of type t the log contains.
func (l *EventLog) Count(t EventType) int {
	n := 0
	for _, e := range l.Events {
		if e.Type == t {
			n++
		}
	}
	return n
}
