package syncsim

import (
	"container/heap"
	"math/rand"
)

// Sim is a single-process event simulator: it owns virtual time, the
// network (delay, drop, duplication), and node crashes. Given the same
// seed, a run is fully deterministic.
type Sim struct {
	Now uint64

	DH DigestHash // digest hash used by all nodes (injectable for tests)

	// Network fault model.
	MinDelay, MaxDelay uint64  // delivery delay bounds in virtual ticks
	DropRate           float64 // probability a message is lost
	DupRate            float64 // probability a message is duplicated

	// Counters (evidence).
	Sent       int
	Dropped    int
	Duplicated int

	rng     *rand.Rand
	seq     int
	queue   eventHeap
	nodes   map[string]*Node
	order   []*Node
	crashed map[string]bool
}

// NewSim creates a simulator with the given seed and digest hash
// (nil means FnvDigest).
func NewSim(seed int64, dh DigestHash) *Sim {
	if dh == nil {
		dh = FnvDigest
	}
	return &Sim{
		DH:       dh,
		MinDelay: 1,
		MaxDelay: 5,
		rng:      rand.New(rand.NewSource(seed)),
		nodes:    make(map[string]*Node),
		crashed:  make(map[string]bool),
	}
}

// AddNode creates a node and returns it.
func (s *Sim) AddNode(id string) *Node {
	n := newNode(s, id)
	s.nodes[id] = n
	s.order = append(s.order, n)
	return n
}

// Nodes returns nodes in creation order (deterministic iteration).
func (s *Sim) Nodes() []*Node { return s.order }

// Crash makes a node stop sending and drop everything it receives.
// Its Store survives (durable state); Restart resumes participation.
func (s *Sim) Crash(id string)   { s.crashed[id] = true }
func (s *Sim) Restart(id string) { delete(s.crashed, id) }

// Send injects a message into the network, applying the fault model.
func (s *Sim) Send(m Msg) {
	if s.crashed[m.From] {
		return
	}
	s.Sent++
	if s.rng.Float64() < s.DropRate {
		s.Dropped++
		return
	}
	s.schedule(s.Now+s.delay(), func() { s.deliver(m) })
	if s.rng.Float64() < s.DupRate {
		s.Duplicated++
		s.schedule(s.Now+s.delay()+1, func() { s.deliver(m) })
	}
}

func (s *Sim) delay() uint64 {
	if s.MaxDelay <= s.MinDelay {
		return s.MinDelay
	}
	return s.MinDelay + uint64(s.rng.Int63n(int64(s.MaxDelay-s.MinDelay+1)))
}

func (s *Sim) deliver(m Msg) {
	if s.crashed[m.To] {
		s.Dropped++
		return
	}
	if n, ok := s.nodes[m.To]; ok {
		n.handle(m)
	}
}

// After schedules fn to run d virtual ticks from now.
func (s *Sim) After(d uint64, fn func()) { s.schedule(s.Now+d, fn) }

func (s *Sim) schedule(at uint64, fn func()) {
	s.seq++
	heap.Push(&s.queue, event{at: at, seq: s.seq, fn: fn})
}

// Pending reports whether events remain.
func (s *Sim) Pending() bool { return s.queue.Len() > 0 }

// Run processes events in (time, seq) order until the queue is empty.
func (s *Sim) Run() {
	for s.queue.Len() > 0 {
		ev := heap.Pop(&s.queue).(event)
		s.Now = ev.at
		ev.fn()
	}
}

type event struct {
	at  uint64
	seq int
	fn  func()
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
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
