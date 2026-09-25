package causal

import (
	"errors"
	"fmt"
)

var (
	// ErrBackpressure is returned when the pending buffer is full and the
	// incoming message is not yet deliverable. The message is NOT accepted;
	// the caller (transport/simulator) must retry later. Nothing is dropped
	// silently and nothing is force-delivered.
	ErrBackpressure = errors.New("causal: pending buffer full, backpressure")

	// ErrUnknownMember is returned for messages from outside the fixed
	// membership set, or with a mismatched vector clock length.
	ErrUnknownMember = errors.New("causal: sender not in fixed membership set")
)

// Node is one member of a fixed membership set. It delivers messages only
// when all causal predecessors have been delivered. There is deliberately
// NO timeout path: a message with a missing predecessor waits forever.
type Node struct {
	ID        int
	clock     VectorClock
	delivered map[MsgID]bool
	pending   []Message
	cap       int

	// Counters, exported as delivery evidence.
	DeliveredCount int
	DuplicateCount int
}

func NewNode(id, members, bufferCap int) *Node {
	return &Node{
		ID:        id,
		clock:     NewVectorClock(members),
		delivered: make(map[MsgID]bool),
		cap:       bufferCap,
	}
}

// Send stamps a new message with the local clock (incremented at self
// index). Sending implies local delivery: the message is applied to the
// local clock and recorded as delivered.
func (n *Node) Send(payload string) Message {
	n.clock[n.ID]++
	m := Message{
		ID:      MsgID{Sender: n.ID, Seq: n.clock[n.ID]},
		From:    n.ID,
		VC:      n.clock.Copy(),
		Payload: payload,
	}
	n.delivered[m.ID] = true
	n.DeliveredCount++
	return m
}

// ready reports the causal delivery condition for m:
//   - m is the next expected sequence from its sender, and
//   - every message m causally depends on is already delivered locally.
func (n *Node) ready(m Message) bool {
	if m.VC[m.From] != n.clock[m.From]+1 {
		return false
	}
	for k := range n.clock {
		if k != m.From && n.clock[k] < m.VC[k] {
			return false
		}
	}
	return true
}

// Receive ingests one message. It returns the messages newly delivered to
// the application (m itself plus any buffered messages it unblocks), in
// delivery order. Duplicates are dropped. If m is not deliverable and the
// buffer is full, ErrBackpressure is returned and m is rejected.
func (n *Node) Receive(m Message) ([]Message, error) {
	if m.From < 0 || m.From >= len(n.clock) || len(m.VC) != len(n.clock) {
		return nil, ErrUnknownMember
	}
	// Dedup: per-sender sequence already covered, or exact ID seen.
	if m.VC[m.From] <= n.clock[m.From] || n.delivered[m.ID] {
		n.DuplicateCount++
		return nil, nil
	}
	if !n.ready(m) {
		if len(n.pending) >= n.cap {
			return nil, ErrBackpressure
		}
		n.pending = append(n.pending, m)
		return nil, nil
	}
	var out []Message
	n.deliver(m)
	out = append(out, m)
	// Drain: among ready buffered messages pick the smallest (From, Seq)
	// so the drain order is deterministic.
	for {
		best := -1
		for i, p := range n.pending {
			if !n.ready(p) {
				continue
			}
			if best == -1 || lessTieBreak(p, n.pending[best]) {
				best = i
			}
		}
		if best == -1 {
			break
		}
		m := n.pending[best]
		n.pending = append(n.pending[:best], n.pending[best+1:]...)
		n.deliver(m)
		out = append(out, m)
	}
	return out, nil
}

func lessTieBreak(a, b Message) bool {
	if a.From != b.From {
		return a.From < b.From
	}
	return a.ID.Seq < b.ID.Seq
}

func (n *Node) deliver(m Message) {
	for k := range n.clock {
		if m.VC[k] > n.clock[k] {
			n.clock[k] = m.VC[k]
		}
	}
	n.delivered[m.ID] = true
	n.DeliveredCount++
}

// Clock returns a copy of the node's current vector clock.
func (n *Node) Clock() VectorClock { return n.clock.Copy() }

// Pending returns a copy of the buffered (not yet deliverable) messages.
func (n *Node) Pending() []Message {
	out := make([]Message, len(n.pending))
	copy(out, n.pending)
	return out
}

// PendingReport explains, for each buffered message, exactly which causal
// predecessors are still missing. Used as evidence that unmet dependencies
// are never "timed out" into being satisfied.
func (n *Node) PendingReport() []string {
	var out []string
	for _, p := range n.pending {
		var missing []string
		if p.VC[p.From] != n.clock[p.From]+1 {
			missing = append(missing,
				fmt.Sprintf("need n%d#%d (have seq %d)", p.From, n.clock[p.From]+1, n.clock[p.From]))
		}
		for k := range n.clock {
			if k != p.From && n.clock[k] < p.VC[k] {
				missing = append(missing,
					fmt.Sprintf("need n%d up to seq %d (have %d)", k, p.VC[k], n.clock[k]))
			}
		}
		out = append(out, fmt.Sprintf("%s waits on %v", p.ID, missing))
	}
	return out
}
