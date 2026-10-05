// Package membership implements a discrete-event simulator for a SWIM-style
// cluster membership protocol.
//
// Members are in one of three states: alive, suspect or dead, each tagged
// with an incarnation number. Updates merge as follows:
//
//   - A higher incarnation always wins, regardless of state.
//   - At the same incarnation, dead > suspect > alive (escalation only).
//   - Only a node itself may raise its own incarnation, which it does to
//     refute a suspect/dead rumor about itself or when restarting.
//
// Probing: a direct probe timeout triggers a bounded number of indirect
// probes (ping-req via random helpers); only when all of them expire does
// the target become suspect. A suspect that is not refuted by a higher
// incarnation before its suspect timeout becomes dead.
//
// Every timer carries a generation token so a stale timer can never
// override a fresher state. The network may duplicate, reorder and drop
// messages. All events are processed in a total (tick, event ID) order, so
// a seed plus a fault plan fully determines the run and can be replayed.
package membership

import (
	"container/heap"
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

// State is the membership state of a cluster member. The declared order
// matches the merge priority at equal incarnation: dead > suspect > alive.
type State int

const (
	Alive State = iota
	Suspect
	Dead
)

func (s State) String() string {
	switch s {
	case Alive:
		return "alive"
	case Suspect:
		return "suspect"
	case Dead:
		return "dead"
	}
	return "unknown"
}

// MemberMeta is what a node believes about one member.
type MemberMeta struct {
	State       State
	Incarnation uint64
}

// Update is a single membership fact propagated by gossip.
type Update struct {
	Member      int
	State       State
	Incarnation uint64
}

// Transition records one state change in one node's local view.
type Transition struct {
	Tick        int
	Node        int // whose view changed
	Member      int // subject of the change
	From        State
	To          State
	Incarnation uint64
	Reason      string
}

// MsgKind enumerates wire message types.
type MsgKind int

const (
	MsgPing MsgKind = iota
	MsgPingReq
	MsgAck
	MsgGossip
)

func (k MsgKind) String() string {
	switch k {
	case MsgPing:
		return "ping"
	case MsgPingReq:
		return "ping-req"
	case MsgAck:
		return "ack"
	case MsgGossip:
		return "gossip"
	}
	return "?"
}

// Message is a wire message. Origin carries the original prober so that an
// ack can be relayed back through a ping-req helper.
type Message struct {
	From    int
	To      int
	Kind    MsgKind
	Target  int // ping-req target / ack subject
	Origin  int // original prober
	Updates []Update
}

// FaultKind enumerates injectable failures.
type FaultKind int

const (
	FaultCrash FaultKind = iota
	FaultRestart
	FaultPartitionStart
	FaultPartitionEnd
)

// Fault is a scheduled failure. Group is the member set on one side of the
// cut for FaultPartitionStart.
type Fault struct {
	Tick  int
	Kind  FaultKind
	Node  int
	Group []int
}

// Config tunes one simulation run.
type Config struct {
	NumNodes        int
	ProtocolPeriod  int // ticks between probe cycles
	DirectTimeout   int // ticks before a direct ping is declared lost
	IndirectTimeout int // ticks before all indirect probes are declared lost
	SuspectTimeout  int // ticks a member may remain suspect before death
	NumIndirect     int // number of ping-req helpers
	DropRate        float64
	DupRate         float64
	MaxDelay        int // delivery delay is uniform in [1, MaxDelay]
	Seed            int64
}

// Stats counts messages sent, by kind.
type Stats struct {
	Pings    int
	PingReqs int
	Acks     int
	Gossips  int
}

type eventKind int

const (
	evDeliver eventKind = iota
	evProtocolPeriod
	evDirectTimeout
	evIndirectTimeout
	evSuspectTimeout
	evCrash
	evRestart
	evPartitionStart
	evPartitionEnd
)

func (k eventKind) String() string {
	switch k {
	case evDeliver:
		return "deliver"
	case evProtocolPeriod:
		return "protocol-period"
	case evDirectTimeout:
		return "direct-timeout"
	case evIndirectTimeout:
		return "indirect-timeout"
	case evSuspectTimeout:
		return "suspect-timeout"
	case evCrash:
		return "crash"
	case evRestart:
		return "restart"
	case evPartitionStart:
		return "partition-start"
	case evPartitionEnd:
		return "partition-end"
	}
	return "?"
}

// event is one entry in the discrete-event queue. gen/inc form the
// generation token that invalidates stale timers.
type event struct {
	tick   int
	seq    int
	kind   eventKind
	node   int
	target int
	gen    uint64
	inc    uint64
	msg    Message
	group  []int
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
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

// Sim is the discrete-event simulator: event queue, network and nodes.
type Sim struct {
	Cfg   Config
	Stats Stats

	now   int
	rng   *rand.Rand
	queue eventHeap
	seq   int
	nodes []*Node

	partitionOn bool
	partitionA  map[int]bool

	// Log records every processed event in order; identical seed and fault
	// plan produce an identical log, which makes runs replayable.
	Log []string
}

// NewSim builds a simulation and schedules the initial protocol periods and
// the given fault plan.
func NewSim(cfg Config, faults []Fault) *Sim {
	s := &Sim{
		Cfg: cfg,
		rng: rand.New(rand.NewSource(cfg.Seed)),
	}
	for i := 0; i < cfg.NumNodes; i++ {
		s.nodes = append(s.nodes, newNode(i, cfg.NumNodes))
		s.push(event{kind: evProtocolPeriod, tick: 1 + i, node: i})
	}
	for _, f := range faults {
		ev := event{tick: f.Tick, node: f.Node, group: f.Group}
		switch f.Kind {
		case FaultCrash:
			ev.kind = evCrash
		case FaultRestart:
			ev.kind = evRestart
		case FaultPartitionStart:
			ev.kind = evPartitionStart
		case FaultPartitionEnd:
			ev.kind = evPartitionEnd
		}
		s.push(ev)
	}
	return s
}

// Run processes every event scheduled at or before the given tick, in
// (tick, event ID) order.
func (s *Sim) Run(until int) {
	for len(s.queue) > 0 && s.queue[0].tick <= until {
		ev := heap.Pop(&s.queue).(event)
		s.now = ev.tick
		s.dispatch(ev)
	}
}

// Now returns the current simulation tick.
func (s *Sim) Now() int { return s.now }

func (s *Sim) push(ev event) {
	ev.seq = s.seq
	s.seq++
	heap.Push(&s.queue, ev)
}

func (s *Sim) logf(format string, args ...interface{}) {
	s.Log = append(s.Log, fmt.Sprintf("t=%d seq=%d %s", s.now, s.seq, fmt.Sprintf(format, args...)))
}

func (s *Sim) dispatch(ev event) {
	switch ev.kind {
	case evDeliver:
		m := ev.msg
		s.logf("deliver %s %d->%d target=%d origin=%d", m.Kind, m.From, m.To, m.Target, m.Origin)
		s.nodes[ev.node].onMessage(s, m)
	case evProtocolPeriod:
		s.logf("protocol-period node=%d", ev.node)
		s.nodes[ev.node].onProtocolPeriod(s)
		s.push(event{kind: evProtocolPeriod, tick: s.now + s.Cfg.ProtocolPeriod, node: ev.node})
	case evDirectTimeout:
		s.logf("direct-timeout node=%d target=%d gen=%d", ev.node, ev.target, ev.gen)
		s.nodes[ev.node].onDirectTimeout(s, ev.target, ev.gen)
	case evIndirectTimeout:
		s.logf("indirect-timeout node=%d target=%d gen=%d", ev.node, ev.target, ev.gen)
		s.nodes[ev.node].onIndirectTimeout(s, ev.target, ev.gen)
	case evSuspectTimeout:
		s.logf("suspect-timeout node=%d target=%d inc=%d gen=%d", ev.node, ev.target, ev.inc, ev.gen)
		s.nodes[ev.node].onSuspectTimeout(s, ev.target, ev.inc, ev.gen)
	case evCrash:
		s.logf("crash node=%d", ev.node)
		s.nodes[ev.node].crash()
	case evRestart:
		s.logf("restart node=%d", ev.node)
		s.nodes[ev.node].restart(s)
	case evPartitionStart:
		s.logf("partition-start group=%v", ev.group)
		s.partitionOn = true
		s.partitionA = map[int]bool{}
		for _, id := range ev.group {
			s.partitionA[id] = true
		}
	case evPartitionEnd:
		s.logf("partition-end")
		s.partitionOn = false
		s.partitionA = nil
	}
}

// send queues a message for delivery, applying duplication, loss and the
// partition cut. Delivery to a crashed node is dropped on arrival.
func (s *Sim) send(from, to int, m Message) {
	m.From = from
	m.To = to
	switch m.Kind {
	case MsgPing:
		s.Stats.Pings++
	case MsgPingReq:
		s.Stats.PingReqs++
	case MsgAck:
		s.Stats.Acks++
	case MsgGossip:
		s.Stats.Gossips++
	}
	copies := 1
	if s.Cfg.DupRate > 0 && s.rng.Float64() < s.Cfg.DupRate {
		copies = 2
	}
	for i := 0; i < copies; i++ {
		if s.Cfg.DropRate > 0 && s.rng.Float64() < s.Cfg.DropRate {
			continue
		}
		if s.partitionOn && s.partitionA[from] != s.partitionA[to] {
			continue
		}
		delay := 1
		if s.Cfg.MaxDelay > 1 {
			delay += s.rng.Intn(s.Cfg.MaxDelay)
		}
		s.push(event{kind: evDeliver, tick: s.now + delay, node: to, msg: m})
	}
}

// broadcast gossips a single update to every other node, including members
// believed dead: a falsely condemned node must be able to learn of the
// rumor so it can refute it.
func (s *Sim) broadcast(from *Node, u Update) {
	for peer := 0; peer < s.Cfg.NumNodes; peer++ {
		if peer == from.id {
			continue
		}
		s.send(from.id, peer, Message{Kind: MsgGossip, Updates: []Update{u}})
	}
}

// View returns a copy of one node's membership view.
func (s *Sim) View(node int) map[int]MemberMeta {
	out := make(map[int]MemberMeta, len(s.nodes[node].view))
	for id, m := range s.nodes[node].view {
		out[id] = m
	}
	return out
}

// Transitions returns the state-change history of one node's view.
func (s *Sim) Transitions(node int) []Transition {
	out := make([]Transition, len(s.nodes[node].transitions))
	copy(out, s.nodes[node].transitions)
	return out
}

// Incarnation returns a node's own persistent incarnation.
func (s *Sim) Incarnation(node int) uint64 { return s.nodes[node].incarnation }

// Report renders every node's membership view and the reason for each
// transition it recorded.
func (s *Sim) Report() string {
	var b strings.Builder
	for _, n := range s.nodes {
		fmt.Fprintf(&b, "node %d (running=%v incarnation=%d)\n", n.id, n.running, n.incarnation)
		ids := make([]int, 0, len(n.view))
		for id := range n.view {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			m := n.view[id]
			fmt.Fprintf(&b, "  member %d: %s inc=%d\n", id, m.State, m.Incarnation)
		}
		for _, t := range n.transitions {
			fmt.Fprintf(&b, "  [t=%d] member %d: %s -> %s (inc=%d) %s\n",
				t.Tick, t.Member, t.From, t.To, t.Incarnation, t.Reason)
		}
	}
	return b.String()
}
