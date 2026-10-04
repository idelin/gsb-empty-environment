package membersim

import (
	"fmt"
	"sort"
)

type msgKind int

const (
	msgPing msgKind = iota
	msgAck
	msgPingReq
	msgGossip // targeted push of updates (refutations, view corrections)
)

type message struct {
	kind    msgKind
	from    int
	to      int
	seq     uint64
	target  int // pingreq: the node the helper should probe
	ackTo   int // ping: where the ack should be sent (relayed probes)
	updates []Update
}

// probe tracks an in-flight direct/indirect probe. The probe sequence
// number acts as the generation token for its timeout events: once the
// probe is completed (ack) or removed (suspected), queued timeout events
// carrying that sequence become stale and are ignored.
type probe struct {
	target   int
	indirect bool
}

type gossipItem struct {
	update    Update
	remaining int
}

type node struct {
	id  int
	sim *sim

	up          bool
	epoch       uint64 // bumped on crash/restart; invalidates queued protocol steps
	incarnation uint64 // own incarnation; persisted across restarts via sim.persist
	seq         uint64 // probe sequence source; monotonic across restarts

	members map[int]Member
	probes  map[uint64]*probe
	gossip  map[int]*gossipItem
}

func newNode(s *sim, id int) *node {
	return &node{
		id:      id,
		sim:     s,
		up:      true,
		members: map[int]Member{id: {State: Alive}},
		probes:  map[uint64]*probe{},
		gossip:  map[int]*gossipItem{},
	}
}

// --- protocol period -------------------------------------------------------

func (n *node) onProtocolStep(epoch uint64) {
	if !n.up || epoch != n.epoch {
		return // stale step from before a crash/restart
	}
	n.sim.schedule(n.sim.now+n.sim.cfg.Period, event{kind: evProtocol, node: n.id, epoch: epoch})
	target, ok := n.pickPeer()
	if !ok {
		return
	}
	n.seq++
	seq := n.seq
	n.probes[seq] = &probe{target: target}
	n.send0(message{kind: msgPing, from: n.id, to: target, seq: seq, ackTo: n.id})
	n.sim.schedule(n.sim.now+n.sim.cfg.ProbeTimeout, event{kind: evProbeTimeout, node: n.id, target: target, probeSeq: seq})
}

// onProbeTimeout fires when the direct probe's ack did not arrive in time.
// The node does NOT suspect yet: it starts indirect probes and waits for
// the indirect deadline.
func (n *node) onProbeTimeout(target int, seq uint64) {
	if !n.up {
		return
	}
	p, ok := n.probes[seq]
	if !ok || p.target != target || p.indirect {
		n.sim.logf("node=%d: stale direct-probe timer seq=%d target=%d ignored", n.id, seq, target)
		return
	}
	p.indirect = true
	helpers := n.pickHelpers(target)
	for _, h := range helpers {
		n.send0(message{kind: msgPingReq, from: n.id, to: h, seq: seq, target: target})
	}
	n.sim.logf("node=%d: direct probe seq=%d to %d expired; started %d indirect probes", n.id, seq, target, len(helpers))
	n.sim.schedule(n.sim.now+n.sim.cfg.IndirectTimeout, event{kind: evIndirectTimeout, node: n.id, target: target, probeSeq: seq})
}

// onIndirectTimeout fires only after every indirect probe has had its
// chance; if no ack (direct or relayed) arrived by now, suspect the target.
func (n *node) onIndirectTimeout(target int, seq uint64) {
	if !n.up {
		return
	}
	p, ok := n.probes[seq]
	if !ok || p.target != target {
		n.sim.logf("node=%d: stale indirect-probe timer seq=%d target=%d ignored (ack arrived)", n.id, seq, target)
		return
	}
	delete(n.probes, seq)
	cur := n.members[target]
	n.applyMember(target, Member{State: Suspect, Incarnation: cur.Incarnation},
		fmt.Sprintf("probe seq=%d: direct and all indirect probes expired without ack", seq))
}

// onSuspicionTimeout declares the member dead only if the suspicion token
// (target, incarnation) still matches the current view exactly. A fresher
// alive/suspect/dead makes the queued timer stale.
func (n *node) onSuspicionTimeout(target int, incarnation uint64) {
	if !n.up {
		return
	}
	cur, ok := n.members[target]
	if !ok || cur.State != Suspect || cur.Incarnation != incarnation {
		n.sim.logf("node=%d: stale suspicion timer target=%d inc=%d ignored (view=%v)", n.id, target, incarnation, cur)
		return
	}
	n.applyMember(target, Member{State: Dead, Incarnation: incarnation},
		"suspicion timer expired without a higher-incarnation refutation")
}

// --- messages --------------------------------------------------------------

func (n *node) onMessage(m message) {
	if !n.up {
		return // crashed nodes drop everything
	}
	for _, u := range m.updates {
		n.applyUpdate(u, fmt.Sprintf("gossip from %d", m.from))
	}
	switch m.kind {
	case msgPing:
		n.send0(message{kind: msgAck, from: n.id, to: m.ackTo, seq: m.seq})
	case msgAck:
		if p, ok := n.probes[m.seq]; ok && p.target == m.from {
			delete(n.probes, m.seq) // direct or relayed ack completes the probe
		}
	case msgPingReq:
		if m.target == n.id {
			n.send0(message{kind: msgAck, from: n.id, to: m.from, seq: m.seq})
		} else {
			n.send0(message{kind: msgPing, from: n.id, to: m.target, seq: m.seq, ackTo: m.from})
		}
	case msgGossip:
		// updates already applied above
	}
	// If the sender is someone we currently doubt, tell them what we think
	// so they can refute it themselves. Nobody else may raise their
	// incarnation for them.
	if m.from != n.id {
		if view, ok := n.members[m.from]; ok && view.State != Alive {
			n.sendPush(m.from, []Update{{Node: m.from, State: view.State, Incarnation: view.Incarnation}})
		}
	}
}

// --- membership view -------------------------------------------------------

// applyUpdate merges one gossiped fact. Facts about the local node never
// overwrite local state directly; a suspect/dead rumor about self triggers
// a refutation with a self-raised incarnation instead.
func (n *node) applyUpdate(u Update, reason string) {
	if u.Node == n.id {
		if u.State != Alive && u.Incarnation >= n.incarnation {
			n.refute(u)
		}
		return
	}
	n.applyMember(u.Node, Member{State: u.State, Incarnation: u.Incarnation}, reason)
}

// applyMember is the single funnel for membership changes about other
// nodes. It schedules a suspicion timer (generation-tokened by
// incarnation) whenever the view becomes suspect.
func (n *node) applyMember(id int, m Member, reason string) {
	cur, known := n.members[id]
	if !known {
		cur = Member{State: Alive}
		reason += " (previously unknown member)"
	}
	merged, changed := mergeMembers(cur, m)
	if !known {
		merged, changed = m, true
	}
	if !changed {
		return
	}
	n.members[id] = merged
	n.sim.logTransition(n.id, id, cur, merged, reason)
	n.addGossip(Update{Node: id, State: merged.State, Incarnation: merged.Incarnation})
	if merged.State == Suspect {
		n.sim.schedule(n.sim.now+n.sim.cfg.SuspicionTimeout,
			event{kind: evSuspicion, node: n.id, target: id, incarnation: merged.Incarnation})
	}
}

// refute answers a rumor about the local node by raising its own
// incarnation and broadcasting a fresh alive. This is the ONLY place
// (besides restart) where an incarnation is incremented.
func (n *node) refute(u Update) {
	old := n.incarnation
	n.incarnation = u.Incarnation + 1
	n.sim.persist[n.id] = n.incarnation
	prev := n.members[n.id]
	n.members[n.id] = Member{State: Alive, Incarnation: n.incarnation}
	n.sim.recordBump(n.id, n.incarnation)
	n.sim.logTransition(n.id, n.id, prev, n.members[n.id],
		fmt.Sprintf("refuted %s(inc=%d) rumor about self by raising incarnation %d->%d", u.State, u.Incarnation, old, n.incarnation))
	upd := Update{Node: n.id, State: Alive, Incarnation: n.incarnation}
	n.addGossip(upd)
	for _, peer := range n.sortedPeers() {
		n.sendPush(peer, []Update{upd})
	}
}

// --- gossip ----------------------------------------------------------------

func (n *node) addGossip(u Update) {
	if item, ok := n.gossip[u.Node]; ok {
		if merged, changed := mergeMembers(
			Member{State: item.update.State, Incarnation: item.update.Incarnation},
			Member{State: u.State, Incarnation: u.Incarnation}); changed {
			item.update = u
			item.remaining = n.sim.cfg.GossipRounds
			_ = merged
		} else {
			item.remaining = n.sim.cfg.GossipRounds // re-announce
		}
		return
	}
	n.gossip[u.Node] = &gossipItem{update: u, remaining: n.sim.cfg.GossipRounds}
}

func (n *node) takeGossip() []Update {
	if len(n.gossip) == 0 {
		return nil
	}
	keys := make([]int, 0, len(n.gossip))
	for k := range n.gossip {
		keys = append(keys, k)
	}
	sort.Ints(keys) // deterministic piggyback order for replayability
	var out []Update
	for _, k := range keys {
		if len(out) >= n.sim.cfg.MaxPiggyback {
			break
		}
		item := n.gossip[k]
		out = append(out, item.update)
		item.remaining--
		if item.remaining <= 0 {
			delete(n.gossip, k)
		}
	}
	return out
}

// --- sending ---------------------------------------------------------------

// send0 attaches gossip plus a self heartbeat and hands the message to the
// (lossy, reordering, duplicating) simulated network.
func (n *node) send0(m message) {
	m.updates = append(m.updates, n.takeGossip()...)
	m.updates = append(m.updates, Update{Node: n.id, State: Alive, Incarnation: n.incarnation})
	n.sim.send(m)
}

// sendPush sends a targeted gossip push without draining the gossip buffer.
func (n *node) sendPush(to int, updates []Update) {
	n.sim.send(message{kind: msgGossip, from: n.id, to: to, updates: updates})
}

// --- peer selection --------------------------------------------------------

func (n *node) sortedPeers() []int {
	ids := make([]int, 0, len(n.members))
	for id := range n.members {
		if id != n.id {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

// pickPeer probes any known peer, including dead ones, so that healed
// partitions and restarted nodes are discovered again.
func (n *node) pickPeer() (int, bool) {
	peers := n.sortedPeers()
	if len(peers) == 0 {
		return 0, false
	}
	return peers[n.sim.rng.Intn(len(peers))], true
}

func (n *node) pickHelpers(target int) []int {
	cand := make([]int, 0, len(n.members))
	for id := range n.members {
		if id != n.id && id != target {
			cand = append(cand, id)
		}
	}
	sort.Ints(cand)
	out := make([]int, 0, n.sim.cfg.NumIndirect)
	for len(out) < n.sim.cfg.NumIndirect && len(cand) > 0 {
		j := n.sim.rng.Intn(len(cand))
		out = append(out, cand[j])
		cand = append(cand[:j], cand[j+1:]...)
	}
	return out
}
