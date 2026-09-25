package causal

import "errors"

// ErrBackpressure is returned by Receive when the hold-back buffer is full.
// The message is NOT accepted; the caller (e.g. the network layer) must
// retry later. This is the explicit backpressure signal of the system.
var ErrBackpressure = errors.New("causal: delivery buffer full")

// Node is a single member of the fixed membership set. It broadcasts
// messages stamped with vector clocks and delivers incoming messages only
// when all of their causal predecessors have been delivered.
//
// Delivery rule for a message m from sender s (D = delivered clock):
//   m.Clock[s] == D[s]+1            (next expected sequence from s)
//   m.Clock[k] <= D[k] for all k!=s (all causal predecessors delivered)
//
// There is deliberately NO timeout-based delivery path: a message whose
// predecessors are missing stays in the buffer forever.
type Node struct {
	ID          NodeID
	clock       VectorClock // local send clock
	deliveredVC VectorClock // highest contiguous delivered seq per sender
	seen        map[MsgID]struct{}
	pending     map[MsgID]struct{}
	buffer      []Message
	maxBuffer   int
	log         []Message

	// Duplicates counts copies dropped by deduplication.
	Duplicates int
}

// NewNode creates a node for the given fixed membership set.
// maxBuffer bounds the hold-back buffer; 0 means unbounded.
func NewNode(id NodeID, members []NodeID, maxBuffer int) *Node {
	n := &Node{
		ID:          id,
		clock:       make(VectorClock),
		deliveredVC: make(VectorClock),
		seen:        make(map[MsgID]struct{}),
		pending:     make(map[MsgID]struct{}),
		maxBuffer:   maxBuffer,
	}
	for _, m := range members {
		n.clock[m] = 0
		n.deliveredVC[m] = 0
	}
	return n
}

// Broadcast creates a message, advances the local clock and delivers it
// to the local log immediately. The caller disseminates the returned
// message to the other members.
func (n *Node) Broadcast(payload string) Message {
	n.clock[n.ID]++
	msg := Message{
		ID:      MsgID{Sender: n.ID, Seq: n.clock[n.ID]},
		Clock:   n.clock.Clone(),
		Payload: payload,
	}
	n.deliver(msg)
	return msg
}

// Receive ingests a message from the network. Duplicates are dropped
// silently; ready messages are delivered; unready messages are buffered
// unless the buffer is full, in which case ErrBackpressure is returned.
func (n *Node) Receive(m Message) error {
	if _, ok := n.seen[m.ID]; ok {
		n.Duplicates++
		return nil
	}
	if _, ok := n.pending[m.ID]; ok {
		n.Duplicates++
		return nil
	}
	if m.ID.Seq <= n.deliveredVC[m.ID.Sender] {
		n.Duplicates++
		return nil
	}
	if n.ready(m) {
		n.deliver(m)
		return nil
	}
	if n.maxBuffer > 0 && len(n.buffer) >= n.maxBuffer {
		return ErrBackpressure
	}
	n.buffer = append(n.buffer, m)
	n.pending[m.ID] = struct{}{}
	return nil
}

// ready reports whether all causal predecessors of m are delivered.
func (n *Node) ready(m Message) bool {
	if m.Clock[m.ID.Sender] != n.deliveredVC[m.ID.Sender]+1 {
		return false
	}
	for k, v := range m.Clock {
		if k == m.ID.Sender {
			continue
		}
		if n.deliveredVC[k] < v {
			return false
		}
	}
	return true
}

// deliver appends m to the delivery log and drains newly-ready messages.
func (n *Node) deliver(m Message) {
	n.apply(m)
	n.drain()
}

func (n *Node) apply(m Message) {
	n.seen[m.ID] = struct{}{}
	n.deliveredVC[m.ID.Sender] = m.ID.Seq
	n.clock.merge(m.Clock)
	n.log = append(n.log, m)
}

// drain repeatedly delivers ready buffered messages. When several are
// ready, the (Sender, Seq)-smallest is chosen, a deterministic tie-break
// shared by all nodes.
func (n *Node) drain() {
	for {
		best := -1
		for i, m := range n.buffer {
			if !n.ready(m) {
				continue
			}
			if best == -1 || lessStable(m, n.buffer[best]) {
				best = i
			}
		}
		if best == -1 {
			return
		}
		m := n.buffer[best]
		n.buffer = append(n.buffer[:best], n.buffer[best+1:]...)
		delete(n.pending, m.ID)
		n.apply(m)
	}
}

// lessStable is the deterministic tie-break used to order concurrent
// messages: by sender, then sequence number.
func lessStable(a, b Message) bool {
	if a.ID.Sender != b.ID.Sender {
		return a.ID.Sender < b.ID.Sender
	}
	return a.ID.Seq < b.ID.Seq
}

// Log returns a copy of the local delivery log, in delivery order.
func (n *Node) Log() []Message {
	out := make([]Message, len(n.log))
	copy(out, n.log)
	return out
}

// Buffered returns the number of messages waiting for predecessors.
func (n *Node) Buffered() int { return len(n.buffer) }

// Delivered returns the number of messages delivered so far.
func (n *Node) Delivered() int { return len(n.log) }
