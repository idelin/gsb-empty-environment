package membership

import (
	"fmt"
	"sort"
)

type probeStage int

const (
	stageDirect probeStage = iota
	stageIndirect
)

// pendingProbe tracks an in-flight probe cycle against one target. gen is
// the generation token: any timeout arriving with an older gen is stale.
type pendingProbe struct {
	gen   uint64
	stage probeStage
}

// modelInput is one input to the independent state model used by tests:
// either an update the node observed, or a restart of the node itself.
type modelInput struct {
	update  Update
	restart bool
}

// Node is one simulated cluster member.
type Node struct {
	id      int
	running bool

	// incarnation is the node's own persistent incarnation. It survives
	// crashes and is only ever raised by the node itself.
	incarnation uint64

	view       map[int]MemberMeta
	probes     map[int]pendingProbe
	suspectGen map[int]uint64 // suspect-timer generation token per member
	timerGen   uint64

	transitions []Transition
	received    []modelInput // every observed update, for model cross-checks
}

func newNode(id, numNodes int) *Node {
	n := &Node{
		id:         id,
		running:    true,
		view:       make(map[int]MemberMeta, numNodes),
		probes:     map[int]pendingProbe{},
		suspectGen: map[int]uint64{},
	}
	for i := 0; i < numNodes; i++ {
		n.view[i] = MemberMeta{State: Alive}
	}
	return n
}

// onProtocolPeriod runs one probe cycle: pick a live-ish target, ping it
// directly, and gossip the full local view to everyone. Full-view gossip is
// what guarantees convergence once the network stops losing messages.
func (n *Node) onProtocolPeriod(s *Sim) {
	if !n.running {
		return
	}
	if target, ok := n.pickTarget(s); ok {
		if _, pending := n.probes[target]; !pending {
			n.timerGen++
			gen := n.timerGen
			n.probes[target] = pendingProbe{gen: gen, stage: stageDirect}
			s.send(n.id, target, Message{Kind: MsgPing, Origin: n.id, Updates: n.fullViewUpdates()})
			s.push(event{kind: evDirectTimeout, tick: s.now + s.Cfg.DirectTimeout, node: n.id, target: target, gen: gen})
		}
	}
	updates := n.fullViewUpdates()
	for peer := 0; peer < s.Cfg.NumNodes; peer++ {
		if peer == n.id {
			continue
		}
		s.send(n.id, peer, Message{Kind: MsgGossip, Updates: updates})
	}
}

// onDirectTimeout escalates a lost direct ping to indirect probes. The
// suspect decision is deferred until every indirect probe has also expired.
func (n *Node) onDirectTimeout(s *Sim, target int, gen uint64) {
	if !n.running {
		return
	}
	p, ok := n.probes[target]
	if !ok || p.gen != gen || p.stage != stageDirect {
		return // stale timer or already acked
	}
	p.stage = stageIndirect
	n.probes[target] = p
	for _, h := range n.pickHelpers(s, target) {
		s.send(n.id, h, Message{Kind: MsgPingReq, Target: target, Origin: n.id, Updates: n.fullViewUpdates()})
	}
	s.push(event{kind: evIndirectTimeout, tick: s.now + s.Cfg.IndirectTimeout, node: n.id, target: target, gen: gen})
}

// onIndirectTimeout fires when the direct probe and all indirect probes
// have expired without an ack: the target becomes suspect.
func (n *Node) onIndirectTimeout(s *Sim, target int, gen uint64) {
	if !n.running {
		return
	}
	p, ok := n.probes[target]
	if !ok || p.gen != gen {
		return // stale timer or already acked
	}
	delete(n.probes, target)
	cur := n.view[target]
	n.applyUpdate(s, Update{Member: target, State: Suspect, Incarnation: cur.Incarnation},
		fmt.Sprintf("direct and indirect probes to node %d all expired", target))
}

// onSuspectTimeout condemns a member whose suspicion was never refuted by a
// higher incarnation. The (incarnation, gen) token makes the timer a no-op
// once the view has moved on.
func (n *Node) onSuspectTimeout(s *Sim, target int, inc, gen uint64) {
	if !n.running {
		return
	}
	cur, ok := n.view[target]
	if !ok {
		return
	}
	if cur.State != Suspect || cur.Incarnation != inc || n.suspectGen[target] != gen {
		return // stale timer: refuted, escalated or superseded
	}
	n.applyUpdate(s, Update{Member: target, State: Dead, Incarnation: inc},
		fmt.Sprintf("suspect timeout for node %d expired without a higher-incarnation refutation", target))
}

func (n *Node) onMessage(s *Sim, m Message) {
	if !n.running {
		return
	}
	for _, u := range m.Updates {
		n.applyUpdate(s, u, fmt.Sprintf("gossip from node %d", m.From))
	}
	switch m.Kind {
	case MsgPing:
		s.send(n.id, m.From, Message{Kind: MsgAck, Target: n.id, Origin: m.Origin, Updates: n.fullViewUpdates()})
	case MsgPingReq:
		s.send(n.id, m.Target, Message{Kind: MsgPing, Origin: m.Origin, Updates: n.fullViewUpdates()})
	case MsgAck:
		if m.Origin == n.id {
			// An ack proves liveness but never clears suspicion on its
			// own; only a higher incarnation does that.
			delete(n.probes, m.Target)
		} else {
			// We are the ping-req helper: relay the ack to the prober.
			s.send(n.id, m.Origin, Message{Kind: MsgAck, Target: m.Target, Origin: m.Origin, Updates: n.fullViewUpdates()})
		}
	case MsgGossip:
	}
}

// applyUpdate merges one membership fact into the local view.
//
// Merge rules:
//   - higher incarnation always wins;
//   - same incarnation: dead > suspect > alive (escalation only);
//   - lower incarnation is stale and dropped.
//
// A suspect/dead rumor about ourselves at an incarnation at least our own
// is refuted by raising our own incarnation and re-declaring alive. Nobody
// else can ever raise our incarnation for us.
func (n *Node) applyUpdate(s *Sim, u Update, reason string) {
	n.received = append(n.received, modelInput{update: u})
	if u.Member == n.id {
		if u.State == Alive || u.Incarnation < n.incarnation {
			return
		}
		n.incarnation = u.Incarnation + 1
		from := n.view[n.id].State
		n.view[n.id] = MemberMeta{State: Alive, Incarnation: n.incarnation}
		n.recordTransition(s, n.id, from, Alive, n.incarnation,
			fmt.Sprintf("refuting %s rumor about self at incarnation %d", u.State, u.Incarnation))
		s.broadcast(n, Update{Member: n.id, State: Alive, Incarnation: n.incarnation})
		return
	}
	cur := n.view[u.Member]
	if u.Incarnation < cur.Incarnation {
		return
	}
	if u.Incarnation == cur.Incarnation && u.State <= cur.State {
		return
	}
	n.view[u.Member] = MemberMeta{State: u.State, Incarnation: u.Incarnation}
	n.recordTransition(s, u.Member, cur.State, u.State, u.Incarnation, reason)
	switch u.State {
	case Suspect:
		n.timerGen++
		n.suspectGen[u.Member] = n.timerGen
		s.push(event{kind: evSuspectTimeout, tick: s.now + s.Cfg.SuspectTimeout,
			node: n.id, target: u.Member, inc: u.Incarnation, gen: n.timerGen})
	default:
		delete(n.suspectGen, u.Member) // invalidate any pending suspect timer
	}
	s.broadcast(n, u)
}

// crash halts the node. The persistent incarnation is retained; all
// in-flight probe and suspect timers are forgotten (and would be rejected
// by their generation tokens anyway).
func (n *Node) crash() {
	n.running = false
	n.probes = map[int]pendingProbe{}
	n.suspectGen = map[int]uint64{}
}

// restart brings the node back with its persisted incarnation incremented
// and a fresh view; it re-announces itself and relearns the rest via gossip.
func (n *Node) restart(s *Sim) {
	n.running = true
	n.incarnation++
	n.view = make(map[int]MemberMeta, s.Cfg.NumNodes)
	for i := 0; i < s.Cfg.NumNodes; i++ {
		n.view[i] = MemberMeta{State: Alive}
	}
	n.view[n.id] = MemberMeta{State: Alive, Incarnation: n.incarnation}
	n.received = append(n.received, modelInput{restart: true})
	n.recordTransition(s, n.id, Dead, Alive, n.incarnation,
		"restart: rejoining with persisted incarnation+1")
	s.broadcast(n, Update{Member: n.id, State: Alive, Incarnation: n.incarnation})
}

func (n *Node) recordTransition(s *Sim, member int, from, to State, inc uint64, reason string) {
	n.transitions = append(n.transitions, Transition{
		Tick: s.now, Node: n.id, Member: member, From: from, To: to,
		Incarnation: inc, Reason: reason,
	})
}

func (n *Node) pickTarget(s *Sim) (int, bool) {
	var candidates []int
	for id := 0; id < s.Cfg.NumNodes; id++ {
		if id == n.id || n.view[id].State == Dead {
			continue
		}
		candidates = append(candidates, id)
	}
	if len(candidates) == 0 {
		return 0, false
	}
	return candidates[s.rng.Intn(len(candidates))], true
}

func (n *Node) pickHelpers(s *Sim, target int) []int {
	var candidates []int
	for id := 0; id < s.Cfg.NumNodes; id++ {
		if id == n.id || id == target || n.view[id].State == Dead {
			continue
		}
		candidates = append(candidates, id)
	}
	s.rng.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})
	if len(candidates) > s.Cfg.NumIndirect {
		candidates = candidates[:s.Cfg.NumIndirect]
	}
	return candidates
}

func (n *Node) fullViewUpdates() []Update {
	ids := make([]int, 0, len(n.view))
	for id := range n.view {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	ups := make([]Update, 0, len(ids))
	for _, id := range ids {
		m := n.view[id]
		ups = append(ups, Update{Member: id, State: m.State, Incarnation: m.Incarnation})
	}
	return ups
}
