// Package barrier implements a deterministic read-barrier coordinator.
//
// A report is only granted a read-only view when every required shard has
// contiguously applied all commits up to the client's target commit. The
// view is pinned at the target commit so a reader never observes a mix of
// one shard's new values and another shard's old values.
package barrier

import "sort"

// Tick is a logical time unit driven by the monotonic test clock.
type Tick int64

// Commit is a commit sequence number within the logical log.
type Commit uint64

// Epoch identifies one incarnation of a shard process. A restart bumps it.
type Epoch uint64

// ShardID identifies a shard.
type ShardID string

// ReqID identifies a client wait request.
type ReqID uint64

// ManualClock is a monotonic test clock. It only moves forward.
type ManualClock struct {
	now Tick
}

// Now returns the current tick.
func (c *ManualClock) Now() Tick { return c.now }

// Set advances the clock to t. It panics on non-monotonic moves so a
// replay can never accidentally travel backwards in time.
func (c *ManualClock) Set(t Tick) {
	if t < c.now {
		panic("ManualClock: non-monotonic set")
	}
	c.now = t
}

// View is the granted read-only snapshot, pinned at the target commit.
type View struct {
	Commit Commit    `json:"commit"`
	Shards []ShardID `json:"shards"`
}

// Lagging describes one shard that blocked a request. Known is false when
// the shard's progress is unknown (no recovery proof for the current epoch,
// or the shard is offline); Gap is only meaningful when Known is true.
type Lagging struct {
	Shard    ShardID `json:"shard"`
	Known    bool    `json:"known"`
	Progress Commit  `json:"progress"`
	Gap      Commit  `json:"gap"`
}

// FailureReason explains why a request finished without a view.
type FailureReason string

const (
	ReasonTimeout   FailureReason = "timeout"
	ReasonOffline   FailureReason = "shard-offline"
	ReasonCancelled FailureReason = "cancelled"
)

// Result is the terminal outcome of a request. Exactly one Result is
// produced per request: either Granted with a pinned View, or a failure
// with the full set of lagging shards (never a partial success).
type Result struct {
	Req     ReqID         `json:"req"`
	Granted bool          `json:"granted"`
	View    View          `json:"view,omitempty"`
	Reason  FailureReason `json:"reason,omitempty"`
	Lagging []Lagging     `json:"lagging,omitempty"`
}

// Stats counts live coordinator state so tests can assert that finished
// requests and drained buffers do not leak resources.
type Stats struct {
	Shards          int `json:"shards"`
	PendingRequests int `json:"pendingRequests"`
	IndexEntries    int `json:"indexEntries"`
	BufferedReports int `json:"bufferedReports"`
}

// normalize sorts and dedups a shard set for deterministic behaviour.
func normalize(shards []ShardID) []ShardID {
	out := make([]ShardID, 0, len(shards))
	seen := make(map[ShardID]struct{}, len(shards))
	for _, s := range shards {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// sortedReqIDs returns request IDs in deterministic order.
func sortedReqIDs(set map[ReqID]struct{}) []ReqID {
	ids := make([]ReqID, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
