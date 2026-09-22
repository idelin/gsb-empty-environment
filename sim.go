package main

import (
	"fmt"
	"sort"
	"strings"
)

// Message kinds of the (deliberately naive) atomic-commit protocol.
const (
	MsgPrepare = "Prepare"
	MsgYes     = "Yes"
	MsgCommit  = "Commit"
)

// Node names.
const (
	NodeCoord = "C"
	NodeP1    = "P1"
	NodeP2    = "P2"
)

var nodeOrder = []string{NodeCoord, NodeP1, NodeP2}

// Message is uniquely identified by Key, which is derived deterministically
// from (kind, from, to) plus a per-sender sequence number, so replays of the
// same action sequence produce the same keys.
type Message struct {
	Key  string
	Kind string
	From string
	To   string
}

func (m Message) String() string {
	return fmt.Sprintf("%s:%s->%s", m.Kind, m.From, m.To)
}

type node struct {
	state    string // coordinator: idle/waitYes/done; participant: idle/prepared/done
	yesFrom  map[string]bool
	decision string // "", "commit" or "abort"
}

// Sim is a single-process event simulator: message delivery, packet loss and
// timeouts are all driven explicitly by the action trace.
type Sim struct {
	nodes   map[string]*node
	pending []Message
	sendSeq map[string]int
	log     []string
}

func NewSim() *Sim {
	return &Sim{
		nodes: map[string]*node{
			NodeCoord: {state: "idle", yesFrom: map[string]bool{}},
			NodeP1:    {state: "idle"},
			NodeP2:    {state: "idle"},
		},
		sendSeq: map[string]int{},
	}
}

func (s *Sim) send(kind, from, to string) {
	base := kind + ":" + from + "->" + to
	s.sendSeq[base]++
	m := Message{Key: fmt.Sprintf("%s#%d", base, s.sendSeq[base]), Kind: kind, From: from, To: to}
	s.pending = append(s.pending, m)
	s.log = append(s.log, "send "+m.Key)
}

// Request injects a client request at the coordinator.
func (s *Sim) Request() {
	c := s.nodes[NodeCoord]
	if c.state != "idle" {
		return
	}
	c.state = "waitYes"
	s.log = append(s.log, "C: client request, broadcast Prepare")
	s.send(MsgPrepare, NodeCoord, NodeP1)
	s.send(MsgPrepare, NodeCoord, NodeP2)
}

func (s *Sim) deliver(m Message) {
	s.log = append(s.log, "deliver "+m.Key)
	switch m.Kind {
	case MsgPrepare:
		p := s.nodes[m.To]
		if p.state == "idle" {
			p.state = "prepared"
			s.send(MsgYes, m.To, NodeCoord)
		} else if p.state == "prepared" {
			s.send(MsgYes, m.To, NodeCoord) // retransmit reply
		}
	case MsgYes:
		c := s.nodes[NodeCoord]
		if c.state != "waitYes" {
			return
		}
		c.yesFrom[m.From] = true
		if c.yesFrom[NodeP1] && c.yesFrom[NodeP2] {
			c.decision = "commit"
			c.state = "done"
			s.log = append(s.log, "C: all votes yes, decide commit")
			s.send(MsgCommit, NodeCoord, NodeP1)
			s.send(MsgCommit, NodeCoord, NodeP2)
		}
	case MsgCommit:
		p := s.nodes[m.To]
		if p.state == "prepared" {
			p.decision = "commit"
			p.state = "done"
			s.log = append(s.log, m.To+": decide commit")
		}
	}
}

// timeout fires the timeout handler of a node. The protocol bug lives here:
// a prepared participant aborts unilaterally instead of blocking.
func (s *Sim) timeout(name string) {
	n := s.nodes[name]
	s.log = append(s.log, "timeout "+name)
	switch {
	case name == NodeCoord && n.state == "waitYes":
		for _, p := range []string{NodeP1, NodeP2} {
			if !n.yesFrom[p] {
				s.send(MsgPrepare, NodeCoord, p) // retransmit
			}
		}
	case name != NodeCoord && n.state == "prepared":
		n.decision = "abort"
		n.state = "done"
		s.log = append(s.log, name+": decide abort (unilateral)")
	}
}

func (s *Sim) take(key string) (Message, bool) {
	for i, m := range s.pending {
		if m.Key == key {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return m, true
		}
	}
	return Message{}, false
}

// ActionKind enumerates the externally controllable event types.
type ActionKind int

const (
	ActRequest ActionKind = iota
	ActDeliver
	ActDrop
	ActTimeout
)

// Action is one scheduled event in a trace.
type Action struct {
	Kind ActionKind
	Key  string // message key for ActDeliver / ActDrop
	Node string // node name for ActTimeout
}

func (a Action) String() string {
	switch a.Kind {
	case ActRequest:
		return "Request(C)"
	case ActDeliver:
		return "Deliver(" + a.Key + ")"
	case ActDrop:
		return "Drop(" + a.Key + ")"
	case ActTimeout:
		return "Timeout(" + a.Node + ")"
	}
	return "?"
}

// Result summarizes a completed replay.
type Result struct {
	Decisions map[string]string // node -> commit/abort (undecided nodes omitted)
	Log       []string
}

// Replay executes a trace deterministically. A trace is a complete schedule:
// it returns an error when an action is not executable (e.g. delivering a
// message that was never sent -- a broken event dependency) or when the
// schedule is not quiescent (a sent message is neither delivered nor
// explicitly dropped by the end of the trace).
func Replay(actions []Action) (Result, error) {
	s := NewSim()
	for i, a := range actions {
		switch a.Kind {
		case ActRequest:
			s.Request()
		case ActDeliver:
			m, ok := s.take(a.Key)
			if !ok {
				return Result{}, fmt.Errorf("step %d: %s: message not pending (dependency broken)", i, a)
			}
			s.deliver(m)
		case ActDrop:
			if _, ok := s.take(a.Key); !ok {
				return Result{}, fmt.Errorf("step %d: %s: message not pending (dependency broken)", i, a)
			}
			s.log = append(s.log, "drop "+a.Key)
		case ActTimeout:
			s.timeout(a.Node)
		}
	}
	if len(s.pending) > 0 {
		return Result{}, fmt.Errorf("schedule incomplete: %s neither delivered nor dropped (quiescence required)", s.pending[0].Key)
	}
	dec := map[string]string{}
	for name, n := range s.nodes {
		if n.decision != "" {
			dec[name] = n.decision
		}
	}
	return Result{Decisions: dec, Log: s.log}, nil
}

// Violation returns a canonical signature of a safety violation (divergent
// decisions), or "" if there is none. Undecided nodes (e.g. stuck after a
// timeout) never count as a safety violation.
func Violation(decisions map[string]string) string {
	var commits, aborts []string
	for n, d := range decisions {
		switch d {
		case "commit":
			commits = append(commits, n)
		case "abort":
			aborts = append(aborts, n)
		}
	}
	if len(commits) == 0 || len(aborts) == 0 {
		return ""
	}
	sort.Strings(commits)
	sort.Strings(aborts)
	return "commit{" + strings.Join(commits, ",") + "} vs abort{" + strings.Join(aborts, ",") + "}"
}

// describe renders decisions compactly for diagnostics.
func describe(res Result) string {
	if len(res.Decisions) == 0 {
		return "no decisions"
	}
	var parts []string
	for _, n := range nodeOrder {
		if d, ok := res.Decisions[n]; ok {
			parts = append(parts, n+"="+d)
		}
	}
	return "decisions{" + strings.Join(parts, ",") + "}"
}
