package jointpaxos

import (
	"container/heap"
	"fmt"
)

// Event is a scheduled action at a virtual time.
type Event struct {
	time  int
	seq   int
	apply func()
}

type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(Event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Sim is a single-process discrete-event simulator. It owns the virtual
// clock, the event queue, and (via Network) all message delivery, so
// communication, faults and time are fully controlled in one process.
type Sim struct {
	Now    int
	events eventHeap
	seq    int
	Logger func(string)
}

// After schedules f to run delay virtual-time units from now.
func (s *Sim) After(delay int, f func()) {
	s.seq++
	heap.Push(&s.events, Event{time: s.Now + delay, seq: s.seq, apply: f})
}

// Step processes the next event. Returns false when the queue is empty.
func (s *Sim) Step() bool {
	if len(s.events) == 0 {
		return false
	}
	e := heap.Pop(&s.events).(Event)
	s.Now = e.time
	e.apply()
	return true
}

// Run processes events until the queue is empty or virtual time exceeds maxTime.
func (s *Sim) Run(maxTime int) {
	for len(s.events) > 0 && s.events[0].time <= maxTime {
		s.Step()
	}
}

// Logf emits a timestamped log line if a Logger is attached.
func (s *Sim) Logf(format string, args ...interface{}) {
	if s.Logger != nil {
		s.Logger(fmt.Sprintf("[t=%4d] %s", s.Now, fmt.Sprintf(format, args...)))
	}
}

// Network delivers messages between nodes inside the simulator.
// Drop and Delay are fault-injection hooks controlled by the scenario.
type Network struct {
	sim   *Sim
	nodes map[string]*Node
	// Drop, if non-nil and returning true, discards the message.
	Drop func(from, to string, m Msg) bool
	// Delay overrides the per-message delivery delay (default 1).
	Delay func(from, to string, m Msg) int
}

func NewNetwork(s *Sim) *Network {
	return &Network{sim: s, nodes: map[string]*Node{}}
}

// Send queues m for delivery, subject to the fault hooks.
func (n *Network) Send(m Msg) {
	if n.Drop != nil && n.Drop(m.From, m.To, m) {
		n.sim.Logf("net: DROP %s -> %s  %s", m.From, m.To, m.describe())
		return
	}
	d := 1
	if n.Delay != nil {
		d = n.Delay(m.From, m.To, m)
	}
	n.sim.After(d, func() {
		dst := n.nodes[m.To]
		if dst != nil {
			dst.Handle(m)
		}
	})
}
