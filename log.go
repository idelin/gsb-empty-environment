package kvsm

import "fmt"

// Log is the local deterministic event log. It is append-only; entries are
// assigned strictly increasing sequence numbers. Memory is bounded by
// truncating entries at or below a snapshot point.
type Log struct {
	base   uint64 // seq of events[0]; 0 means empty
	events []Event
	next   uint64
}

// NewLog creates an empty log starting at sequence 1.
func NewLog() *Log {
	return &Log{next: 1}
}

// Append assigns the next sequence number and stores the event.
func (l *Log) Append(ev Event) Event {
	ev.Seq = l.next
	l.next++
	if len(l.events) == 0 {
		l.base = ev.Seq
	}
	l.events = append(l.events, ev)
	return ev
}

// Len returns the number of retained (not yet truncated) events.
func (l *Log) Len() int { return len(l.events) }

// NextSeq returns the sequence number the next append will assign.
func (l *Log) NextSeq() uint64 { return l.next }

// Events returns a copy of all retained events in sequence order.
func (l *Log) Events() []Event {
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}

// From returns retained events with Seq > seq, in order. Used to replay the
// suffix of the log after a snapshot slice point.
func (l *Log) From(seq uint64) []Event {
	var out []Event
	for _, ev := range l.events {
		if ev.Seq > seq {
			out = append(out, ev)
		}
	}
	return out
}

// Truncate drops all events with Seq <= uptoSeq (the snapshot slice point).
// It is an error to truncate beyond the last appended event.
func (l *Log) Truncate(uptoSeq uint64) error {
	if len(l.events) == 0 {
		return nil
	}
	last := l.events[len(l.events)-1].Seq
	if uptoSeq > last {
		return fmt.Errorf("kvsm: truncate seq %d beyond last event %d", uptoSeq, last)
	}
	keep := l.From(uptoSeq)
	l.events = keep
	if len(keep) == 0 {
		l.base = 0
	} else {
		l.base = keep[0].Seq
	}
	return nil
}
