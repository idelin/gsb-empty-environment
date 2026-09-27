package desim

import (
	"container/heap"
	"fmt"
)

// Handler processes a fired event. Data is the payload given at
// schedule time.
type Handler func(s *Scheduler, data interface{})

type slotState struct {
	Gen       uint32
	Cancelled bool
	Live      bool
}

// Scheduler is a deterministic discrete-event scheduler. Time is an
// integer tick; events at the same tick are ordered by category and
// then by a monotonically increasing sequence number. No real time or
// real network traffic is involved.
type Scheduler struct {
	// Env is an opaque pointer handlers can use to reach the model.
	Env interface{}

	now      uint64
	seq      uint64
	events   eventHeap
	slots    []slotState
	free     []int
	handlers map[string]Handler
}

// NewScheduler returns an empty scheduler. env is stored in Env.
func NewScheduler(env interface{}) *Scheduler {
	return &Scheduler{Env: env, handlers: make(map[string]Handler)}
}

// Now returns the current simulation tick.
func (s *Scheduler) Now() uint64 { return s.now }

// Pending returns the number of events still in the queue, including
// cancelled ones that have not been reaped yet.
func (s *Scheduler) Pending() int { return len(s.events) }

// Register binds an event type name to a handler. It panics on a
// duplicate registration.
func (s *Scheduler) Register(typ string, h Handler) {
	if _, dup := s.handlers[typ]; dup {
		panic("desim: duplicate handler for " + typ)
	}
	s.handlers[typ] = h
}

// ScheduleAt schedules an event for the given absolute tick. It
// panics if tick is in the past or the type is unregistered.
func (s *Scheduler) ScheduleAt(tick uint64, cat Category, typ string, data interface{}) Handle {
	if tick < s.now {
		panic(fmt.Sprintf("desim: schedule in the past: tick %d < now %d", tick, s.now))
	}
	if _, ok := s.handlers[typ]; !ok {
		panic("desim: unregistered event type " + typ)
	}
	slot := s.allocSlot()
	s.seq++
	ev := &Event{
		Tick: tick, Cat: cat, Seq: s.seq,
		Type: typ, Data: data,
		slot: slot, gen: s.slots[slot].Gen,
	}
	heap.Push(&s.events, ev)
	return Handle{slot: slot, gen: ev.gen, valid: true}
}

// After schedules an event d ticks in the future.
func (s *Scheduler) After(d uint64, cat Category, typ string, data interface{}) Handle {
	return s.ScheduleAt(s.now+d, cat, typ, data)
}

// Cancel cancels the event referenced by h. It reports whether the
// cancellation took effect. A cancelled event never fires. Cancelling
// twice, cancelling an already-consumed event, or cancelling with a
// stale handle whose slot has been recycled all return false.
func (s *Scheduler) Cancel(h Handle) bool {
	if !h.valid || h.slot < 0 || h.slot >= len(s.slots) {
		return false
	}
	st := &s.slots[h.slot]
	if !st.Live || st.Cancelled || st.Gen != h.gen {
		return false
	}
	st.Cancelled = true
	return true
}

func (s *Scheduler) allocSlot() int {
	n := len(s.free)
	if n > 0 {
		slot := s.free[n-1]
		s.free = s.free[:n-1]
		s.slots[slot].Live = true
		s.slots[slot].Cancelled = false
		return slot
	}
	s.slots = append(s.slots, slotState{Gen: 1, Live: true})
	return len(s.slots) - 1
}

// Step processes the next live event and returns true, or returns
// false if no events remain. Cancelled events are reaped silently.
func (s *Scheduler) Step() bool {
	for len(s.events) > 0 {
		ev := heap.Pop(&s.events).(*Event)
		st := &s.slots[ev.slot]
		live := st.Live && !st.Cancelled && st.Gen == ev.gen
		// Recycle the slot with a bumped generation so any handle
		// to this event becomes stale.
		st.Live = false
		st.Cancelled = false
		st.Gen++
		s.free = append(s.free, ev.slot)
		if !live {
			continue
		}
		s.now = ev.Tick
		s.handlers[ev.Type](s, ev.Data)
		return true
	}
	return false
}

// Run processes events until the queue is empty.
func (s *Scheduler) Run() {
	for s.Step() {
	}
}

// RunUntil processes every event with Tick <= limit, then advances
// the clock to limit. Events scheduled from within handlers at a tick
// <= limit are processed too, in the usual (tick, category, seq)
// order.
func (s *Scheduler) RunUntil(limit uint64) {
	for len(s.events) > 0 && s.events[0].Tick <= limit {
		s.Step()
	}
	if s.now < limit {
		s.now = limit
	}
}
