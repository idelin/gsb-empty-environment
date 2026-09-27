package reordersim

import "container/heap"

// Action is a piece of work executed at a tick.
type Action func()

// Tickable is notified once per tick, before that tick's events run.
type Tickable interface {
	OnTick(t int64)
}

type event struct {
	tick  int64
	order int // FIFO tie-break inside one tick
	act   Action
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].order < h[j].order
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

// Simulator is a discrete-event scheduler over integer ticks. It carries no
// real network traffic: packets, queues and links exist only as Go values
// and scheduled actions.
type Simulator struct {
	now       int64
	order     int
	pq        eventHeap
	tickables []Tickable
}

// NewSimulator returns a simulator whose clock starts at tick 0.
func NewSimulator() *Simulator { return &Simulator{} }

// Now reports the current tick.
func (s *Simulator) Now() int64 { return s.now }

// AddTickable registers t to receive OnTick once per tick during Run.
func (s *Simulator) AddTickable(t Tickable) { s.tickables = append(s.tickables, t) }

// Schedule runs act at the given tick. Scheduling in the past panics.
func (s *Simulator) Schedule(tick int64, act Action) {
	if tick < s.now {
		panic("reordersim: schedule in the past")
	}
	heap.Push(&s.pq, event{tick: tick, order: s.order, act: act})
	s.order++
}

// After schedules act delay ticks from now.
func (s *Simulator) After(delay int64, act Action) { s.Schedule(s.now+delay, act) }

// Run processes every tick from the current one until and including until.
// Each tick first notifies tickables (so timers like the wait limit fire),
// then runs that tick's events in FIFO order. Actions may schedule more
// events at the same tick (delay 0); they run within the same tick.
func (s *Simulator) Run(until int64) {
	for {
		for _, t := range s.tickables {
			t.OnTick(s.now)
		}
		for len(s.pq) > 0 && s.pq[0].tick == s.now {
			e := heap.Pop(&s.pq).(event)
			e.act()
		}
		if s.now >= until {
			break
		}
		s.now++
	}
}

// Pending reports how many events are still scheduled beyond the clock.
func (s *Simulator) Pending() int { return len(s.pq) }

// Link is a point-to-point delay line inside the simulation. Every packet
// sent through it is delivered to To after Delay(p) ticks.
type Link struct {
	Sim   *Simulator
	Delay func(p Packet) int64
	To    func(p Packet)
}

// Send injects a packet into the link.
func (l *Link) Send(p Packet) {
	d := l.Delay(p)
	l.Sim.After(d, func() { l.To(p) })
}

// Source emits packets into a link at scripted ticks.
type Source struct {
	Sim *Simulator
	Out *Link
}

// SendAt schedules packet p to enter the link at the given tick.
func (s *Source) SendAt(tick int64, p Packet) {
	s.Sim.Schedule(tick, func() { s.Out.Send(p) })
}
