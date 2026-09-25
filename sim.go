package causal

import (
	"container/heap"
	"fmt"
	"math/rand"
)

// DeliveryRecord is one delivery event, logged as evidence.
type DeliveryRecord struct {
	Node  int
	Msg   Message
	Time  int64
	Order int // per-node delivery position (0-based)
}

func (r DeliveryRecord) String() string {
	return fmt.Sprintf("t=%-4d node=n%d deliver %s (local order %d)", r.Time, r.Node, r.Msg, r.Order)
}

// BackpressureEvent records a rejected receive (buffer full).
type BackpressureEvent struct {
	Node int
	Msg  Message
	Time int64
}

// Faults decides when (and whether) a message reaches a peer.
// Communication, faults and time are all controlled here, in-process.
type Faults interface {
	// Plan returns the absolute virtual times at which m will be delivered
	// to node `to`. Empty slice means the message is lost. More than one
	// entry means duplicates are injected.
	Plan(m Message, to int, now int64, rng *rand.Rand) []int64
}

// Reliable delivers everything after a fixed delay. Zero-loss, zero-dup.
type Reliable struct{ Delay int64 }

func (f Reliable) Plan(m Message, to int, now int64, rng *rand.Rand) []int64 {
	d := f.Delay
	if d <= 0 {
		d = 1
	}
	return []int64{now + d}
}

// Chaos injects random delays (reordering) and duplicates, but never drops:
// suitable for liveness evidence. Deterministic under a fixed seed.
type Chaos struct {
	MinDelay int64
	MaxDelay int64
	DupRate  float64 // probability of one extra duplicate copy
}

func (f Chaos) Plan(m Message, to int, now int64, rng *rand.Rand) []int64 {
	span := f.MaxDelay - f.MinDelay + 1
	if span <= 0 {
		span = 1
	}
	d := f.MinDelay + rng.Int63n(span)
	if d <= 0 {
		d = 1
	}
	times := []int64{now + d}
	if rng.Float64() < f.DupRate {
		times = append(times, now+d+1+rng.Int63n(span))
	}
	return times
}

// Drop wraps another Faults and drops messages for which If returns true.
// Used to model permanent loss of a specific predecessor.
type Drop struct {
	Inner Faults
	If    func(m Message, to int) bool
}

func (f Drop) Plan(m Message, to int, now int64, rng *rand.Rand) []int64 {
	if f.If != nil && f.If(m, to) {
		return nil
	}
	return f.Inner.Plan(m, to, now, rng)
}

// event kinds
const (
	evSend = iota
	evDeliver
)

type event struct {
	time    int64
	seq     int64
	kind    int
	from    int
	payload string
	to      int
	msg     Message
}

type eventQueue []event

func (q eventQueue) Len() int { return len(q) }
func (q eventQueue) Less(i, j int) bool {
	if q[i].time != q[j].time {
		return q[i].time < q[j].time
	}
	return q[i].seq < q[j].seq
}
func (q eventQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *eventQueue) Push(x interface{}) { *q = append(*q, x.(event)) }
func (q *eventQueue) Pop() interface{} {
	old := *q
	n := len(old)
	e := old[n-1]
	*q = old[:n-1]
	return e
}

// Simulator is a single-process, deterministic event simulator. Virtual
// time advances only when an event is processed; event order at equal
// timestamps is fixed by a sequence counter, so runs are reproducible.
type Simulator struct {
	Now    int64
	Nodes  []*Node
	Faults Faults

	// RetryOnBackpressure makes the simulator hold a rejected message and
	// retry it after RetryDelay, instead of losing it.
	RetryOnBackpressure bool
	RetryDelay          int64
	// MaxEvents bounds a run (guard against infinite retry loops).
	MaxEvents int

	Log          []DeliveryRecord
	Backpressure []BackpressureEvent

	rng     *rand.Rand
	queue   eventQueue
	seq     int64
	perNode []int
}

// NewSimulator creates a fixed membership set of `members` nodes.
func NewSimulator(members, bufferCap int, faults Faults, seed int64) *Simulator {
	s := &Simulator{
		Faults:     faults,
		rng:        rand.New(rand.NewSource(seed)),
		RetryDelay: 5,
		MaxEvents:  100000,
	}
	for i := 0; i < members; i++ {
		s.Nodes = append(s.Nodes, NewNode(i, members, bufferCap))
	}
	s.perNode = make([]int, members)
	return s
}

func (s *Simulator) push(e event) {
	e.seq = s.seq
	s.seq++
	heap.Push(&s.queue, e)
}

// Send schedules node `from` to broadcast a message at virtual time `at`.
func (s *Simulator) Send(at int64, from int, payload string) {
	s.push(event{time: at, kind: evSend, from: from, payload: payload})
}

// Run processes events until the queue is empty or MaxEvents is hit.
func (s *Simulator) Run() {
	processed := 0
	for len(s.queue) > 0 {
		if processed >= s.MaxEvents {
			return
		}
		processed++
		e := heap.Pop(&s.queue).(event)
		s.Now = e.time
		switch e.kind {
		case evSend:
			s.doSend(e.from, e.payload)
		case evDeliver:
			s.doDeliver(e.to, e.msg)
		}
	}
}

func (s *Simulator) doSend(from int, payload string) {
	m := s.Nodes[from].Send(payload)
	for to := range s.Nodes {
		if to == from {
			// Sender applied its own message in Send; just log it.
			s.record(from, m)
			continue
		}
		for _, at := range s.Faults.Plan(m, to, s.Now, s.rng) {
			s.push(event{time: at, kind: evDeliver, to: to, msg: m})
		}
	}
}

func (s *Simulator) doDeliver(to int, m Message) {
	delivered, err := s.Nodes[to].Receive(m)
	if err == ErrBackpressure {
		s.Backpressure = append(s.Backpressure, BackpressureEvent{Node: to, Msg: m, Time: s.Now})
		if s.RetryOnBackpressure {
			delay := s.RetryDelay
			if delay <= 0 {
				delay = 1
			}
			s.push(event{time: s.Now + delay, kind: evDeliver, to: to, msg: m})
		}
		return
	}
	if err != nil {
		return
	}
	for _, d := range delivered {
		s.record(to, d)
	}
}

func (s *Simulator) record(node int, m Message) {
	s.Log = append(s.Log, DeliveryRecord{
		Node:  node,
		Msg:   m,
		Time:  s.Now,
		Order: s.perNode[node],
	})
	s.perNode[node]++
}
