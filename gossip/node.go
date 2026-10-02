package gossip

import (
	"sort"
	"sync"
)

// Node is a cluster member: it owns a membership view table and a
// disseminator, and exchanges piggybacked events through Ping/Ack packets.
type Node struct {
	ID   string
	Addr string

	mu   sync.Mutex
	inc  uint64
	view map[string]*Member
	dis  *Disseminator
}

func NewNode(id, addr string, dis *Disseminator) *Node {
	n := &Node{
		ID:   id,
		Addr: addr,
		view: make(map[string]*Member),
		dis:  dis,
	}
	n.view[id] = &Member{ID: id, Addr: addr, State: Alive}
	return n
}

// Incarnation returns the node's current incarnation number.
func (n *Node) Incarnation() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.inc
}

// Member looks up one entry of the membership view table.
func (n *Node) Member(id string) (Member, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	m, ok := n.view[id]
	if !ok {
		return Member{}, false
	}
	return *m, true
}

// View returns a sorted snapshot of the membership view table.
func (n *Node) View() []Member {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Member, 0, len(n.view))
	for _, m := range n.view {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Apply merges a remote event into the view table. Fresh events also enter
// the local disseminator so they keep propagating. Returns true if the view
// changed.
func (n *Node) Apply(ev Event) bool {
	if ev.NodeID == n.ID {
		return n.refute(ev)
	}
	n.mu.Lock()
	cur, known := n.view[ev.NodeID]
	if known && !ev.supersedes(Event{NodeID: cur.ID, State: cur.State, Incarnation: cur.Incarnation}) {
		n.mu.Unlock()
		return false
	}
	n.view[ev.NodeID] = &Member{ID: ev.NodeID, Addr: ev.Addr, State: ev.State, Incarnation: ev.Incarnation}
	n.mu.Unlock()
	n.dis.Queue(ev)
	return true
}

// refute handles events about the node itself: a suspect/dead rumor is
// refuted by bumping the incarnation and gossiping an alive notice.
func (n *Node) refute(ev Event) bool {
	if ev.State == Alive {
		return false
	}
	n.mu.Lock()
	if ev.Incarnation < n.inc {
		n.mu.Unlock()
		return false
	}
	n.inc = ev.Incarnation + 1
	self := n.view[n.ID]
	self.Incarnation = n.inc
	self.State = Alive
	inc := n.inc
	n.mu.Unlock()
	n.dis.Queue(Event{NodeID: n.ID, Addr: n.Addr, State: Alive, Incarnation: inc})
	return true
}

// GossipEvents picks events from the disseminator for the next packet header.
func (n *Node) GossipEvents(limit int) []Event {
	return n.dis.Piggyback(limit)
}

// ReceivePing handles an incoming Ping carrying piggybacked events and
// returns the events to attach to the Ack header.
func (n *Node) ReceivePing(events []Event, ackLimit int) []Event {
	for _, ev := range events {
		n.Apply(ev)
	}
	return n.dis.Piggyback(ackLimit)
}

// ReceiveAck merges the events piggybacked on an Ack.
func (n *Node) ReceiveAck(events []Event) {
	for _, ev := range events {
		n.Apply(ev)
	}
}
