package raftsim

import (
	"container/heap"
	"fmt"
	"math/rand"
)

// event is a scheduled message delivery.
type event struct {
	at  int
	seq int
	msg Message
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

// Sim is a single-process event simulator. It owns the logical clock
// (Now, advanced one tick per Run iteration), the network (a priority
// queue of in-flight messages with configurable delay and loss), and the
// failure model (crash/restart of nodes, partitions between node sets).
// Every run is deterministic for a given seed.
type Sim struct {
	Now   int
	Nodes []*Node

	up    []bool
	rng   *rand.Rand
	seq   int
	queue eventHeap

	// LossRate is the per-message drop probability.
	LossRate float64
	// MinDelay/MaxDelay bound message delivery latency in ticks.
	MinDelay, MaxDelay int

	cut map[[2]int]bool // directed links currently severed

	// Trace enables node-level event logging (elections, commits, hints).
	Trace bool
	// TraceDeliver additionally logs every message delivery.
	TraceDeliver bool

	// Check, when set, runs after every delivery and every tick; tests use
	// it to assert safety invariants continuously rather than only at the
	// end of a scenario.
	Check func()
}

// NewSim builds a cluster of n nodes, all alive and fully connected.
func NewSim(n int, seed int64) *Sim {
	s := &Sim{
		rng:      rand.New(rand.NewSource(seed)),
		MinDelay: 1,
		MaxDelay: 2,
		cut:      map[[2]int]bool{},
	}
	s.up = make([]bool, n)
	for i := 0; i < n; i++ {
		s.up[i] = true
		nd := NewNode(i, n, rand.New(rand.NewSource(seed*1000+int64(i)+1)))
		nd.logger = func(format string, args ...any) {
			if s.Trace {
				fmt.Printf("t=%04d  %s\n", s.Now, fmt.Sprintf(format, args...))
			}
		}
		s.Nodes = append(s.Nodes, nd)
	}
	return s
}

func (s *Sim) trace(format string, args ...any) {
	if s.TraceDeliver {
		fmt.Printf("t=%04d  net: %s\n", s.Now, fmt.Sprintf(format, args...))
	}
}

func (s *Sim) send(msgs []Message) {
	for _, m := range msgs {
		if m.From == m.To {
			continue
		}
		if s.rng.Float64() < s.LossRate {
			s.trace("drop %d -> %d", m.From, m.To)
			continue
		}
		delay := s.MinDelay
		if s.MaxDelay > s.MinDelay {
			delay += s.rng.Intn(s.MaxDelay - s.MinDelay + 1)
		}
		s.seq++
		heap.Push(&s.queue, event{at: s.Now + delay, seq: s.seq, msg: m})
	}
}

func (s *Sim) linked(from, to int) bool { return !s.cut[[2]int{from, to}] }

// Run advances the simulated world by the given number of ticks.
func (s *Sim) Run(ticks int) {
	for t := 0; t < ticks; t++ {
		s.Now++
		for len(s.queue) > 0 && s.queue[0].at <= s.Now {
			ev := heap.Pop(&s.queue).(event)
			if !s.up[ev.msg.To] || !s.linked(ev.msg.From, ev.msg.To) {
				continue // crashed receiver or severed link: message lost
			}
			s.trace("deliver %s", describe(ev.msg))
			s.send(s.Nodes[ev.msg.To].Step(ev.msg))
			if s.Check != nil {
				s.Check()
			}
		}
		for _, nd := range s.Nodes {
			if s.up[nd.ID] {
				s.send(nd.Tick())
			}
		}
		if s.Check != nil {
			s.Check()
		}
	}
}

// Crash stops a node (fail-stop); persistent state survives.
func (s *Sim) Crash(id int) {
	s.up[id] = false
	s.Nodes[id].Crash()
}

// Restart brings a crashed node back with its persistent state.
func (s *Sim) Restart(id int) { s.up[id] = true }

// Partition severs all directed links between groups a and b.
func (s *Sim) Partition(a, b []int) {
	for _, x := range a {
		for _, y := range b {
			s.cut[[2]int{x, y}] = true
			s.cut[[2]int{y, x}] = true
		}
	}
}

// Heal restores all links.
func (s *Sim) Heal() { s.cut = map[[2]int]bool{} }

// Propose offers a command to the first live leader; returns false when no
// leader is currently available.
func (s *Sim) Propose(cmd string) bool {
	for _, nd := range s.Nodes {
		if s.up[nd.ID] && nd.Role() == Leader {
			if msgs := nd.Propose(cmd); msgs != nil {
				s.send(msgs)
				return true
			}
		}
	}
	return false
}

// ProposeOn offers a command to a specific node; returns false unless that
// node is a live leader.
func (s *Sim) ProposeOn(id int, cmd string) bool {
	if !s.up[id] || s.Nodes[id].Role() != Leader {
		return false
	}
	s.send(s.Nodes[id].Propose(cmd))
	return true
}

// LeaderIDs returns the ids of all live nodes that currently believe they
// are leader (more than one is possible across a partition).
func (s *Sim) LeaderIDs() []int {
	var ids []int
	for _, nd := range s.Nodes {
		if s.up[nd.ID] && nd.Role() == Leader {
			ids = append(ids, nd.ID)
		}
	}
	return ids
}

// Alive reports whether a node is currently up.
func (s *Sim) Alive(id int) bool { return s.up[id] }

func describe(m Message) string {
	switch m.Type {
	case MsgRequestVote:
		return fmt.Sprintf("RequestVote %d->%d term=%d last=(%d,t%d)", m.From, m.To, m.Term, m.LastIndex, m.LastTerm)
	case MsgRequestVoteResp:
		return fmt.Sprintf("RequestVoteResp %d->%d term=%d granted=%v", m.From, m.To, m.Term, m.Granted)
	case MsgAppend:
		return fmt.Sprintf("AppendEntries %d->%d term=%d prev=(%d,t%d) entries=%d commit=%d",
			m.From, m.To, m.Term, m.PrevIndex, m.PrevTerm, len(m.Entries), m.LeaderCommit)
	case MsgAppendResp:
		return fmt.Sprintf("AppendEntriesResp %d->%d term=%d ok=%v match=%d hint=(t%d,%d)",
			m.From, m.To, m.Term, m.Success, m.MatchIndex, m.ConflictTerm, m.ConflictIndex)
	}
	return "?"
}
