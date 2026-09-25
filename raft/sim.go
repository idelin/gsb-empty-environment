package raft

import (
	"container/heap"
	"fmt"
	"math/rand"
)

// Disk is a node's stable storage: it survives crashes.
type Disk struct {
	Term        int
	VotedFor    int
	Log         []Entry
	CommitIndex int
}

type event struct {
	time int
	seq  int
	fn   func()
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

// Sim is a single-process event simulator. All communication (message
// delay, loss, duplication, partitions), all failures (crash/restart) and
// all time (a logical clock advanced only by the event loop) are controlled
// here, which makes every run deterministic for a given seed.
type Sim struct {
	now   int
	seq   int
	queue eventHeap
	rng   *rand.Rand

	nodes []*Node
	disks []Disk

	blocked map[[2]int]bool // directed network partitions

	// Fault-injection knobs.
	Verbose  bool
	LossProb float64 // probability of dropping a message
	DupProb  float64 // probability of delivering a message twice

	// Evidence collected while running.
	Logs       []string  // human-readable trace (when Verbose)
	Applied    [][]Entry // per-node committed prefix delivered to the "state machine"
	Violations []string  // safety violations observed (must stay empty)
}

func NewSim(size int, seed int64) *Sim {
	s := &Sim{
		rng:     rand.New(rand.NewSource(seed)),
		blocked: make(map[[2]int]bool),
		Applied: make([][]Entry, size),
	}
	for i := 0; i < size; i++ {
		s.disks = append(s.disks, Disk{VotedFor: -1, Log: []Entry{{Term: 0}}})
		n := &Node{id: i, sim: s, role: Follower}
		s.nodes = append(s.nodes, n)
		s.loadFromDisk(n)
		n.resetElectionTimer()
	}
	return s
}

func (s *Sim) loadFromDisk(n *Node) {
	d := s.disks[n.id]
	n.currentTerm = d.Term
	n.votedFor = d.VotedFor
	n.log = append([]Entry(nil), d.Log...)
	n.commitIndex = d.CommitIndex
	n.lastApplied = 0
	n.role = Follower
}

// ---- time ----

func (s *Sim) schedule(at int, fn func()) {
	s.seq++
	heap.Push(&s.queue, event{time: at, seq: s.seq, fn: fn})
}

// Schedule lets tests and demos inject their own events at a logical time.
func (s *Sim) Schedule(at int, fn func()) { s.schedule(at, fn) }

// Now returns the current logical time in milliseconds.
func (s *Sim) Now() int { return s.now }

// Run processes events in time order until the queue is empty or the next
// event lies beyond `until`.
func (s *Sim) Run(until int) {
	for s.queue.Len() > 0 {
		ev := heap.Pop(&s.queue).(event)
		if ev.time > until {
			heap.Push(&s.queue, ev)
			break
		}
		s.now = ev.time
		ev.fn()
	}
	if s.now < until {
		s.now = until
	}
}

// RunFor advances the simulation by d logical milliseconds.
func (s *Sim) RunFor(d int) { s.Run(s.now + d) }

// ---- communication ----

// send delivers a message after a random 1-10ms delay. Delivery is checked
// against partitions, receiver liveness, loss and duplication at delivery
// time, so faults injected while a message is in flight take effect.
func (s *Sim) send(from, to int, deliver func()) {
	delay := 1 + s.rng.Intn(10)
	s.schedule(s.now+delay, func() {
		if s.blocked[[2]int{from, to}] || s.nodes[to].dead {
			return
		}
		if s.LossProb > 0 && s.rng.Float64() < s.LossProb {
			return
		}
		deliver()
		if s.DupProb > 0 && s.rng.Float64() < s.DupProb {
			dup := deliver
			s.schedule(s.now+1+s.rng.Intn(5), func() {
				if s.blocked[[2]int{from, to}] || s.nodes[to].dead {
					return
				}
				dup()
			})
		}
	})
}

// ---- failures ----

// Crash halts a node; its volatile state is lost, its disk survives.
func (s *Sim) Crash(id int) {
	n := s.nodes[id]
	if n.dead {
		return
	}
	n.dead = true
	n.timerGen++
	n.heartbeatGen++
	n.role = Follower
	n.nextIndex = nil
	n.matchIndex = nil
	s.logf("sim: node %d CRASHED", id)
}

// Restart brings a crashed node back, restoring persistent state from disk.
func (s *Sim) Restart(id int) {
	n := s.nodes[id]
	n.dead = false
	n.timerGen++
	n.heartbeatGen++
	s.loadFromDisk(n)
	n.apply() // re-deliver the persisted committed prefix to the state machine
	n.resetElectionTimer()
	s.logf("sim: node %d restarted (term=%d lastIndex=%d commitIndex=%d)",
		id, n.currentTerm, n.lastIndex(), n.commitIndex)
}

// Partition drops all traffic between a and b in both directions.
func (s *Sim) Partition(a, b int) {
	if a == b {
		return
	}
	s.blocked[[2]int{a, b}] = true
	s.blocked[[2]int{b, a}] = true
}

// Isolate cuts a node off from all its peers.
func (s *Sim) Isolate(id int) {
	for i := range s.nodes {
		s.Partition(id, i)
	}
	s.logf("sim: node %d isolated from the cluster", id)
}

// Heal removes all partitions.
func (s *Sim) Heal() {
	s.blocked = make(map[[2]int]bool)
	s.logf("sim: all partitions healed")
}

// ---- helpers for tests and the demo ----

// Leader returns the id of a live leader, or -1 if there is none.
func (s *Sim) Leader() int {
	for _, n := range s.nodes {
		if !n.dead && n.role == Leader {
			return n.id
		}
	}
	return -1
}

// Propose submits a client command to the current leader, if any.
func (s *Sim) Propose(command string) bool {
	if id := s.Leader(); id >= 0 {
		return s.nodes[id].Propose(command)
	}
	return false
}

// NodeLog returns a copy of a node's current log (including the sentinel).
func (s *Sim) NodeLog(id int) []Entry {
	return append([]Entry(nil), s.nodes[id].log...)
}

// AppliedCommands returns the committed commands a node has applied, in order.
func (s *Sim) AppliedCommands(id int) []string {
	out := []string{}
	for _, e := range s.Applied[id] {
		out = append(out, e.Command)
	}
	return out
}

func (s *Sim) logf(format string, args ...interface{}) {
	if s.Verbose {
		s.Logs = append(s.Logs, fmt.Sprintf("[t=%4d] %s", s.now, fmt.Sprintf(format, args...)))
	}
}

// onApply delivers a committed entry to a node's "state machine" and checks
// the state-machine-safety invariant: committed entries never change and
// never diverge between nodes.
func (s *Sim) onApply(id, index int, e Entry) {
	cur := s.Applied[id]
	if index <= len(cur) {
		if cur[index-1] != e {
			s.Violations = append(s.Violations,
				fmt.Sprintf("node %d changed its committed entry at index %d", id, index))
		}
		return
	}
	s.Applied[id] = append(cur, e)
	for other := range s.nodes {
		if other == id {
			continue
		}
		if o := s.Applied[other]; len(o) >= index && o[index-1] != e {
			s.Violations = append(s.Violations,
				fmt.Sprintf("committed entries diverge at index %d: node %d has %+v, node %d has %+v",
					index, id, e, other, o[index-1]))
		}
	}
	s.logf("node %d: applied index %d = %q", id, index, e.Command)
}
