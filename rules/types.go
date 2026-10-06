// Package rules implements an event-driven rule system that maintains the
// effective value per object from a stream of events and retractions.
//
// Effective-value selection for one object:
//  1. higher business Priority wins;
//  2. tie-break on higher event Seq (newer wins);
//  3. final deterministic tie-break on smaller EventID.
//
// Retractions target a specific event id only, and may arrive before the
// event they retract (pending retraction). Retracting the current winner
// reveals the next best candidate. Every change of the effective value is
// emitted as a Delta.
package rules

// Event is a candidate value for an object.
type Event struct {
	ObjectID string
	EventID  string
	Priority int
	Seq      int64
	Payload  string
}

// OpKind distinguishes operation types.
type OpKind int

const (
	// OpAdd delivers an event candidate.
	OpAdd OpKind = iota
	// OpRetract retracts a previously delivered (or not yet delivered) event id.
	OpRetract
)

// Op is one input operation. For OpAdd, Event is populated. For OpRetract,
// EventID is populated and targets exactly that event id.
type Op struct {
	Kind    OpKind
	Event   Event
	EventID string
}

// Add builds an OpAdd operation.
func Add(e Event) Op { return Op{Kind: OpAdd, Event: e} }

// Retract builds an OpRetract operation for a specific event id.
func Retract(eventID string) Op { return Op{Kind: OpRetract, EventID: eventID} }

// Delta describes one change of an object's effective value.
type Delta struct {
	ObjectID   string
	OldPayload string
	NewPayload string
	HadOld     bool
	HasNew     bool
}

// better reports whether candidate a beats candidate b in the
// effective-value ordering: priority desc, seq desc, event id asc.
func better(a, b Event) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if a.Seq != b.Seq {
		return a.Seq > b.Seq
	}
	return a.EventID < b.EventID
}
