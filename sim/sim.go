// Package sim is a single-process, deterministic event simulator.
//
// Communication (message delay, loss, duplication, reordering), failures
// (crash / recover, network partition / heal) and time (a virtual clock
// advanced only by the event queue) are all controlled here. Nothing in
// the OR-Set implementation reads the wall clock or a real network.
package sim

import (
	"container/heap"
	"fmt"
	"math/rand"

	"orsetdemo/orset"
)

const (
	evDeliver = iota
	evCall
)

type event struct {
	at   uint64
	tie  uint64
	kind int
	to   string
	from string
	st   orset.State
	fn   func(*Sim)
}

// eventQueue is a min-heap ordered by (at, tie) for deterministic replay.
type eventQueue []event

func (q eventQueue) Len() int { return len(q) }
func (q eventQueue) Less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	return q[i].tie < q[j].tie
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

// Node is one simulated replica.
type Node struct {
	ID      string
	Set     *orset.Set
	Crashed bool
}

// Sim is the simulator. The zero-value knobs mean: no loss, no
// duplication, delivery delay in [1, MaxDelay] virtual ticks.
type Sim struct {
	Now      uint64
	Nodes    map[string]*Node
	roster   []string
	queue    eventQueue
	tie      uint64
	rng      *rand.Rand
	Loss     float64 // probability a message is lost
	Dup      float64 // probability a message is delivered twice
	MaxDelay uint64  // max random delivery delay (>= 1)
	links    map[string]map[string]bool
	Log      []string
	Verbose  bool
}

// New creates a simulator with one node per id; the id list is the fixed
// roster used by every replica for GC causal confirmation. All links start
// connected.
func New(seed int64, ids ...string) *Sim {
	s := &Sim{
		Nodes:    make(map[string]*Node),
		rng:      rand.New(rand.NewSource(seed)),
		MaxDelay: 3,
		links:    make(map[string]map[string]bool),
	}
	s.roster = append([]string(nil), ids...)
	for _, id := range ids {
		s.Nodes[id] = &Node{ID: id, Set: orset.New(id, ids...)}
		s.links[id] = make(map[string]bool)
		for _, o := range ids {
			if o != id {
				s.links[id][o] = true
			}
		}
	}
	return s
}

func (s *Sim) logf(format string, args ...interface{}) {
	if s.Verbose {
		s.Log = append(s.Log, fmt.Sprintf("[t=%03d] %s", s.Now, fmt.Sprintf(format, args...)))
	}
}

// Partition cuts communication between a and b (both directions).
func (s *Sim) Partition(a, b string) {
	s.links[a][b] = false
	s.links[b][a] = false
	s.logf("partition %s |/| %s", a, b)
}

// Heal restores communication between a and b.
func (s *Sim) Heal(a, b string) {
	s.links[a][b] = true
	s.links[b][a] = true
	s.logf("heal %s |/| %s", a, b)
}

// Crash stops a node: it sends nothing and drops everything addressed to it.
func (s *Sim) Crash(id string) {
	s.Nodes[id].Crashed = true
	s.logf("crash %s", id)
}

// Recover brings a crashed node back (state retained, missed messages lost).
func (s *Sim) Recover(id string) {
	s.Nodes[id].Crashed = false
	s.logf("recover %s", id)
}

// At schedules fn to run at virtual time t.
func (s *Sim) At(t uint64, fn func(*Sim)) {
	s.push(event{at: t, kind: evCall, fn: fn})
}

// After schedules fn to run d ticks from Now.
func (s *Sim) After(d uint64, fn func(*Sim)) { s.At(s.Now+d, fn) }

// Send enqueues a snapshot of from's state for delivery to to, applying
// the configured loss, duplication and random delay. Messages between
// partitioned or crashed nodes are dropped at send time.
func (s *Sim) Send(from, to string) {
	if from == to {
		return
	}
	if s.Nodes[from].Crashed || s.Nodes[to].Crashed || !s.links[from][to] {
		s.logf("drop %s -> %s (no route)", from, to)
		return
	}
	if s.rng.Float64() < s.Loss {
		s.logf("loss %s -> %s", from, to)
		return
	}
	st := s.Nodes[from].Set.Snapshot() // captured at send time
	s.deliver(from, to, st)
	if s.rng.Float64() < s.Dup {
		s.deliver(from, to, st)
	}
}

func (s *Sim) deliver(from, to string, st orset.State) {
	delay := uint64(1)
	if s.MaxDelay > 1 {
		delay += uint64(s.rng.Int63n(int64(s.MaxDelay)))
	}
	s.push(event{at: s.Now + delay, kind: evDeliver, from: from, to: to, st: st})
}

// Gossip makes every live node send its state to every connected peer.
func (s *Sim) Gossip() {
	for _, id := range s.roster {
		for _, o := range s.roster {
			s.Send(id, o)
		}
	}
}

// Run processes all events with virtual time <= until.
func (s *Sim) Run(until uint64) {
	for len(s.queue) > 0 && s.queue[0].at <= until {
		e := heap.Pop(&s.queue).(event)
		s.Now = e.at
		switch e.kind {
		case evDeliver:
			n := s.Nodes[e.to]
			if n.Crashed || !s.links[e.from][e.to] {
				s.logf("drop %s -> %s (delivery)", e.from, e.to)
				continue
			}
			n.Set.Merge(e.st)
			s.logf("deliver %s -> %s", e.from, e.to)
		case evCall:
			e.fn(s)
		}
	}
	s.Now = until
}

// RunAll processes every queued event.
func (s *Sim) RunAll() {
	for len(s.queue) > 0 {
		s.Run(s.queue[0].at)
	}
}

// Converged reports whether all non-crashed nodes hold identical states.
func (s *Sim) Converged() bool {
	var ref *orset.State
	for _, id := range s.roster {
		n := s.Nodes[id]
		if n.Crashed {
			continue
		}
		snap := n.Set.Snapshot()
		if ref == nil {
			ref = &snap
			continue
		}
		if !orset.Equal(*ref, snap) {
			return false
		}
	}
	return true
}

// Elements returns the live elements of node id (for assertions/logging).
func (s *Sim) Elements(id string) []string { return s.Nodes[id].Set.Elements() }

func (s *Sim) push(e event) {
	e.tie = s.tie
	s.tie++
	heap.Push(&s.queue, e)
}
