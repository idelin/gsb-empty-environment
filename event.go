package kernel

import "fmt"

// EventKind classifies every event that can enter the kernel.
type EventKind uint8

const (
	// KindPacketArrival is an external input: a network packet arrived.
	KindPacketArrival EventKind = iota + 1
	// KindClockInterrupt is an external input: a hardware clock tick.
	KindClockInterrupt
	// KindTimeout is an internal event scheduled by the system itself
	// (retransmission timer expiry). It is re-derived during replay.
	KindTimeout
)

func (k EventKind) String() string {
	switch k {
	case KindPacketArrival:
		return "PacketArrival"
	case KindClockInterrupt:
		return "ClockInterrupt"
	case KindTimeout:
		return "Timeout"
	default:
		return fmt.Sprintf("EventKind(%d)", uint8(k))
	}
}

// External reports whether the event kind originates outside the system
// and therefore must be recorded in the log to make replay possible.
func (k EventKind) External() bool {
	return k == KindPacketArrival || k == KindClockInterrupt
}

// Packet is the payload of a packet-arrival event. Fixed-size fields keep
// the binary encoding trivially deterministic.
type Packet struct {
	ID   uint64
	Size uint32
	Src  uint32
	Dst  uint32
}

// Event is a single entry in the deterministic event sequence.
type Event struct {
	Tick uint64
	Kind EventKind

	// Packet is valid for KindPacketArrival.
	Packet Packet
	// TimerID is valid for KindTimeout and carries the packet ID whose
	// retransmission timer expired.
	TimerID uint64
}

func (e Event) String() string {
	switch e.Kind {
	case KindPacketArrival:
		return fmt.Sprintf("tick=%-3d %-14s id=%d size=%d", e.Tick, e.Kind, e.Packet.ID, e.Packet.Size)
	case KindTimeout:
		return fmt.Sprintf("tick=%-3d %-14s timer=%d", e.Tick, e.Kind, e.TimerID)
	default:
		return fmt.Sprintf("tick=%-3d %-14s", e.Tick, e.Kind)
	}
}

// EventLog is the append-only record of everything needed to reproduce a
// run: the random seed plus every event in the exact order it was applied.
type EventLog struct {
	Seed   uint64
	Events []Event
}

// NewEventLog creates an empty log bound to a random seed.
func NewEventLog(seed uint64) *EventLog {
	return &EventLog{Seed: seed}
}

// Append records an applied event at the end of the log.
func (l *EventLog) Append(ev Event) {
	l.Events = append(l.Events, ev)
}

// Len returns the number of recorded events.
func (l *EventLog) Len() int {
	return len(l.Events)
}

// CountKind counts recorded events of a given kind.
func (l *EventLog) CountKind(kind EventKind) int {
	n := 0
	for _, ev := range l.Events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}
