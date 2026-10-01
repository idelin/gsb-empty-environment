package gossip

import "sync"

// PiggybackCapacity is how many events fit in one Ping/Ack packet header.
const PiggybackCapacity = 3

// Packet is a Ping or Ack network message; membership events ride along
// in its header without costing extra packets.
type Packet struct {
	From   string
	Events []Event // piggybacked membership changes
}

// Node is a cluster member: it keeps a membership view table, a
// disseminator queue, and speaks Ping/Ack.
type Node struct {
	ID          string
	incarnation uint64

	mu      sync.Mutex
	members map[string]Member
	diss    *Disseminator
}

func NewNode(id string, clusterSize int) *Node {
	n := &Node{
		ID:      id,
		members: make(map[string]Member),
		diss:    NewDisseminator(clusterSize),
	}
	n.members[id] = Member{ID: id, State: Alive}
	return n
}

// Join announces this node to an introducer: the introducer records the
// new member and queues a JOIN event for piggyback dissemination.
func (n *Node) Join(introducer *Node) {
	introducer.mu.Lock()
	introducer.members[n.ID] = Member{ID: n.ID, State: Alive}
	introducer.mu.Unlock()
	introducer.diss.Queue(Event{Type: Join, Node: n.ID})
}

// Leave voluntarily departs: the node marks itself left and gossips it.
func (n *Node) Leave() {
	n.mu.Lock()
	m := n.members[n.ID]
	m.State = Left
	n.members[n.ID] = m
	n.mu.Unlock()
	n.diss.Queue(Event{Type: Leave, Node: n.ID, Incarnation: m.Incarnation})
}

// SuspectNode marks another node suspected and gossips the suspicion.
func (n *Node) SuspectNode(target string) {
	n.mu.Lock()
	m, ok := n.members[target]
	if !ok {
		n.mu.Unlock()
		return
	}
	m.State = Suspected
	n.members[target] = m
	n.mu.Unlock()
	n.diss.Queue(Event{Type: Suspect, Node: target, Incarnation: m.Incarnation})
}

// Ping sends a Ping packet with piggybacked events to target and returns
// the Ack packet (which also carries piggybacked events back).
func (n *Node) Ping(target *Node) Packet {
	ping := Packet{From: n.ID, Events: n.diss.Piggyback(PiggybackCapacity)}
	ack := target.onPing(ping)
	n.applyEvents(ack.Events)
	return ack
}

// onPing handles an incoming Ping: apply its events, reply with an Ack
// carrying this node's own pending events.
func (n *Node) onPing(ping Packet) Packet {
	n.applyEvents(ping.Events)
	return Packet{From: n.ID, Events: n.diss.Piggyback(PiggybackCapacity)}
}

// applyEvents folds received events into the membership view and
// re-queues novel ones so the gossip keeps spreading (infection model).
func (n *Node) applyEvents(events []Event) {
	for _, e := range events {
		if n.applyEvent(e) {
			n.diss.Queue(e)
		}
	}
}

// applyEvent updates the view table; it reports whether the event taught
// us something new (and should therefore be re-disseminated).
func (n *Node) applyEvent(e Event) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	cur, known := n.members[e.Node]
	if known && e.Incarnation < cur.Incarnation {
		return false // stale news
	}
	switch e.Type {
	case Join:
		if known && cur.State == Alive && cur.Incarnation >= e.Incarnation {
			return false
		}
		n.members[e.Node] = Member{ID: e.Node, State: Alive, Incarnation: e.Incarnation}
	case Suspect:
		if !known || cur.State == Left {
			return false
		}
		n.members[e.Node] = Member{ID: e.Node, State: Suspected, Incarnation: e.Incarnation}
	case Leave:
		if !known {
			return false
		}
		n.members[e.Node] = Member{ID: e.Node, State: Left, Incarnation: e.Incarnation}
	}
	return true
}

// MemberView returns a copy of the node's membership view table.
func (n *Node) MemberView() map[string]Member {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make(map[string]Member, len(n.members))
	for id, m := range n.members {
		out[id] = m
	}
	return out
}

// Knows reports whether the node has id in its view table.
func (n *Node) Knows(id string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.members[id]
	return ok
}

// PendingEvents reports how many events await dissemination.
func (n *Node) PendingEvents() int { return n.diss.Pending() }
