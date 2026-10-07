// Package hb builds a happens-before partial order from distributed events.
//
// Events carry a local sequence number per node plus send/receive links.
// Wall-clock timestamps are kept for display only and never influence the
// causal order. Duplicate imports of the same event merge when identical;
// contradictory duplicates or links are reported with cycle evidence.
package hb

import "fmt"

// Kind classifies an event.
type Kind int

const (
	// Local is an internal step of a node.
	Local Kind = iota
	// Send emits a message.
	Send
	// Recv consumes a message; SendID must reference a Send event.
	Recv
)

func (k Kind) String() string {
	switch k {
	case Local:
		return "local"
	case Send:
		return "send"
	case Recv:
		return "recv"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Event is a single observed step on a node.
//
// ID uniquely identifies the event across the whole trace. Node and Seq
// give its position in the node's local order. For Recv events, SendID
// links to the Send event of the same message. Wall is a wall-clock
// timestamp used only for display; it never affects causality.
type Event struct {
	ID     string
	Node   string
	Seq    int
	Kind   Kind
	SendID string // Recv only: ID of the matching Send event
	Wall   int64  // display only, never used for ordering
	Data   string // opaque payload label
}

// canonical returns the identity-relevant fields for duplicate comparison.
func (e Event) canonical() Event {
	return e
}

func (e Event) equal(o Event) bool {
	return e.canonical() == o.canonical()
}

func (e Event) String() string {
	return fmt.Sprintf("%s(%s#%d %s)", e.ID, e.Node, e.Seq, e.Kind)
}
