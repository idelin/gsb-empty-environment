package paxos

import (
	"fmt"
	"strings"
)

const (
	// NumNodes is the fixed cluster membership.
	NumNodes = 5
	// Majority is the quorum size for NumNodes nodes.
	Majority = 3
)

// NoOp is the value a new leader uses to fill slots that have no accepted
// value anywhere in its promise quorum.
const NoOp = "\x00noop"

// Node is one simulated Multi-Paxos participant (proposer + acceptor +
// learner). All durable state is written through to Disk before any
// dependent message is emitted; a failed write crashes the node.
type Node struct {
	id   int
	sim  *Sim
	disk *Disk

	up bool

	// Persistent (mirrored on disk).
	maxPromise Ballot
	accepted   map[int]AcceptedEntry
	chosen     map[int]string

	// Volatile.
	applied      []string
	ballot       Ballot                          // ballot this node is currently leading
	highestTried int                             // highest round this node has proposed
	promises     map[int]Message                 // promises collected for ballot
	phase1Done   bool                            // phase 1 completed for ballot
	nextSlot     int                             // next free slot once phase1Done
	clientQueue  []string                        // client values waiting for leadership
	votes        map[int]map[string]map[int]bool // slot -> value -> acceptors
}

func newNode(id int, sim *Sim, disk *Disk) *Node {
	n := &Node{id: id, sim: sim, disk: disk}
	n.loadFromDisk()
	n.resetVolatile()
	n.up = true
	return n
}

func (n *Node) loadFromDisk() {
	ps := n.disk.Load()
	n.maxPromise = ps.MaxPromise
	n.accepted = ps.Accepted
	n.chosen = ps.Chosen
	n.applied = nil
	for {
		v, ok := n.chosen[len(n.applied)+1]
		if !ok {
			break
		}
		n.applied = append(n.applied, v)
	}
}

func (n *Node) resetVolatile() {
	n.ballot = Ballot{}
	n.promises = nil
	n.phase1Done = false
	n.nextSlot = 0
	n.clientQueue = nil
	n.votes = map[int]map[string]map[int]bool{}
}

// persist writes durable state; on failure the node crashes without having
// emitted any message that depends on the failed write.
func (n *Node) persist() bool {
	err := n.disk.Save(PersistentState{
		MaxPromise: n.maxPromise,
		Accepted:   n.accepted,
		Chosen:     n.chosen,
	})
	if err != nil {
		n.sim.logf("tick=%d node=%d persist failed (%v); node crashes", n.sim.tick, n.id, err)
		n.up = false
		n.resetVolatile()
		return false
	}
	return true
}

// ---- proposer ----

// Campaign starts phase 1 with a fresh, strictly higher ballot.
func (n *Node) Campaign() {
	if !n.up {
		return
	}
	round := n.highestTried
	if n.maxPromise.Round > round {
		round = n.maxPromise.Round
	}
	round++
	n.highestTried = round
	n.ballot = Ballot{Round: round, Node: n.id}
	n.promises = map[int]Message{}
	n.phase1Done = false
	n.sim.logf("tick=%d node=%d campaigns with ballot=%s", n.sim.tick, n.id, n.ballot)
	n.broadcast(Message{Kind: MsgPrepare, Ballot: n.ballot})
}

// Propose submits a client value. Before phase 1 completes the value is
// queued; afterwards it is assigned the next slot directly.
func (n *Node) Propose(v string) {
	if !n.up {
		return
	}
	if !n.phase1Done {
		n.clientQueue = append(n.clientQueue, v)
		return
	}
	n.broadcast(Message{Kind: MsgAccept, Ballot: n.ballot, Slot: n.nextSlot, Value: v})
	n.nextSlot++
}

func (n *Node) onPromise(m Message) {
	if n.phase1Done || n.ballot.IsZero() || m.Ballot != n.ballot {
		return
	}
	n.promises[m.From] = m
	if len(n.promises) >= Majority {
		n.finishPhase1()
	}
}

// finishPhase1 adopts, for every slot seen in the quorum, the value of the
// highest accepted ballot; slots with no accepted value are filled with
// NoOp. Only after this point may the leader propose later slots directly.
func (n *Node) finishPhase1() {
	n.phase1Done = true
	merged := map[int]AcceptedEntry{}
	for slot, e := range n.accepted {
		merged[slot] = e
	}
	for _, p := range n.promises {
		for slot, e := range p.Accepted {
			if cur, ok := merged[slot]; !ok || e.Ballot.Compare(cur.Ballot) > 0 {
				merged[slot] = e
			}
		}
	}
	maxSlot := 0
	for slot := range merged {
		if slot > maxSlot {
			maxSlot = slot
		}
	}
	for slot := 1; slot <= maxSlot; slot++ {
		v := NoOp
		if e, ok := merged[slot]; ok {
			v = e.Value
		}
		n.broadcast(Message{Kind: MsgAccept, Ballot: n.ballot, Slot: slot, Value: v})
	}
	n.nextSlot = maxSlot + 1
	for _, v := range n.clientQueue {
		n.broadcast(Message{Kind: MsgAccept, Ballot: n.ballot, Slot: n.nextSlot, Value: v})
		n.nextSlot++
	}
	n.clientQueue = nil
	n.sim.logf("tick=%d node=%d finished phase1 ballot=%s recovered=%s nextSlot=%d",
		n.sim.tick, n.id, n.ballot, formatAccepted(merged), n.nextSlot)
}

// ---- acceptor ----

func (n *Node) onPrepare(m Message) {
	if m.Ballot.Compare(n.maxPromise) <= 0 {
		n.sim.logf("tick=%d node=%d rejects Prepare b=%s (maxPromise=%s)",
			n.sim.tick, n.id, m.Ballot, n.maxPromise)
		return
	}
	n.maxPromise = m.Ballot
	if !n.persist() {
		return
	}
	accepted := make(map[int]AcceptedEntry, len(n.accepted))
	for slot, e := range n.accepted {
		accepted[slot] = e
	}
	n.send(m.From, Message{Kind: MsgPromise, Ballot: m.Ballot, Accepted: accepted})
}

func (n *Node) onAccept(m Message) {
	if m.Ballot.Compare(n.maxPromise) < 0 {
		n.sim.logf("tick=%d node=%d rejects Accept b=%s slot=%d (maxPromise=%s)",
			n.sim.tick, n.id, m.Ballot, m.Slot, n.maxPromise)
		return
	}
	n.maxPromise = m.Ballot
	n.accepted[m.Slot] = AcceptedEntry{Ballot: m.Ballot, Value: m.Value}
	if !n.persist() {
		return
	}
	n.sim.checker.RecordAccept(n.id, m.Slot, m.Ballot, m.Value)
	n.broadcast(Message{Kind: MsgAccepted, Ballot: m.Ballot, Slot: m.Slot, Value: m.Value})
}

// ---- learner ----

func (n *Node) onAccepted(m Message) {
	if _, ok := n.chosen[m.Slot]; ok {
		return
	}
	vs := n.votes[m.Slot]
	if vs == nil {
		vs = map[string]map[int]bool{}
		n.votes[m.Slot] = vs
	}
	acc := vs[m.Value]
	if acc == nil {
		acc = map[int]bool{}
		vs[m.Value] = acc
	}
	acc[m.From] = true
	if len(acc) >= Majority {
		n.learnChosen(m.Slot, m.Value, true)
	}
}

func (n *Node) onChosen(m Message) {
	n.learnChosen(m.Slot, m.Value, false)
}

// learnChosen records a chosen value and advances the applied prefix.
// announce broadcasts a Chosen notification (only for values learned by
// counting Accepted messages, to keep notification traffic bounded).
func (n *Node) learnChosen(slot int, v string, announce bool) {
	if cv, ok := n.chosen[slot]; ok {
		if cv != v {
			n.sim.logf("tick=%d node=%d CONFLICT slot=%d chosen=%s told=%s",
				n.sim.tick, n.id, slot, printable(cv), printable(v))
		}
		return
	}
	n.chosen[slot] = v
	if !n.persist() {
		return
	}
	n.sim.checker.RecordLearn(n.id, slot, v)
	n.sim.logf("tick=%d node=%d learns chosen slot=%d v=%s", n.sim.tick, n.id, slot, printable(v))
	if announce {
		n.broadcast(Message{Kind: MsgChosen, Slot: slot, Value: v})
	}
	n.advanceApplied()
}

// advanceApplied applies only the contiguous chosen prefix.
func (n *Node) advanceApplied() {
	for {
		next := len(n.applied) + 1
		v, ok := n.chosen[next]
		if !ok {
			return
		}
		n.applied = append(n.applied, v)
		n.sim.checker.RecordApplied(n.id, next, v)
		n.sim.logf("tick=%d node=%d applies slot=%d v=%s", n.sim.tick, n.id, next, printable(v))
	}
}

// ---- crash / restart ----

func (n *Node) Crash() {
	if !n.up {
		return
	}
	n.up = false
	n.resetVolatile()
	n.sim.logf("tick=%d node=%d crashes", n.sim.tick, n.id)
}

func (n *Node) Restart() {
	if n.up {
		return
	}
	n.loadFromDisk()
	n.resetVolatile()
	n.up = true
	n.sim.logf("tick=%d node=%d restarts (maxPromise=%s accepted=%s applied=%d slots)",
		n.sim.tick, n.id, n.maxPromise, formatAccepted(n.accepted), len(n.applied))
}

// ---- messaging helpers ----

func (n *Node) send(to int, m Message) {
	m.From = n.id
	m.To = to
	n.sim.send(m)
}

func (n *Node) broadcast(m Message) {
	for i := 0; i < NumNodes; i++ {
		n.send(i, m)
	}
}

// Deliver dispatches one inbound message; lost nodes ignore everything.
func (n *Node) Deliver(m Message) {
	if !n.up {
		return
	}
	switch m.Kind {
	case MsgPrepare:
		n.onPrepare(m)
	case MsgPromise:
		n.onPromise(m)
	case MsgAccept:
		n.onAccept(m)
	case MsgAccepted:
		n.onAccepted(m)
	case MsgChosen:
		n.onChosen(m)
	}
}

// ---- read-only accessors (for tests and reports) ----

func (n *Node) Up() bool           { return n.up }
func (n *Node) MaxPromise() Ballot { return n.maxPromise }

func (n *Node) Applied() []string { return append([]string(nil), n.applied...) }

func (n *Node) Accepted(slot int) (AcceptedEntry, bool) {
	e, ok := n.accepted[slot]
	return e, ok
}

func (n *Node) Chosen(slot int) (string, bool) {
	v, ok := n.chosen[slot]
	return v, ok
}

func (n *Node) State() string {
	var b strings.Builder
	status := "up  "
	if !n.up {
		status = "DOWN"
	}
	fmt.Fprintf(&b, "node %d [%s] maxPromise=%s applied=[%s]\n",
		n.id, status, n.maxPromise, printableSlice(n.applied))
	if len(n.accepted) > 0 {
		fmt.Fprintf(&b, "  accepted: %s\n", formatAccepted(n.accepted))
	}
	if len(n.chosen) > 0 {
		parts := make([]string, 0, len(n.chosen))
		for _, slot := range sortedKeys(n.chosen) {
			parts = append(parts, fmt.Sprintf("%d:%s", slot, printable(n.chosen[slot])))
		}
		fmt.Fprintf(&b, "  chosen:   {%s}\n", strings.Join(parts, " "))
	}
	return b.String()
}
