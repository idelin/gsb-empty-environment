// Package agg implements a barrier-based generational aggregator.
//
// The aggregator issues a reset (reset barrier) to every producer
// partition. Events a partition emits before it crosses the barrier are
// counted toward the old generation; events after the crossing count
// toward the new generation. Partitions reach the barrier out of order
// and each arrival is cached; only when every live partition has arrived
// is the old generation's final value published.
//
// Every decision is recorded as a tagged event in an append-only log.
// State is a pure fold over that log, which gives us recovery (replay)
// and an independent verifier that re-checks the protocol from the tags.
package agg

// Tag classifies an event for the verification model.
type Tag string

const (
	TagControl    Tag = "control"    // reset lifecycle
	TagData       Tag = "data"       // partition data, accepted or dropped
	TagBarrier    Tag = "barrier"    // barrier arrivals (and ignored ones)
	TagMembership Tag = "membership" // joins and exits
	TagPublish    Tag = "publish"    // finalized generations
)

// Kind is the concrete event type.
type Kind string

const (
	KindResetIssued     Kind = "ResetIssued"
	KindResetIgnored    Kind = "ResetIgnored"
	KindDataAccepted    Kind = "DataAccepted"
	KindDataDropped     Kind = "DataDropped"
	KindBarrierCrossed  Kind = "BarrierCrossed"
	KindBarrierIgnored  Kind = "BarrierIgnored"
	KindPartitionJoined Kind = "PartitionJoined"
	KindPartitionExited Kind = "PartitionExited"
	KindGenFinalized    Kind = "GenerationFinalized"
	KindGenRolled       Kind = "GenerationRolled"
)

// Event is one immutable entry in the log. Fields not relevant to a
// kind are left at their zero value.
type Event struct {
	Seq       uint64   // position in the log
	Tag       Tag      // verification category
	Kind      Kind     // concrete type
	Partition string   // subject partition, if any
	Gen       uint64   // generation the event refers to
	Value     int64    // data value / finalized value / carried value
	Note      string   // reason string for ignored/dropped events
	Members   []string // ResetIssued: expected partition set
}
