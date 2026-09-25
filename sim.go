package causal

import (
	"container/heap"
	"math/rand"
)

// Config controls a simulation run. Time is logical (integer ticks);
// all communication, faults and time advancement happen inside Sim.
type Config struct {
	Members   []NodeID
	MaxBuffer int // per-node hold-back buffer bound; 0 = unbounded

	MinDelay uint64 // network delay range in ticks
	MaxDelay uint64

	DropRate float64 // probability a copy is lost
	DupRate  float64 // probability one extra duplicate copy is sent

	// DropPred, when non-nil, decides deterministically whether the copy
	// to a given node is dropped (overrides DropRate).
	DropPred func(to NodeID, m Message) bool

	RetryAfter uint64 // delay before retrying after ErrBackpressure
	MaxTime    uint64 // give-up horizon in ticks; 0 = unlimited
	Seed       int64
}

// Stats accumulates evidence counters for a run.
type Stats struct {
	Broadcasts   int
	Transmitting int // copies put on the wire
	Deliveries   int // delivery events processed
	Drops        int
	Backpressure int // ErrBackpressure signals raised
	Retries      int // retransmissions scheduled after backpressure
	Expired      int // events dropped because MaxTime was exceeded
}

type event struct {
	time uint64
	seq  uint64
	to   NodeID
	msg  Message
}

type eventQueue []event

func (q eventQueue) Len() int { return len(q) }
func (q eventQueue) Less(i, j int) bool {
	if q[i].time != q[j].time {
		return q[i].time < q[j].time
	}
	return q[i].seq < q[j].seq
}
func (q eventQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *eventQueue) Push(x interface{}) { *q = append(*q, x.(event)) }
func (q *eventQueue) Pop() interface{} {
	old := *q
	n := len(old)
	e := old[n-1]
	*q = old[:n-1]
	return e
}

// Sim is a single-process discrete-event simulator: it owns the
// network, the fault injection and the clock.
type Sim struct {
	cfg   Config
	nodes map[NodeID]*Node
	rng   *rand.Rand
	queue eventQueue
	seq   uint64
	now   uint64

	Stats Stats
}

// NewSim builds the fixed membership set.
func NewSim(cfg Config) *Sim {
	s := &Sim{
		cfg:   cfg,
		nodes: make(map[NodeID]*Node),
		rng:   rand.New(rand.NewSource(cfg.Seed)),
	}
	for _, id := range cfg.Members {
		s.nodes[id] = NewNode(id, cfg.Members, cfg.MaxBuffer)
	}
	return s
}

// Node returns the node with the given id.
func (s *Sim) Node(id NodeID) *Node { return s.nodes[id] }

// Now returns the current logical time in ticks.
func (s *Sim) Now() uint64 { return s.now }

// Broadcast makes node `from` broadcast a payload and schedules network
// delivery of a copy to every other member.
func (s *Sim) Broadcast(from NodeID, payload string) {
	msg := s.nodes[from].Broadcast(payload)
	s.Stats.Broadcasts++
	for _, m := range s.cfg.Members {
		if m == from {
			continue
		}
		s.schedule(m, msg)
	}
}

func (s *Sim) schedule(to NodeID, msg Message) {
	drop := false
	if s.cfg.DropPred != nil {
		drop = s.cfg.DropPred(to, msg)
	} else if s.rng.Float64() < s.cfg.DropRate {
		drop = true
	}
	if drop {
		s.Stats.Drops++
		return
	}
	delay := s.cfg.MinDelay
	if s.cfg.MaxDelay > s.cfg.MinDelay {
		delay += uint64(s.rng.Int63n(int64(s.cfg.MaxDelay-s.cfg.MinDelay+1)))
	}
	s.push(event{time: s.now + delay, to: to, msg: msg})
	s.Stats.Transmitting++
	if s.rng.Float64() < s.cfg.DupRate {
		s.push(event{time: s.now + delay + 1, to: to, msg: msg})
		s.Stats.Transmitting++
	}
}

func (s *Sim) push(ev event) {
	s.seq++
	ev.seq = s.seq
	heap.Push(&s.queue, ev)
}

// Run processes events until the queue is empty or MaxTime is exceeded.
func (s *Sim) Run() {
	for len(s.queue) > 0 {
		ev := heap.Pop(&s.queue).(event)
		if s.cfg.MaxTime > 0 && ev.time > s.cfg.MaxTime {
			s.Stats.Expired += len(s.queue) + 1
			return
		}
		s.now = ev.time
		s.Stats.Deliveries++
		err := s.nodes[ev.to].Receive(ev.msg)
		if err == ErrBackpressure {
			// Explicit backpressure: the copy is rejected, not lost
			// silently; the simulator retransmits after RetryAfter.
			s.Stats.Backpressure++
			s.Stats.Retries++
			s.push(event{time: s.now + s.cfg.RetryAfter, to: ev.to, msg: ev.msg})
		}
	}
}

// Duplicates sums the dedup counters of all nodes.
func (s *Sim) Duplicates() int {
	total := 0
	for _, n := range s.nodes {
		total += n.Duplicates
	}
	return total
}
