package pktscale

import "container/heap"

// Link is a point-to-point link in the discrete-event model. Bandwidth
// is bytes per tick; Delay is propagation ticks. All times are integer
// ticks; no real network traffic is generated.
type Link struct {
	Bandwidth int64
	Delay     int64

	busyUntil int64
}

// Arrival records a fragment leaving the link at Tick.
type Arrival struct {
	Tick int64
	Frag Fragment
}

type event struct {
	tick int64
	seq  int64
	frag Fragment
	link *Link
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Sim is a discrete-event simulator over integer ticks.
type Sim struct {
	events   eventHeap
	seq      int64
	Arrivals []Arrival
}

// Submit schedules a fragment to enter link at its planned Tick.
func (s *Sim) Submit(link *Link, frag Fragment) {
	heap.Push(&s.events, event{tick: frag.Tick, seq: s.seq, frag: frag, link: link})
	s.seq++
}

// Run drains the event queue. A fragment entering a busy link is
// serialized: its transmission starts at max(arrival, busyUntil), takes
// ceil(size/bandwidth) ticks, and arrives Delay ticks later.
func (s *Sim) Run() {
	for len(s.events) > 0 {
		e := heap.Pop(&s.events).(event)
		start := e.tick
		if e.link.busyUntil > start {
			start = e.link.busyUntil
		}
		tx := (e.frag.Size + e.link.Bandwidth - 1) / e.link.Bandwidth
		e.link.busyUntil = start + tx
		s.Arrivals = append(s.Arrivals, Arrival{
			Tick: e.link.busyUntil + e.link.Delay,
			Frag: e.frag,
		})
	}
}
