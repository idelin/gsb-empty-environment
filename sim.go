package membersim

import (
	"container/heap"
	"fmt"
	"math/rand"
	"strings"
)

// Config tunes one simulation run. Seed fully determines the run together
// with the fault plan, so any run can be replayed event by event.
type Config struct {
	Seed             int64
	NumNodes         int
	Period           uint64 // protocol period in ticks
	ProbeTimeout     uint64 // direct probe deadline
	IndirectTimeout  uint64 // indirect (pingreq) deadline, starts after ProbeTimeout
	SuspicionTimeout uint64 // suspect -> dead deadline
	NumIndirect      int    // helpers used for indirect probes
	MinDelay         uint64 // network delay range, ticks
	MaxDelay         uint64
	DropProb         float64 // per-message loss probability
	DupProb          float64 // per-message duplication probability
	GossipRounds     int     // retransmissions per gossip item
	MaxPiggyback     int     // gossip updates per protocol message
}

func DefaultConfig() Config {
	return Config{
		Seed:             1,
		NumNodes:         5,
		Period:           10,
		ProbeTimeout:     4,
		IndirectTimeout:  8,
		SuspicionTimeout: 40,
		NumIndirect:      2,
		MinDelay:         1,
		MaxDelay:         2,
		GossipRounds:     10,
		MaxPiggyback:     4,
	}
}

// FaultKind enumerates injectable faults.
type FaultKind int

const (
	FaultCrash FaultKind = iota
	FaultRestart
	FaultPartition // cut Nodes off from the rest of the cluster
	FaultHeal      // remove all partition cuts
)

// Fault is one entry of the failure plan, applied at Tick.
type Fault struct {
	Tick  uint64
	Kind  FaultKind
	Nodes []int
}

// Transition records one membership-view change with its reason.
type Transition struct {
	Tick    uint64
	Node    int // observer whose view changed
	Subject int
	From    Member
	To      Member
	Reason  string
}

func (t Transition) String() string {
	return fmt.Sprintf("tick=%d node=%d view[%d]: %s(inc=%d) -> %s(inc=%d): %s",
		t.Tick, t.Node, t.Subject, t.From.State, t.From.Incarnation, t.To.State, t.To.Incarnation, t.Reason)
}

type eventKind int

const (
	evDeliver eventKind = iota
	evProbeTimeout
	evIndirectTimeout
	evSuspicion
	evProtocol
	evFault
)

// event is one entry in the global queue. Events are processed in total
// order of (tick, seq) where seq is a monotonically increasing id assigned
// at schedule time.
type event struct {
	tick        uint64
	seq         uint64
	kind        eventKind
	node        int
	target      int
	probeSeq    uint64
	incarnation uint64
	epoch       uint64
	msg         message
	fault       Fault
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

type sim struct {
	cfg Config
	rng *rand.Rand

	now uint64
	seq uint64
	pq  eventHeap

	nodes   []*node
	persist map[int]uint64 // durable incarnation store, survives crashes
	cuts    map[[2]int]bool

	transitions []Transition
	logs        []string
	selfBumps   map[int][]uint64 // every incarnation each node raised for itself

	delivered int
	dropped   int
}

// NewSim builds a cluster with a static bootstrap: every node knows the
// full node set as alive(inc=0). Faults are scheduled up front.
func NewSim(cfg Config, faults []Fault) *sim {
	s := &sim{
		cfg:       cfg,
		rng:       rand.New(rand.NewSource(cfg.Seed)),
		persist:   map[int]uint64{},
		cuts:      map[[2]int]bool{},
		selfBumps: map[int][]uint64{},
	}
	for i := 0; i < cfg.NumNodes; i++ {
		n := newNode(s, i)
		for j := 0; j < cfg.NumNodes; j++ {
			n.members[j] = Member{State: Alive}
		}
		s.nodes = append(s.nodes, n)
	}
	for i := range s.nodes {
		jitter := uint64(s.rng.Int63n(int64(cfg.Period)))
		s.schedule(1+jitter, event{kind: evProtocol, node: i})
	}
	for _, f := range faults {
		s.schedule(f.Tick, event{kind: evFault, fault: f})
	}
	return s
}

// Run processes events in (tick, seq) order until the queue is empty or
// the next event lies beyond `until`.
func (s *sim) Run(until uint64) {
	for s.pq.Len() > 0 {
		ev := heap.Pop(&s.pq).(event)
		if ev.tick > until {
			heap.Push(&s.pq, ev)
			break
		}
		s.now = ev.tick
		s.dispatch(ev)
	}
}

func (s *sim) schedule(tick uint64, ev event) {
	ev.tick = tick
	ev.seq = s.seq
	s.seq++
	heap.Push(&s.pq, ev)
}

func (s *sim) dispatch(ev event) {
	switch ev.kind {
	case evDeliver:
		s.nodes[ev.msg.to].onMessage(ev.msg)
	case evProbeTimeout:
		s.nodes[ev.node].onProbeTimeout(ev.target, ev.probeSeq)
	case evIndirectTimeout:
		s.nodes[ev.node].onIndirectTimeout(ev.target, ev.probeSeq)
	case evSuspicion:
		s.nodes[ev.node].onSuspicionTimeout(ev.target, ev.incarnation)
	case evProtocol:
		s.nodes[ev.node].onProtocolStep(ev.epoch)
	case evFault:
		s.applyFault(ev.fault)
	}
}

// --- network ---------------------------------------------------------------

// send puts a message on the wire. The network may drop, duplicate and
// reorder messages; partition cuts silently swallow crossing traffic.
func (s *sim) send(m message) {
	copies := 1
	if s.rng.Float64() < s.cfg.DupProb {
		copies = 2
	}
	for i := 0; i < copies; i++ {
		if s.isCut(m.from, m.to) {
			s.dropped++
			continue
		}
		if s.rng.Float64() < s.cfg.DropProb {
			s.dropped++
			continue
		}
		delay := s.cfg.MinDelay
		if s.cfg.MaxDelay > s.cfg.MinDelay {
			delay += uint64(s.rng.Int63n(int64(s.cfg.MaxDelay - s.cfg.MinDelay + 1)))
		}
		s.delivered++
		s.schedule(s.now+delay, event{kind: evDeliver, msg: m})
	}
}

func (s *sim) isCut(a, b int) bool {
	if a > b {
		a, b = b, a
	}
	return s.cuts[[2]int{a, b}]
}

// --- faults ----------------------------------------------------------------

func (s *sim) applyFault(f Fault) {
	switch f.Kind {
	case FaultCrash:
		for _, id := range f.Nodes {
			n := s.nodes[id]
			n.up = false
			n.epoch++ // invalidate queued protocol steps
			s.logf("fault: node %d crashed", id)
		}
	case FaultRestart:
		for _, id := range f.Nodes {
			s.restartNode(id)
		}
	case FaultPartition:
		inGroup := make(map[int]bool, len(f.Nodes))
		for _, id := range f.Nodes {
			inGroup[id] = true
		}
		for _, a := range f.Nodes {
			for _, other := range s.nodes {
				if inGroup[other.id] {
					continue
				}
				lo, hi := a, other.id
				if lo > hi {
					lo, hi = hi, lo
				}
				s.cuts[[2]int{lo, hi}] = true
			}
		}
		s.logf("fault: partition, cut %v off from the rest", f.Nodes)
	case FaultHeal:
		s.cuts = map[[2]int]bool{}
		s.logf("fault: partition healed")
	}
}

// restartNode reboots a node: the durable incarnation is loaded and
// incremented (by the node itself), volatile membership state is lost.
func (s *sim) restartNode(id int) {
	n := s.nodes[id]
	n.epoch++
	n.up = true
	n.incarnation = s.persist[id] + 1
	s.persist[id] = n.incarnation
	n.members = map[int]Member{id: {State: Alive, Incarnation: n.incarnation}}
	n.probes = map[uint64]*probe{}
	n.gossip = map[int]*gossipItem{}
	s.recordBump(id, n.incarnation)
	s.logf("fault: node %d restarted with incarnation %d", id, n.incarnation)
	upd := Update{Node: id, State: Alive, Incarnation: n.incarnation}
	n.addGossip(upd)
	for _, peer := range s.nodes {
		if peer.id != id {
			n.sendPush(peer.id, []Update{upd})
		}
	}
	s.schedule(s.now+s.cfg.Period, event{kind: evProtocol, node: id, epoch: n.epoch})
}

// --- logging / output ------------------------------------------------------

func (s *sim) recordBump(id int, inc uint64) {
	s.selfBumps[id] = append(s.selfBumps[id], inc)
}

func (s *sim) logTransition(observer, subject int, from, to Member, reason string) {
	s.transitions = append(s.transitions, Transition{
		Tick: s.now, Node: observer, Subject: subject, From: from, To: to, Reason: reason,
	})
}

func (s *sim) logf(format string, args ...interface{}) {
	s.logs = append(s.logs, fmt.Sprintf("tick=%d ", s.now)+fmt.Sprintf(format, args...))
}

// Transitions returns the full ordered transition log.
func (s *sim) Transitions() []Transition { return s.transitions }

// Logs returns the verbose event log (stale timers, probe phases, faults).
func (s *sim) Logs() []string { return s.logs }

// View returns a copy of one node's membership view.
func (s *sim) View(id int) map[int]Member {
	out := make(map[int]Member, len(s.nodes[id].members))
	for k, v := range s.nodes[id].members {
		out[k] = v
	}
	return out
}

// Report renders the transition log and every node's final view.
func (s *sim) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "== transitions (%d) ==\n", len(s.transitions))
	for _, t := range s.transitions {
		fmt.Fprintf(&b, "%s\n", t)
	}
	b.WriteString("== final views ==\n")
	for _, n := range s.nodes {
		fmt.Fprintf(&b, "node %d (up=%v inc=%d):\n", n.id, n.up, n.incarnation)
		for _, id := range n.sortedPeers() {
			m := n.members[id]
			fmt.Fprintf(&b, "  [%d] %s inc=%d\n", id, m.State, m.Incarnation)
		}
	}
	fmt.Fprintf(&b, "== network: delivered=%d dropped=%d ==\n", s.delivered, s.dropped)
	return b.String()
}
