package kernel

import "container/heap"

// scheduledEvent wraps an Event with a monotonic sequence number so the
// pop order is fully deterministic even when ticks and kinds tie.
type scheduledEvent struct {
	ev  Event
	seq uint64
}

// eventHeap is a min-heap ordered by (tick, kind, seq).
type eventHeap []scheduledEvent

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.ev.Tick != b.ev.Tick {
		return a.ev.Tick < b.ev.Tick
	}
	if a.ev.Kind != b.ev.Kind {
		return a.ev.Kind < b.ev.Kind
	}
	return a.seq < b.seq
}

func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(x any) {
	*h = append(*h, x.(scheduledEvent))
}

func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// Scheduler is the deterministic event scheduler. Events are popped in
// (tick, kind, seq) order; the virtual clock never goes backwards.
type Scheduler struct {
	now       uint64
	seq       uint64
	h         eventHeap
	cancelled map[uint64]bool // timer IDs of cancelled timeout events
}

// NewScheduler returns an empty scheduler with virtual time 0.
func NewScheduler() *Scheduler {
	return &Scheduler{}
}

// Now returns the current virtual tick.
func (s *Scheduler) Now() uint64 {
	return s.now
}

// Pending returns the number of events still queued.
func (s *Scheduler) Pending() int {
	return len(s.h)
}

// Push schedules ev. The event tick must not lie in the past.
func (s *Scheduler) Push(ev Event) {
	if ev.Tick < s.now {
		panic("scheduler: event scheduled in the past")
	}
	heap.Push(&s.h, scheduledEvent{ev: ev, seq: s.seq})
	s.seq++
}

// CancelTimer cancels the pending timeout event for timerID, mirroring
// how a real kernel deletes a timer when its condition is resolved.
// Cancelled events never fire and never reach the event log.
func (s *Scheduler) CancelTimer(timerID uint64) {
	if s.cancelled == nil {
		s.cancelled = make(map[uint64]bool)
	}
	s.cancelled[timerID] = true
}

// Next removes and returns the earliest live event, advancing virtual
// time. Cancelled timeout events are dropped silently.
func (s *Scheduler) Next() (Event, bool) {
	for len(s.h) > 0 {
		item := heap.Pop(&s.h).(scheduledEvent)
		if item.ev.Kind == KindTimeout && s.cancelled[item.ev.TimerID] {
			delete(s.cancelled, item.ev.TimerID)
			continue
		}
		s.now = item.ev.Tick
		return item.ev, true
	}
	return Event{}, false
}

// pendingSnapshot returns a canonical (sorted) copy of the queued live
// events so snapshots depend neither on internal heap layout nor on the
// order in which events were pushed.
func (s *Scheduler) pendingSnapshot() []scheduledEvent {
	out := make([]scheduledEvent, 0, len(s.h))
	for _, item := range s.h {
		if item.ev.Kind == KindTimeout && s.cancelled[item.ev.TimerID] {
			continue
		}
		out = append(out, item)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1], out[j]
			if eventCanonicalLess(b.ev, a.ev) {
				out[j-1], out[j] = b, a
			} else {
				break
			}
		}
	}
	return out
}

// eventCanonicalLess defines the canonical ordering used for snapshots.
func eventCanonicalLess(a, b Event) bool {
	if a.Tick != b.Tick {
		return a.Tick < b.Tick
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Packet.ID != b.Packet.ID {
		return a.Packet.ID < b.Packet.ID
	}
	return a.TimerID < b.TimerID
}
