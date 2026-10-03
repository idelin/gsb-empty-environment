// Package sim implements a deterministic discrete-event network simulator.
//
// Everything runs in a single process on a virtual clock that advances one
// discrete tick at a time. A virtual switch applies per-link packet loss and
// randomized latency jitter using a seeded PRNG, so an entire run is fully
// reproducible from its seed.
package sim

import (
	"container/heap"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"
)

// Event kinds.
const (
	EvMessage = iota // a network message delivery
	EvAction         // a scheduled callback (test orchestration)
)

// Event is a single entry in the simulator's future-event queue. Events are
// processed in strict (Time, Seq) order; Seq is a monotonically increasing
// insertion counter, which makes simultaneous events deterministic.
type Event struct {
	Time uint64
	Seq  uint64
	Kind int
	From string
	To   string
	Msg  any
	Fn   func()
}

// eventHeap is a priority queue ordered by (Time, Seq).
type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].Time != h[j].Time {
		return h[i].Time < h[j].Time
	}
	return h[i].Seq < h[j].Seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(Event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	ev := old[n-1]
	*h = old[:n-1]
	return ev
}

// Node is anything that can participate in the simulated network.
type Node interface {
	ID() string
	// OnTick is invoked once per clock tick, after all message deliveries
	// for that tick. Nodes are ticked in sorted ID order.
	OnTick(now uint64)
	// OnMessage delivers a network message.
	OnMessage(from string, msg any, now uint64)
}

// Switch is a virtual network switch. It applies packet loss and latency
// jitter to every message. All randomness comes from the seeded PRNG given
// at construction, so behaviour is fully deterministic per seed.
type Switch struct {
	rng       *rand.Rand
	loss      float64 // default loss probability for all links
	baseDelay uint64  // minimum delivery delay in ticks (must be >= 1)
	jitter    uint64  // extra uniform random delay in [0, jitter]
	linkLoss  map[[2]string]float64

	Sent    uint64
	Dropped uint64
}

// NewSwitch creates a virtual switch. baseDelay must be >= 1 so that a
// message is never delivered in the same tick it was sent (deliveries for
// the current tick are drained before nodes tick).
func NewSwitch(rng *rand.Rand, loss float64, baseDelay, jitter uint64) *Switch {
	if baseDelay < 1 {
		baseDelay = 1
	}
	return &Switch{
		rng:       rng,
		loss:      loss,
		baseDelay: baseDelay,
		jitter:    jitter,
		linkLoss:  make(map[[2]string]float64),
	}
}

// SetLinkLoss overrides the loss probability for a directed link. Use 1.0 to
// partition a link and 0.0 to heal it.
func (s *Switch) SetLinkLoss(from, to string, rate float64) {
	s.linkLoss[[2]string{from, to}] = rate
}

// Simulator is the discrete-event engine: virtual clock + event queue +
// virtual switch + registered nodes.
type Simulator struct {
	now   uint64
	seq   uint64
	queue eventHeap
	sw    *Switch
	nodes map[string]Node
	order []string

	LogEnabled bool
	Log        []string
}

func NewSimulator(sw *Switch) *Simulator {
	return &Simulator{
		sw:         sw,
		nodes:      make(map[string]Node),
		LogEnabled: true,
	}
}

// Switch exposes the virtual switch so tests can partition/heal links.
func (s *Simulator) Switch() *Switch { return s.sw }

func (s *Simulator) Now() uint64 { return s.now }

// AddNode registers a node. Nodes are ticked in sorted ID order each step.
func (s *Simulator) AddNode(n Node) {
	s.nodes[n.ID()] = n
	s.order = append(s.order, n.ID())
	sort.Strings(s.order)
}

// Send injects a message into the network. The switch decides (seeded PRNG)
// whether the message is dropped and how long it is delayed.
func (s *Simulator) Send(from, to string, msg any) {
	s.sw.Sent++
	rate := s.sw.loss
	if r, ok := s.sw.linkLoss[[2]string{from, to}]; ok {
		rate = r
	}
	if s.sw.rng.Float64() < rate {
		s.sw.Dropped++
		return
	}
	delay := s.sw.baseDelay
	if s.sw.jitter > 0 {
		delay += uint64(s.sw.rng.Int63n(int64(s.sw.jitter) + 1))
	}
	heap.Push(&s.queue, Event{
		Time: s.now + delay,
		Seq:  s.seq,
		Kind: EvMessage,
		From: from,
		To:   to,
		Msg:  msg,
	})
	s.seq++
}

// Schedule registers a callback to run at the start of the given tick,
// before any node ticks. Used by tests to partition/heal the network.
func (s *Simulator) Schedule(at uint64, fn func()) {
	heap.Push(&s.queue, Event{Time: at, Seq: s.seq, Kind: EvAction, Fn: fn})
	s.seq++
}

// Step advances the virtual clock by exactly one tick: first all events due
// at this tick are processed in (time, seq) order, then every node ticks in
// sorted ID order.
func (s *Simulator) Step() {
	for len(s.queue) > 0 && s.queue[0].Time <= s.now {
		ev := heap.Pop(&s.queue).(Event)
		switch ev.Kind {
		case EvMessage:
			if n, ok := s.nodes[ev.To]; ok {
				n.OnMessage(ev.From, ev.Msg, s.now)
			}
		case EvAction:
			ev.Fn()
		}
	}
	for _, id := range s.order {
		s.nodes[id].OnTick(s.now)
	}
	s.now++
}

// Run advances the simulator by the given number of discrete clock steps.
func (s *Simulator) Run(steps uint64) {
	for i := uint64(0); i < steps; i++ {
		s.Step()
	}
}

// Logf appends a timestamped line to the deterministic event log.
func (s *Simulator) Logf(format string, args ...any) {
	if !s.LogEnabled {
		return
	}
	s.Log = append(s.Log, fmt.Sprintf("tick=%d %s", s.now, fmt.Sprintf(format, args...)))
}

// Digest returns a stable FNV-1a hash of the whole event log. Two runs with
// the same seed must produce the same digest.
func (s *Simulator) Digest() string {
	h := fnv.New64a()
	for _, l := range s.Log {
		h.Write([]byte(l))
		h.Write([]byte("\n"))
	}
	return fmt.Sprintf("%016x", h.Sum64())
}
