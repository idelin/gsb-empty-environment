package main

import (
	"fmt"
	"sort"
)

// Cert is recorded evidence for every commit: which voters, under which
// configuration, formed the quorum. Tests and the safety checker verify
// that every committed entry carries a valid (joint) majority certificate.
type Cert struct {
	Index  int
	View   int
	Cfg    Config
	Voters []int
}

type event struct {
	time int
	seq  int
	msg  Message
}

// Sim is a single-process event simulator: it owns virtual time, the
// message queue, all fault injection (drops, delays, crashes) and the
// safety invariant checker.
type Sim struct {
	now   int
	seq   int
	queue []event
	nodes map[int]*Node

	// Fault hooks. Drop returns true to discard a message; Delay returns
	// virtual-time units of delivery latency (default 1).
	Drop  func(Message) bool
	Delay func(Message) int

	Trace []string
	Certs []Cert
}

func NewSim() *Sim {
	return &Sim{nodes: map[int]*Node{}}
}

func (s *Sim) AddNode(id int, base Config) {
	s.nodes[id] = newNode(id, base)
}

func (s *Sim) Node(id int) *Node { return s.nodes[id] }

func (s *Sim) tracef(format string, args ...interface{}) {
	s.Trace = append(s.Trace, fmt.Sprintf("t=%04d %s", s.now, fmt.Sprintf(format, args...)))
}

func (s *Sim) recordCert(index, view int, cfg Config, voters map[int]bool) {
	s.Certs = append(s.Certs, Cert{Index: index, View: view, Cfg: cfg, Voters: sortedSet(voters)})
}

func (s *Sim) send(m Message) {
	if s.Drop != nil && s.Drop(m) {
		s.tracef("DROP %s", m)
		return
	}
	d := 1
	if s.Delay != nil {
		d = s.Delay(m)
	}
	s.seq++
	s.queue = append(s.queue, event{time: s.now + d, seq: s.seq, msg: m})
}

// Step delivers the earliest queued event. Returns false when idle.
func (s *Sim) Step() bool {
	if len(s.queue) == 0 {
		return false
	}
	best := 0
	for i := range s.queue {
		if s.queue[i].time < s.queue[best].time ||
			(s.queue[i].time == s.queue[best].time && s.queue[i].seq < s.queue[best].seq) {
			best = i
		}
	}
	ev := s.queue[best]
	s.queue = append(s.queue[:best], s.queue[best+1:]...)
	s.now = ev.time
	s.deliver(ev.msg)
	return true
}

// RunUntil drains events up to a virtual-time deadline.
func (s *Sim) RunUntil(deadline int) {
	for len(s.queue) > 0 {
		next := s.queue[0].time
		for _, e := range s.queue {
			if e.time < next {
				next = e.time
			}
		}
		if next > deadline {
			break
		}
		s.Step()
	}
}

func (s *Sim) deliver(m Message) {
	n := s.nodes[m.To]
	if n == nil || n.crashed {
		return
	}
	switch m.Type {
	case MsgVoteRequest:
		n.onVoteRequest(s, m)
	case MsgVoteResponse:
		n.onVoteResponse(s, m)
	case MsgAppend:
		n.onAppend(s, m)
	case MsgAppendAck:
		n.onAck(s, m)
	}
}

// --- fault and control API ---

// Elect triggers an election timeout on a node.
func (s *Sim) Elect(id int) {
	if n := s.nodes[id]; n != nil && !n.crashed {
		n.startElection(s)
	}
}

// Crash fails a node and purges its in-flight outbound messages.
func (s *Sim) Crash(id int) {
	if n := s.nodes[id]; n != nil {
		n.crashed = true
		s.tracef("node %d: CRASH", id)
	}
	kept := s.queue[:0]
	for _, e := range s.queue {
		if e.msg.From != id {
			kept = append(kept, e)
		}
	}
	s.queue = kept
}

// Restart revives a node; persistent state (view, log, commit) survives,
// volatile leader/candidate state is lost.
func (s *Sim) Restart(id int) {
	if n := s.nodes[id]; n != nil {
		n.crashed = false
		n.role = Follower
		n.votes, n.match, n.next = nil, nil, nil
		s.tracef("node %d: restart (view %d, log %d entries, commit %d)", id, n.view, n.lastIndex(), n.commit)
	}
}

// Heartbeat makes the leader re-send Append to all voters (retry/keepalive).
func (s *Sim) Heartbeat(id int) {
	if n := s.nodes[id]; n != nil && !n.crashed && n.role == Leader {
		n.replicate(s)
	}
}

// Propose appends a client command on the leader.
func (s *Sim) Propose(id int, cmd string) error {
	n := s.nodes[id]
	if n == nil || n.crashed {
		return fmt.Errorf("node %d unavailable", id)
	}
	if n.role != Leader {
		return fmt.Errorf("node %d is not leader", id)
	}
	n.appendEntry(s, Entry{Cmd: cmd})
	return nil
}

// ProposeReconfig starts a membership change to newMembers via the
// mandatory joint phase.
func (s *Sim) ProposeReconfig(id int, newMembers []int) error {
	n := s.nodes[id]
	if n == nil || n.crashed {
		return fmt.Errorf("node %d unavailable", id)
	}
	if n.role != Leader {
		return fmt.Errorf("node %d is not leader", id)
	}
	if n.cfg.Joint {
		return fmt.Errorf("reconfiguration already in progress")
	}
	j := JointCfg(n.cfg.Old, newMembers)
	s.tracef("node %d: propose reconfiguration to %s", id, j)
	n.appendEntry(s, Entry{Cfg: &j})
	return nil
}

// ProposeDirectSwitch attempts to jump straight from the current stable
// majority to a new (possibly disjoint) stable majority. The protocol
// forbids this; it always fails unless newMembers equals the current set.
func (s *Sim) ProposeDirectSwitch(id int, newMembers []int) error {
	n := s.nodes[id]
	if n == nil || n.crashed {
		return fmt.Errorf("node %d unavailable", id)
	}
	target := Stable(newMembers...)
	if !n.cfg.canTransitionTo(target) {
		return fmt.Errorf("REJECTED: direct switch %s -> %s bypasses the joint phase", n.cfg, target)
	}
	n.appendEntry(s, Entry{Cfg: &target})
	return nil
}

// --- safety evidence ---

// CheckSafety verifies the state-machine safety invariant over all
// committed state, plus the validity of every commit certificate.
func (s *Sim) CheckSafety() error {
	ids := make([]int, 0, len(s.nodes))
	for id := range s.nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			na, nb := s.nodes[a], s.nodes[b]
			k := na.commit
			if nb.commit < k {
				k = nb.commit
			}
			for idx := 1; idx <= k; idx++ {
				ea, eb := na.log[idx-1], nb.log[idx-1]
				if ea.View != eb.View || ea.Cmd != eb.Cmd || !cfgPtrEqual(ea.Cfg, eb.Cfg) {
					return fmt.Errorf("SAFETY VIOLATION: nodes %d and %d diverge at committed index %d (%v vs %v)",
						a, b, idx, ea, eb)
				}
			}
		}
	}
	for _, c := range s.Certs {
		votes := map[int]bool{}
		for _, id := range c.Voters {
			votes[id] = true
		}
		if !c.Cfg.Quorum(votes) {
			return fmt.Errorf("SAFETY VIOLATION: commit at index %d (view %d) lacks a valid quorum under %s (voters %v)",
				c.Index, c.View, c.Cfg, c.Voters)
		}
	}
	return nil
}

func cfgPtrEqual(a, b *Config) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Joint == b.Joint && equalSets(a.Old, b.Old) && equalSets(a.New, b.New)
}
