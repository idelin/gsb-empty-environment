// Package swim implements a minimal SWIM-style failure detector that runs on
// top of the deterministic discrete-event simulator.
//
// Determinism rules honoured everywhere in this file:
//   - no wall clock, no goroutines, no real randomness;
//   - map iteration is always done over sorted keys;
//   - probe targets are chosen round-robin (no PRNG inside the node);
//   - conflicting membership claims are resolved by the deterministic
//     arbitration rules in arbitrateDead / refute.
package swim

import (
	"sort"

	"swimlab/sim"
)

// State is the membership state of a member.
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

// Member is the local view of a peer.
type Member struct {
	ID          string
	State       State
	Incarnation uint64
}

// Wire messages.
type Ping struct{ Seq uint64 }
type Ack struct {
	Seq         uint64
	Incarnation uint64
}
type AliveMsg struct {
	ID          string
	Incarnation uint64
}
type SuspectMsg struct {
	ID          string
	Incarnation uint64
}
type DeadMsg struct {
	ID          string
	Incarnation uint64
}

// Config holds protocol timing, in simulator ticks.
type Config struct {
	Period         uint64 // ticks between probes
	AckTimeout     uint64 // ticks to wait for an ack before suspecting
	SuspectTimeout uint64 // ticks a member may stay suspect before being declared dead
}

type pendingPing struct {
	target string
	sentAt uint64
}

// Node is a SWIM protocol participant.
type Node struct {
	id          string
	sim         *sim.Simulator
	cfg         Config
	incarnation uint64
	members     map[string]*Member
	probeIdx    int
	seq         uint64
	pending     map[uint64]pendingPing
	suspectAt   map[string]uint64

	// Instrumentation for tests.
	DeclaredDead map[string]uint64 // peer -> tick at which this node declared it dead
	Refutations  []uint64          // ticks at which this node refuted its own death
}

func NewNode(id string, s *sim.Simulator, cfg Config) *Node {
	return &Node{
		id:           id,
		sim:          s,
		cfg:          cfg,
		members:      make(map[string]*Member),
		pending:      make(map[uint64]pendingPing),
		suspectAt:    make(map[string]uint64),
		DeclaredDead: make(map[string]uint64),
	}
}

func (n *Node) ID() string { return n.id }

// Incarnation returns the node's own current incarnation number.
func (n *Node) Incarnation() uint64 { return n.incarnation }

// AddMember registers a peer at incarnation 0.
func (n *Node) AddMember(id string) {
	n.members[id] = &Member{ID: id, State: Alive}
}

// MemberState reports the local view of a peer (for tests and the demo).
func (n *Node) MemberState(id string) (State, uint64, bool) {
	m, ok := n.members[id]
	if !ok {
		return Alive, 0, false
	}
	return m.State, m.Incarnation, true
}

func (n *Node) sortedMemberIDs() []string {
	ids := make([]string, 0, len(n.members))
	for id := range n.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (n *Node) sortedPendingSeqs() []uint64 {
	seqs := make([]uint64, 0, len(n.pending))
	for seq := range n.pending {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs
}

// OnTick drives the protocol: ack timeouts, suspect timeouts, then probing.
func (n *Node) OnTick(now uint64) {
	// 1. Failed probes turn into suspicions.
	for _, seq := range n.sortedPendingSeqs() {
		p := n.pending[seq]
		if now-p.sentAt >= n.cfg.AckTimeout {
			delete(n.pending, seq)
			n.suspect(p.target, now)
		}
	}
	// 2. Suspicions that outlive the suspect timeout become death
	//    declarations.
	for _, id := range n.sortedMemberIDs() {
		m := n.members[id]
		if m.State == Suspect && now-n.suspectAt[id] >= n.cfg.SuspectTimeout {
			n.declareDead(m, now)
		}
	}
	// 3. Periodic probe of the next round-robin target.
	if now%n.cfg.Period == 0 {
		n.probe(now)
	}
}

func (n *Node) probe(now uint64) {
	targets := make([]string, 0, len(n.members))
	for _, id := range n.sortedMemberIDs() {
		if n.members[id].State != Dead {
			targets = append(targets, id)
		}
	}
	if len(targets) == 0 {
		return
	}
	t := targets[n.probeIdx%len(targets)]
	n.probeIdx++
	n.seq++
	n.pending[n.seq] = pendingPing{target: t, sentAt: now}
	n.sim.Send(n.id, t, Ping{Seq: n.seq})
}

func (n *Node) suspect(id string, now uint64) {
	m := n.members[id]
	if m == nil || m.State != Alive {
		return
	}
	m.State = Suspect
	n.suspectAt[id] = now
	n.sim.Logf("node %s suspects %s (inc=%d)", n.id, id, m.Incarnation)
	n.broadcast(SuspectMsg{ID: id, Incarnation: m.Incarnation})
}

func (n *Node) declareDead(m *Member, now uint64) {
	m.State = Dead
	delete(n.suspectAt, m.ID)
	n.DeclaredDead[m.ID] = now
	n.sim.Logf("node %s DECLARES %s DEAD (inc=%d)", n.id, m.ID, m.Incarnation)
	n.broadcast(DeadMsg{ID: m.ID, Incarnation: m.Incarnation})
}

// OnMessage handles an incoming protocol message.
func (n *Node) OnMessage(from string, msg any, now uint64) {
	switch m := msg.(type) {
	case Ping:
		n.sim.Send(n.id, from, Ack{Seq: m.Seq, Incarnation: n.incarnation})
	case Ack:
		delete(n.pending, m.Seq)
		n.touchAlive(from, m.Incarnation, now)
	case AliveMsg:
		n.mergeAlive(m, now)
	case SuspectMsg:
		n.mergeSuspect(m, now)
	case DeadMsg:
		n.arbitrateDead(m, now)
	}
}

// touchAlive treats a direct ack as proof of life: it clears a suspicion
// raised against the acking member at the same or older incarnation.
func (n *Node) touchAlive(from string, inc uint64, now uint64) {
	m := n.members[from]
	if m == nil || m.State == Dead {
		return
	}
	if inc >= m.Incarnation {
		m.Incarnation = inc
		if m.State == Suspect {
			m.State = Alive
			delete(n.suspectAt, from)
			n.sim.Logf("node %s clears suspicion of %s (ack inc=%d)", n.id, from, inc)
		}
	}
}

func (n *Node) mergeAlive(msg AliveMsg, now uint64) {
	if msg.ID == n.id {
		if msg.Incarnation > n.incarnation {
			n.incarnation = msg.Incarnation
		}
		return
	}
	m, ok := n.members[msg.ID]
	if !ok {
		m = &Member{ID: msg.ID}
		n.members[msg.ID] = m
	}
	if msg.Incarnation < m.Incarnation {
		return
	}
	if msg.Incarnation == m.Incarnation && m.State != Suspect {
		return
	}
	was := m.State
	m.Incarnation = msg.Incarnation
	m.State = Alive
	delete(n.suspectAt, msg.ID)
	if was == Dead {
		n.sim.Logf("node %s REVIVES %s (inc=%d) after refutation", n.id, msg.ID, msg.Incarnation)
	} else if was == Suspect {
		n.sim.Logf("node %s clears suspicion of %s (alive inc=%d)", n.id, msg.ID, msg.Incarnation)
	}
}

func (n *Node) mergeSuspect(msg SuspectMsg, now uint64) {
	if msg.ID == n.id {
		// Suspicion about ourselves: refute with a bumped incarnation.
		if msg.Incarnation >= n.incarnation {
			n.refute(msg.Incarnation, now, "suspect")
		}
		return
	}
	m := n.members[msg.ID]
	if m == nil || m.State == Dead {
		return
	}
	if msg.Incarnation < m.Incarnation {
		return
	}
	if msg.Incarnation == m.Incarnation && m.State == Suspect {
		return
	}
	m.Incarnation = msg.Incarnation
	m.State = Suspect
	n.suspectAt[msg.ID] = now
	n.sim.Logf("node %s suspects %s via gossip (inc=%d)", n.id, msg.ID, msg.Incarnation)
}

// arbitrateDead is the deterministic arbitration point for death
// declarations. Rules, applied in order:
//
//  1. A live node ALWAYS refutes its own death declaration by bumping its
//     incarnation above the claim and broadcasting an Alive. No peer can
//     kill a node that can still speak.
//  2. For claims about others, a stale claim (lower incarnation than the
//     locally known one) loses and is dropped.
//  3. Equal-incarnation conflicts are resolved by processing order, which
//     the simulator guarantees to be the deterministic (tick, seq) order —
//     so the outcome is identical on every replay of the same seed.
func (n *Node) arbitrateDead(msg DeadMsg, now uint64) {
	if msg.ID == n.id {
		n.sim.Logf("node %s ARBITRATES own death claim (inc=%d): refuting", n.id, msg.Incarnation)
		n.refute(msg.Incarnation, now, "dead")
		return
	}
	m := n.members[msg.ID]
	if m == nil {
		return
	}
	if msg.Incarnation < m.Incarnation {
		n.sim.Logf("node %s ARBITRATES stale death claim on %s (claim inc=%d < known inc=%d): dropped",
			n.id, msg.ID, msg.Incarnation, m.Incarnation)
		return
	}
	if m.State == Dead {
		return
	}
	m.Incarnation = msg.Incarnation
	m.State = Dead
	delete(n.suspectAt, msg.ID)
	n.sim.Logf("node %s accepts death of %s (inc=%d)", n.id, msg.ID, msg.Incarnation)
}

// refute bumps the local incarnation above the offending claim and
// broadcasts an Alive. This is the only way a node defends itself.
func (n *Node) refute(claimInc uint64, now uint64, reason string) {
	if claimInc >= n.incarnation {
		n.incarnation = claimInc + 1
	} else {
		n.incarnation++
	}
	n.Refutations = append(n.Refutations, now)
	n.sim.Logf("node %s REFUTES %s claim, incarnation -> %d", n.id, reason, n.incarnation)
	n.broadcast(AliveMsg{ID: n.id, Incarnation: n.incarnation})
}

// broadcast gossips a message to every known peer in sorted ID order.
func (n *Node) broadcast(msg any) {
	for _, id := range n.sortedMemberIDs() {
		n.sim.Send(n.id, id, msg)
	}
}
