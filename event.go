package desim

import "container/heap"

// Category is the event class used as the first tie-breaker for events
// scheduled at the same tick. Lower values fire first:
//
//	CatPacket (packet arrivals) < CatLink (link tx completion) < CatTimer
//
// Within the same tick and category, events fire in scheduling order (Seq).
// Events created while dispatching an event at tick T therefore always run
// after all events of the same category already pending at tick T.
type Category int

const (
	CatPacket Category = iota
	CatLink
	CatTimer
)

func (c Category) String() string {
	switch c {
	case CatPacket:
		return "packet"
	case CatLink:
		return "link"
	case CatTimer:
		return "timer"
	}
	return "unknown"
}

// EventKind identifies what an event means. Handlers are plain code in
// Sim.dispatch, so events stay pure data and can be gob-checkpointed.
type EventKind uint8

const (
	EvPacketArrive EventKind = iota
	EvLinkFree
	EvTimer
)

// Payload carries everything any event kind may need.
type Payload struct {
	Packet  Packet
	NodeID  int
	LinkID  int
	TimerID int
}

// Event is a scheduled occurrence at an integer tick.
type Event struct {
	Tick uint64
	Cat  Category
	Seq  uint64
	Kind EventKind
	Data Payload
}

// Handle is a generational handle to a scheduled event. A handle only
// matches the slot it was issued for while the slot generation is unchanged;
// firing or cancelling the event bumps the generation, so stale handles can
// never cancel (or be confused with) a different event that reuses the slot.
type Handle struct {
	Slot int
	Gen  uint64
}

type slot struct {
	occupied bool
	gen      uint64
	ev       Event
}

type heapEntry struct {
	slot int
	gen  uint64
	tick uint64
	cat  Category
	seq  uint64
}

type entryHeap []heapEntry

func (h entryHeap) Len() int { return len(h) }

func (h entryHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.tick != b.tick {
		return a.tick < b.tick
	}
	if a.cat != b.cat {
		return a.cat < b.cat
	}
	return a.seq < b.seq
}

func (h entryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *entryHeap) Push(x any) { *h = append(*h, x.(heapEntry)) }

func (h *entryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Scheduler is a unified discrete-event queue for timers, link events and
// packet arrivals. Time is an integer tick; no real time or network I/O is
// involved.
type Scheduler struct {
	now   uint64
	seq   uint64
	live  int
	slots []slot
	free  []int
	pq    entryHeap
}

func NewScheduler() *Scheduler { return &Scheduler{} }

// Now returns the tick of the most recently dispatched event.
func (s *Scheduler) Now() uint64 { return s.now }

// Live returns the number of pending (not cancelled, not fired) events.
func (s *Scheduler) Live() int { return s.live }

// ScheduleAt schedules an event at an absolute tick.
func (s *Scheduler) ScheduleAt(tick uint64, cat Category, kind EventKind, p Payload) Handle {
	if tick < s.now {
		panic("desim: cannot schedule in the past")
	}
	idx := -1
	if n := len(s.free); n > 0 {
		idx = s.free[n-1]
		s.free = s.free[:n-1]
	} else {
		idx = len(s.slots)
		s.slots = append(s.slots, slot{})
	}
	sl := &s.slots[idx]
	sl.occupied = true
	sl.ev = Event{Tick: tick, Cat: cat, Seq: s.seq, Kind: kind, Data: p}
	s.seq++
	s.live++
	heap.Push(&s.pq, heapEntry{slot: idx, gen: sl.gen, tick: tick, cat: cat, seq: sl.ev.Seq})
	return Handle{Slot: idx, Gen: sl.gen}
}

// ScheduleAfter schedules an event delay ticks from Now.
func (s *Scheduler) ScheduleAfter(delay uint64, cat Category, kind EventKind, p Payload) Handle {
	return s.ScheduleAt(s.now+delay, cat, kind, p)
}

// Cancel invalidates the event referred to by h. It returns false if the
// handle is stale (event already fired or cancelled, or the slot has been
// recycled). A cancelled event never fires.
func (s *Scheduler) Cancel(h Handle) bool {
	if h.Slot < 0 || h.Slot >= len(s.slots) {
		return false
	}
	sl := &s.slots[h.Slot]
	if !sl.occupied || sl.gen != h.Gen {
		return false
	}
	s.release(h.Slot)
	return true
}

// release frees a slot and bumps its generation so outstanding handles
// become stale.
func (s *Scheduler) release(idx int) {
	sl := &s.slots[idx]
	sl.occupied = false
	sl.gen++
	s.free = append(s.free, idx)
	s.live--
}

// cleanTop discards stale heap entries at the top (cancelled or fired).
func (s *Scheduler) cleanTop() {
	for len(s.pq) > 0 {
		e := s.pq[0]
		sl := &s.slots[e.slot]
		if sl.occupied && sl.gen == e.gen {
			return
		}
		heap.Pop(&s.pq)
	}
}

// Peek returns the next event without consuming it.
func (s *Scheduler) Peek() (Event, bool) {
	s.cleanTop()
	if len(s.pq) == 0 {
		return Event{}, false
	}
	return s.slots[s.pq[0].slot].ev, true
}

// Next consumes and returns the next event, advancing Now to its tick.
// Cancelled events are skipped and never returned.
func (s *Scheduler) Next() (Event, bool) {
	for len(s.pq) > 0 {
		e := heap.Pop(&s.pq).(heapEntry)
		sl := &s.slots[e.slot]
		if !sl.occupied || sl.gen != e.gen {
			continue
		}
		s.release(e.slot)
		s.now = sl.ev.Tick
		return sl.ev, true
	}
	return Event{}, false
}
