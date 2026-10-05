// Package barrier implements a deterministic read-barrier coordinator.
//
// Shards report contiguous applied commit numbers; a high commit with a
// hole behind it never counts as progress. A client submits a target
// commit, the set of shards it needs, and a deadline tick. The
// coordinator hands out a read-only view pinned at the target commit
// only once every required shard has contiguously applied up to the
// target. On timeout it reports every lagging shard and its gap and
// never returns a pseudo-success partial result.
package barrier

// ShardID identifies a shard.
type ShardID string

// ReqID identifies a submitted barrier request.
type ReqID uint64

// Tick is a logical time unit from the monotonic test clock.
type Tick uint64

// Epoch identifies a shard incarnation. Reports carrying an older epoch
// than the current one can never advance the new instance.
type Epoch uint64

// Report is a shard progress report. Applied lists commit numbers the
// shard has applied; they may arrive out of order and may contain
// duplicates (reports are idempotent). Proof carries a recovery proof:
// ProofContig is the durable contiguous commit the shard guarantees
// after a restart.
type Report struct {
	Shard       ShardID
	Epoch       Epoch
	Applied     []uint64
	Proof       bool
	ProofContig uint64
}

// Request is a client read-barrier request.
type Request struct {
	ID       ReqID
	Target   uint64
	Shards   []ShardID
	Deadline Tick
}

// OutcomeKind classifies a finished request.
type OutcomeKind int

const (
	// Granted means every required shard reached the target contiguously
	// before the deadline; View is set.
	Granted OutcomeKind = iota
	// TimedOut means the deadline tick was reached without full
	// progress; Lagging lists every shard that blocked the barrier.
	TimedOut
)

// View is a read-only view pinned at the target commit.
type View struct {
	Target uint64
	Shards []ShardID
}

// ShardGap describes one lagging shard in a timeout outcome.
type ShardGap struct {
	Shard   ShardID
	Contig  uint64 // contiguous applied commit known for the shard
	Missing uint64 // Target - Contig (0 when Unknown)
	Unknown bool   // no recovery proof yet (or shard never reported)
	Offline bool
}

// Outcome is the terminal result of a request.
type Outcome struct {
	Req     ReqID
	Kind    OutcomeKind
	View    *View      // set when Kind == Granted
	Lagging []ShardGap // set when Kind == TimedOut
}
