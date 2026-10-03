package delta

import "sync"

// Ack acknowledges a received delta: it carries the receiver's causal
// context after merging, which the sender records as that peer's
// confirmed watermark.
type Ack struct {
	Ctx VersionVector
}

// Node is a replica that tracks, per peer, the acknowledged watermark
// used to compute minimal outbound deltas.
type Node struct {
	mu   sync.Mutex
	doc  *Document
	acks map[ReplicaID]VersionVector
}

// NewNode creates a node with the given replica ID.
func NewNode(id ReplicaID) *Node {
	return &Node{
		doc:  NewDocument(id),
		acks: make(map[ReplicaID]VersionVector),
	}
}

// ID returns the node's replica ID.
func (n *Node) ID() ReplicaID { return n.doc.Replica() }

// Doc returns the underlying document for local edits and reads.
// Callers must not mutate it concurrently with sync operations.
func (n *Node) Doc() *Document { return n.doc }

// DeltaFor computes the delta to send to peer: every operation not yet
// acknowledged by that peer. An unknown peer (zero watermark) receives
// the full state.
func (n *Node) DeltaFor(peer ReplicaID) Delta {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.doc.DeltaSince(n.acks[peer])
}

// HandleDelta merges an inbound delta and returns the ack to send back.
func (n *Node) HandleDelta(from ReplicaID, delta Delta) Ack {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.doc.MergeDelta(delta)
	return Ack{Ctx: n.doc.Context()}
}

// HandleAck records the peer's confirmed watermark, shrinking all
// future deltas addressed to that peer.
func (n *Node) HandleAck(from ReplicaID, ack Ack) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.acks[from] = n.acks[from].Join(ack.Ctx)
}

// Watermark returns the confirmed watermark recorded for peer.
func (n *Node) Watermark(peer ReplicaID) VersionVector {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.acks[peer].Clone()
}

// SyncPair performs one in-process sync round between two nodes: both
// exchange deltas computed from their per-peer watermarks and record
// each other's acks.
func SyncPair(a, b *Node) {
	da := a.DeltaFor(b.ID())
	db := b.DeltaFor(a.ID())
	ackB := b.HandleDelta(a.ID(), da)
	ackA := a.HandleDelta(b.ID(), db)
	a.HandleAck(b.ID(), ackB)
	b.HandleAck(a.ID(), ackA)
}
