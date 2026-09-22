// Package sim implements a single-process, deterministic event simulator.
//
// All communication, failures (crashes/restarts) and the passage of time are
// controlled here. Protocol code never blocks: every effect (message send,
// persistence flush, periodic heartbeat, client retry) is scheduled as an
// Event on one logical time line.
package sim

import (
	"container/heap"
	"math/rand"
)

// Event kinds.
const (
	EvtDeliver = iota // a message reached its destination
	EvtTimer          // a node-local timer fired
	EvtRestart        // restart timer fires even while the node is crashed
)

// Event is one scheduled occurrence on the simulator time line.
type Event struct {
	Time  int64
	Seq   int64
	Kind  int
	To    int
	Timer string
	Msg   *Message
	seq   int64 // internal: unique tie-breaker, equals Seq
}

// eventHeap is a min-heap ordered by (Time, Seq).
type eventHeap []*Event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	return a.Time < b.Time || a.Time == b.Time && a.Seq < b.Seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(*Event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Link describes the directional network link src -> dst.
type Link struct {
	BaseDelay int64 // deterministic latency added to every delivered message
	Jitter    int64 // uniform extra delay in [0, Jitter)
	LossPct   int   // drop probability in percent
	Down      bool  // administratively down (partition/crash isolation)
}

// Simulator owns the logical clock, event queue and failure model.
type Simulator struct {
	now        int64
	seq        int64
	q          eventHeap
	links      map[[2]int]*Link
	rng        *rand.Rand
	alive      map[int]bool
	nodes      int
	OnDeliver  func(t int64, to int, m *Message)
	OnTimer    func(t int64, to int, name string)
	OnCheck    func(t int64)
	OnCallback func(t int64, name string)
}

// New creates a simulator for n nodes with a fixed seed (deterministic runs).
func New(n int, seed int64) *Simulator {
	s := &Simulator{
		links: map[[2]int]*Link{},
		rng:   rand.New(rand.NewSource(seed)),
		alive: map[int]bool{},
		nodes: n,
	}
	for i := 0; i < n; i++ {
		s.alive[i] = true
		for j := 0; j < n; j++ {
			if i != j {
				s.links[[2]int{i, j}] = &Link{BaseDelay: 5}
			}
		}
	}
	heap.Init(&s.q)
	return s
}

// Now returns the current logical time (nanoseconds).
func (s *Simulator) Now() int64 { return s.now }

// Link returns the link description src -> dst for tuning in tests.
func (s *Simulator) Link(src, dst int) *Link { return s.links[[2]int{src, dst}] }

// Schedule a node-local timer.
func (s *Simulator) Schedule(delay int64, to int, name string) *Event {
	s.seq++
	e := &Event{Time: s.now + delay, Seq: s.seq, Kind: EvtTimer, To: to, Timer: name}
	heap.Push(&s.q, e)
	return e
}

// ScheduleRestart arms a timer that fires even while its node is crashed;
// it is how the failure model brings a process back from durable storage.
func (s *Simulator) ScheduleRestart(delay int64, to int, name string) *Event {
	s.seq++
	e := &Event{Time: s.now + delay, Seq: s.seq, Kind: EvtRestart, To: to, Timer: name}
	heap.Push(&s.q, e)
	return e
}

// Send enqueues a message. Messages to crashed nodes or over down/lossy links
// are dropped here, so protocol code never observes undelivered traffic.
func (s *Simulator) Send(from int, m *Message) {
	dst := m.To
	if !s.alive[from] || !s.alive[dst] {
		return
	}
	l := s.links[[2]int{from, dst}]
	if l == nil || l.Down {
		return
	}
	if l.LossPct > 0 && s.rng.Intn(100) < l.LossPct {
		return
	}
	delay := l.BaseDelay
	if l.Jitter > 0 {
		delay += s.rng.Int63n(l.Jitter)
	}
	s.seq++
	cp := *m
	e := &Event{Time: s.now + delay, Seq: s.seq, Kind: EvtDeliver, To: dst, Msg: &cp}
	heap.Push(&s.q, e)
}

// Partition isolates node from every other node (both directions).
func (s *Simulator) Partition(node int) {
	for other := 0; other < s.nodes; other++ {
		if other == node {
			continue
		}
		if l := s.links[[2]int{node, other}]; l != nil {
			l.Down = true
		}
		if l := s.links[[2]int{other, node}]; l != nil {
			l.Down = true
		}
	}
}

// Heal restores all links touching node.
func (s *Simulator) Heal(node int) {
	for other := 0; other < s.nodes; other++ {
		if other == node {
			continue
		}
		if l := s.links[[2]int{node, other}]; l != nil {
			l.Down = false
		}
		if l := s.links[[2]int{other, node}]; l != nil {
			l.Down = false
		}
	}
}

// Alive reports whether the node is up.
func (s *Simulator) Alive(id int) bool { return s.alive[id] }

// Crash stops a node: all its in-flight messages are discarded at send points
// and future timers are ignored until Restart.
func (s *Simulator) Crash(id int) { s.alive[id] = false }

// Restart brings a node back; its durable state survives in the protocol layer.
func (s *Simulator) Restart(id int) {
	s.alive[id] = true
}

// Pending reports queued events at or before limit.
func (s *Simulator) Pending(limit int64) bool {
	return s.q.Len() > 0 && s.q[0].Time <= limit
}

// RunUntil advances logical time, dispatching events, until no event remains
// with Time <= limit. checkEvery, if > 0, invokes OnCheck at that granularity.
func (s *Simulator) RunUntil(limit, checkEvery int64) {
	for s.q.Len() > 0 && s.q[0].Time <= limit {
		e := heap.Pop(&s.q).(*Event)
		s.now = e.Time
		if e.Kind == EvtDeliver {
			if s.alive[e.To] && s.OnDeliver != nil {
				s.OnDeliver(e.Time, e.To, e.Msg)
			}
		} else if e.To < 0 {
			if s.OnCallback != nil {
				s.OnCallback(e.Time, e.Timer)
			}
		} else if e.Kind == EvtRestart {
			if s.OnTimer != nil {
				s.OnTimer(e.Time, e.To, e.Timer)
			}
		} else if s.alive[e.To] && s.OnTimer != nil {
			s.OnTimer(e.Time, e.To, e.Timer)
		}
		if checkEvery > 0 && s.OnCheck != nil {
			s.OnCheck(e.Time)
		}
	}
	if s.now < limit {
		s.now = limit
	}
	if checkEvery > 0 && s.OnCheck != nil {
		s.OnCheck(s.now)
	}
}
