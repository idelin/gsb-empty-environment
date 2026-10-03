// Package swim implements a SWIM-style failure detector on top of the
// deterministic discrete-event simulator. Every source of ordering
// (peer iteration, gossip buffers, probe scheduling) is deterministic,
// so a fixed seed reproduces a run bit-for-bit, including races.
package swim

import (
	"fmt"
	"sort"

	"swimdsim/sim"
)

// State is the membership state of a node.
type State int

const (
	Alive State = iota
	Suspect
	Dead
)

func (s State) String() string {
	switch s {
	case Alive:
		return "ALIVE"
	case Suspect:
		return "SUSPECT"
	case Dead:
		return "DEAD"
	}
	return "?"
}

// Update is a membership rumor: node ID is in State at Incarnation.
type Update struct {
	ID          string
	State       State
	Incarnation uint64
}

// Member is the local view of one node.
type Member struct {
	ID          string
	State       State
	Incarnation uint64
}

// MsgType enumerates the wire protocol messages.
type MsgType int

const (
	Ping MsgType = iota
	Ack
	PingReq
)

// Envelope is a protocol message. Membership updates are always
// piggybacked (infection-style dissemination).
type Envelope struct {
	Type    MsgType
	From    string
	Target  string // Ping/PingReq: probed node; Ack: node this ack answers for
	Seq     uint64
	Updates []Update
}

// Config holds the protocol timing, in simulator ticks.
type Config struct {
	ProtocolPeriod  uint64 // ticks between probes
	AckTimeout      uint64 // direct probe timeout
	IndirectTimeout uint64 // extra timeout after PingReq fan-out
	SuspectTimeout  uint64 // how long a suspect may refute before Dead
	NumIndirect     int    // PingReq fan-out width
	Dissemination   int    // how many protocol periods a rumor is gossiped
	MaxPiggyback    int    // max updates attached to one message
}

// DefaultConfig returns timings tuned for the simulator.
func DefaultConfig() Config {
	return Config{
		ProtocolPeriod:  20,
		AckTimeout:      6,
		IndirectTimeout: 6,
		SuspectTimeout:  100,
		NumIndirect:     2,
		Dissemination:   30,
		MaxPiggyback:    8,
	}
}

type relayReq struct {
	origin string
	seq    uint64
	target string
}

type gossipItem struct {
	u    Update
	left int // remaining protocol periods to gossip
}

// Node is a single SWIM protocol participant.
type Node struct {
	id  string
	sim *sim.Simulator
	net *sim.Switch
	cfg Config

	members map[string]*Member
	order   []string // sorted peer ids: deterministic probe order
	next    int

	seq          uint64
	pending      map[string]uint64 // probe target -> outstanding seq
	relay        map[uint64]relayReq
	suspectSince map[string]uint64

	gossip []gossipItem

	// Logf, if set, receives deterministic, tick-prefixed log lines.
	Logf func(format string, args ...any)
}

// NewNode creates a node that knows the full peer list at boot
// (static membership, like the tests use). The first protocol step
// runs at tick 1.
func NewNode(s *sim.Simulator, net *sim.Switch, cfg Config, id string, peers []string) *Node {
	n := &Node{
		id:           id,
		sim:          s,
		net:          net,
		cfg:          cfg,
		members:      make(map[string]*Member),
		pending:      make(map[string]uint64),
		relay:        make(map[uint64]relayReq),
		suspectSince: make(map[string]uint64),
	}
	n.members[id] = &Member{ID: id, State: Alive}
	for _, p := range peers {
		if p == id {
			continue
		}
		n.members[p] = &Member{ID: p, State: Alive}
		n.order = append(n.order, p)
	}
	sort.Strings(n.order)
	net.Attach(id, n.handle)
	s.After(1, n.protocolStep)
	return n
}

// ID returns the node's address.
func (n *Node) ID() string { return n.id }

// View returns the local view of member id.
func (n *Node) View(id string) Member { return *n.members[id] }

// Members returns the local membership table, sorted by ID.
func (n *Node) Members() []Member {
	out := make([]Member, 0, len(n.members))
	for _, id := range n.sortedIDs() {
		out = append(out, *n.members[id])
	}
	return out
}

func (n *Node) sortedIDs() []string {
	ids := make([]string, 0, len(n.members))
	for id := range n.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (n *Node) log(format string, args ...any) {
	if n.Logf == nil {
		return
	}
	args = append([]any{n.sim.Now(), n.id}, args...)
	n.Logf("tick=%04d node=%s "+format, args...)
}

// --- protocol machinery -------------------------------------------------

func (n *Node) protocolStep() {
	n.expireSuspects()
	n.probe()
	n.ageGossip()
	n.sim.After(n.cfg.ProtocolPeriod, n.protocolStep)
}

func (n *Node) probe() {
	if len(n.order) == 0 {
		return
	}
	for i := 0; i < len(n.order); i++ {
		id := n.order[n.next]
		n.next = (n.next + 1) % len(n.order)
		if _, ok := n.pending[id]; ok {
			continue
		}
		n.startProbe(id)
		return
	}
}

func (n *Node) startProbe(target string) {
	n.seq++
	seq := n.seq
	n.pending[target] = seq
	n.send(target, Envelope{Type: Ping, From: n.id, Target: target, Seq: seq})
	n.sim.After(n.cfg.AckTimeout, func() {
		if n.pending[target] != seq {
			return // acked in time
		}
		n.startIndirect(target, seq)
	})
}

func (n *Node) startIndirect(target string, seq uint64) {
	for _, h := range n.pickHelpers(target) {
		n.send(h, Envelope{Type: PingReq, From: n.id, Target: target, Seq: seq})
	}
	n.sim.After(n.cfg.IndirectTimeout, func() {
		if n.pending[target] != seq {
			return
		}
		delete(n.pending, target)
		n.suspect(target)
	})
}

func (n *Node) pickHelpers(target string) []string {
	var out []string
	for _, id := range n.order {
		if id == target {
			continue
		}
		if n.members[id].State == Dead {
			continue
		}
		out = append(out, id)
		if len(out) == n.cfg.NumIndirect {
			break
		}
	}
	return out
}

func (n *Node) suspect(id string) {
	m := n.members[id]
	if m.State != Alive {
		return
	}
	m.State = Suspect
	n.suspectSince[id] = n.sim.Now()
	n.log("suspect %s inc=%d (local probe failed)", id, m.Incarnation)
	n.publish(Update{ID: id, State: Suspect, Incarnation: m.Incarnation})
}

func (n *Node) expireSuspects() {
	now := n.sim.Now()
	for _, id := range n.order { // deterministic iteration
		since, ok := n.suspectSince[id]
		if !ok || now-since < n.cfg.SuspectTimeout {
			continue
		}
		delete(n.suspectSince, id)
		m := n.members[id]
		if m.State != Suspect {
			continue
		}
		m.State = Dead
		n.log("declare-dead %s inc=%d", id, m.Incarnation)
		n.publish(Update{ID: id, State: Dead, Incarnation: m.Incarnation})
	}
}

// --- message handling ---------------------------------------------------

func (n *Node) send(to string, env Envelope) {
	env.Updates = n.piggyback()
	n.net.Send(n.id, to, env)
}

func (n *Node) handle(from string, msg any) {
	env := msg.(Envelope)
	for _, u := range env.Updates {
		n.applyUpdate(u)
	}
	switch env.Type {
	case Ping:
		n.send(env.From, Envelope{Type: Ack, From: n.id, Target: env.Target, Seq: env.Seq})
	case PingReq:
		n.handlePingReq(env)
	case Ack:
		n.handleAck(env)
	}
}

func (n *Node) handlePingReq(env Envelope) {
	if env.Target == n.id {
		// We are the target: answer the originator directly.
		n.send(env.From, Envelope{Type: Ack, From: n.id, Target: n.id, Seq: env.Seq})
		return
	}
	if _, ok := n.members[env.Target]; !ok {
		return
	}
	n.seq++
	seq := n.seq
	n.relay[seq] = relayReq{origin: env.From, seq: env.Seq, target: env.Target}
	n.send(env.Target, Envelope{Type: Ping, From: n.id, Target: env.Target, Seq: seq})
	// Clean up the relay entry whether or not the target answers.
	n.sim.After(n.cfg.AckTimeout+n.cfg.IndirectTimeout, func() {
		delete(n.relay, seq)
	})
}

func (n *Node) handleAck(env Envelope) {
	if r, ok := n.relay[env.Seq]; ok {
		delete(n.relay, env.Seq)
		n.send(r.origin, Envelope{Type: Ack, From: n.id, Target: r.target, Seq: r.seq})
		return
	}
	if n.pending[env.Target] == env.Seq {
		delete(n.pending, env.Target)
	}
}

// --- arbitration --------------------------------------------------------

// applyUpdate is the arbitration core. Updates are totally ordered by
// (incarnation, state) with Dead > Suspect > Alive at equal
// incarnation; a rumor about ourselves is always refuted by bumping
// our own incarnation above it. Because the ordering is total and the
// simulator delivers events in a fixed order, arbitration is fully
// deterministic.
func (n *Node) applyUpdate(u Update) {
	if u.ID == n.id {
		n.refute(u)
		return
	}
	m, ok := n.members[u.ID]
	if !ok {
		return // static membership: ignore strangers
	}
	if u.Incarnation < m.Incarnation {
		return
	}
	if u.Incarnation == m.Incarnation && u.State <= m.State {
		return
	}
	m.State = u.State
	m.Incarnation = u.Incarnation
	switch u.State {
	case Alive:
		delete(n.suspectSince, u.ID)
	case Suspect:
		n.suspectSince[u.ID] = n.sim.Now()
		n.log("suspect %s inc=%d (learned via gossip)", u.ID, u.Incarnation)
	case Dead:
		delete(n.suspectSince, u.ID)
		n.log("declare-dead %s inc=%d (learned via gossip)", u.ID, u.Incarnation)
	}
	n.publish(u)
}

// refute answers a rumor about ourselves: bump the incarnation above
// the rumor and broadcast Alive. Highest incarnation always wins, so
// a live node can never be convicted by a stale Suspect/Dead rumor.
func (n *Node) refute(u Update) {
	if u.State == Alive {
		return
	}
	self := n.members[n.id]
	if u.Incarnation < self.Incarnation {
		return
	}
	self.Incarnation = u.Incarnation + 1
	n.log("refute %s-rumor inc=%d -> bump incarnation to %d",
		u.State, u.Incarnation, self.Incarnation)
	n.publish(Update{ID: n.id, State: Alive, Incarnation: self.Incarnation})
}

// --- gossip -------------------------------------------------------------

func (n *Node) publish(u Update) {
	// A newer update about a node supersedes any older rumor about the
	// same node still sitting in the buffer: updates for one ID are
	// totally ordered, so keeping only the freshest is safe and keeps
	// the buffer small.
	kept := n.gossip[:0]
	for _, g := range n.gossip {
		if g.u.ID != u.ID {
			kept = append(kept, g)
		}
	}
	n.gossip = append(kept, gossipItem{u: u, left: n.cfg.Dissemination})
}

func (n *Node) piggyback() []Update {
	if len(n.gossip) == 0 {
		return nil
	}
	// Freshest updates first: refutations must never starve behind
	// older rumors.
	limit := n.cfg.MaxPiggyback
	if len(n.gossip) < limit {
		limit = len(n.gossip)
	}
	out := make([]Update, limit)
	for i := 0; i < limit; i++ {
		out[i] = n.gossip[len(n.gossip)-1-i].u
	}
	return out
}

func (n *Node) ageGossip() {
	kept := n.gossip[:0]
	for _, g := range n.gossip {
		g.left--
		if g.left > 0 {
			kept = append(kept, g)
		}
	}
	n.gossip = kept
}

func (m Member) String() string {
	return fmt.Sprintf("%s=%s(inc=%d)", m.ID, m.State, m.Incarnation)
}
