package matrixclock

import "fmt"

// Event is a single entry in a node's replicated message log.
type Event struct {
	LSN    uint64 // node-local log sequence number, starts at 1
	Origin string // node that created the event
	Seq    uint64 // origin node's clock value when the event was created
	Kind   string // "local", "send" or "recv"
	Data   string
}

func (e Event) String() string {
	return fmt.Sprintf("LSN=%d %s@%d %s %q", e.LSN, e.Origin, e.Seq, e.Kind, e.Data)
}

// Message carries an event plus the sender's full matrix snapshot.
type Message struct {
	From   string
	To     string
	Matrix MatrixClock
	Event  Event
}

// Node is one participant of the cluster: a matrix clock plus a
// physical message log that can be truncated by the GC watermark.
type Node struct {
	ID      string
	clock   MatrixClock
	log     []Event
	nextLSN uint64
	baseLSN uint64 // LSN of log[0]; everything below is physically gone
}

// NewNode creates a node that knows the cluster membership upfront.
func NewNode(id string, peers []string) *Node {
	n := &Node{ID: id, clock: make(MatrixClock), nextLSN: 1, baseLSN: 1}
	n.clock.ensure(id)
	for _, p := range peers {
		n.clock.ensure(p)
	}
	return n
}

func (n *Node) append(origin string, seq uint64, kind, data string) Event {
	ev := Event{LSN: n.nextLSN, Origin: origin, Seq: seq, Kind: kind, Data: data}
	n.nextLSN++
	n.log = append(n.log, ev)
	return ev
}

// LocalEvent records a purely local event.
func (n *Node) LocalEvent(data string) Event {
	n.clock.Increment(n.ID)
	return n.append(n.ID, n.clock[n.ID][n.ID], "local", data)
}

// Send records a send event and returns a message carrying the event
// and a snapshot of the full matrix clock.
func (n *Node) Send(to, data string) Message {
	n.clock.Increment(n.ID)
	ev := n.append(n.ID, n.clock[n.ID][n.ID], "send", data)
	return Message{From: n.ID, To: to, Matrix: n.clock.Snapshot(), Event: ev}
}

// Receive merges the sender's matrix and logs the delivered event.
func (n *Node) Receive(msg Message) Event {
	n.clock.Merge(n.ID, msg.From, msg.Matrix)
	return n.append(msg.Event.Origin, msg.Event.Seq, "recv", msg.Event.Data)
}

// Matrix returns a snapshot of the node's matrix clock.
func (n *Node) Matrix() MatrixClock { return n.clock.Snapshot() }

// Log returns a copy of the live (not yet truncated) log entries.
func (n *Node) Log() []Event {
	out := make([]Event, len(n.log))
	copy(out, n.log)
	return out
}

// BaseLSN is the LSN of the first live log entry.
func (n *Node) BaseLSN() uint64 { return n.baseLSN }

// GCWatermark computes, per origin node, the timestamp that every node
// in the cluster has acknowledged: the per-column matrix minimum.
func (n *Node) GCWatermark() Vector { return n.clock.Acknowledged() }

// SafeLSN returns the highest LSN such that every log entry at or below
// it is acknowledged by every node. Returns baseLSN-1 when nothing is
// safe to collect yet.
func (n *Node) SafeLSN() uint64 {
	ack := n.GCWatermark()
	safe := n.baseLSN - 1
	for _, ev := range n.log {
		if ev.Seq > ack[ev.Origin] {
			break
		}
		safe = ev.LSN
	}
	return safe
}

// Truncate physically drops every log entry whose LSN is at or below
// the GC-safe watermark and reports the LSN up to which the log was
// truncated (baseLSN-1 if nothing was collected).
func (n *Node) Truncate() uint64 {
	safe := n.SafeLSN()
	idx := int(safe-n.baseLSN) + 1
	if idx < 0 {
		idx = 0
	}
	if idx > len(n.log) {
		idx = len(n.log)
	}
	n.log = n.log[idx:]
	n.baseLSN = safe + 1
	return safe
}
