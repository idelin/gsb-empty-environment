package steady

import "time"

// simKind enumerates discrete-event simulation events.
type simKind int

const (
	evArrive   simKind = iota // ingress request arrives at an entry node
	evComplete                // a physical attempt finishes
	evBackoff                 // a backoff timer elapses
	evHedge                   // a hedge timer elapses
	evEnd                     // simulation horizon reached
)

// simEvent is one scheduled event of the discrete-event simulation.
type simEvent struct {
	at      int64 // nanoseconds since virtual epoch
	seq     int64 // tie-breaker for stable, deterministic ordering
	kind    simKind
	node    int
	req     *simRequest
	attempt *simAttempt
}

// eventHeap is a min-heap keyed by (at, seq).
type eventHeap []*simEvent

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(*simEvent)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// simAttempt is one physical attempt of a request on one edge.
type simAttempt struct {
	edge     int
	attempt  int
	hedge    bool
	startAt  int64
	deadline int64
	// locallyDone is set once the attempt's own processing completed; a
	// later downstream rollback must not release its slots a second time.
	locallyDone bool
}

// simRequest is one external request travelling through the DAG.
type simRequest struct {
	id       uint64
	arrived  int64
	deadline int64
	// pathCursor is the node currently processing the request.
	cursor int
	// per-edge call state keyed by edge index.
	calls map[int]*simCall
}

// simCall tracks the retry state machine of one request on one edge.
type simCall struct {
	edge         int
	parentEdge   int // edge index of the enclosing call, -1 for top level
	childEdge    int // edge index of the nested downstream call, -1 if none
	childCall    *simCall
	attemptsUsed int
	hedgesFired  int
	finished     bool
	succeeded    bool
	// generations tracks how often this logical call (re)started its
	// downstream chain; rollback retries reuse the same call object instead
	// of creating a new logical call, matching the analytical edge call rate.
	generations int
	pending     map[int]*simAttempt // in-flight attempts keyed by attempt no
	hedgeArmed  bool
}

// SimConfig configures a discrete-event simulation run.
type SimConfig struct {
	Duration time.Duration
	Warmup   time.Duration
	Seed     int64
	// ServiceScale converts MeanServiceTime into the exponential service
	// distribution mean; 1.0 uses it directly.
	ServiceScale float64
}

// SimNodeResult is the measured per-node outcome.
type SimNodeResult struct {
	Name            string
	NormalArrival   float64
	PhysicalArrival float64
	FailureRate     float64
	Amplification   float64
	LoadToCapacity  float64
}

// SimEdgeResult is the measured per-edge outcome.
type SimEdgeResult struct {
	From           string
	To             string
	Calls          float64 // outermost logical calls/s
	AttemptRate    float64 // total physical attempts/s placed on the edge
	RetriesPerCall float64
	HedgesPerCall  float64
	ExtraPerCall   float64
}

// SimResult is the full measurement window output.
type SimResult struct {
	Nodes []SimNodeResult
	Edges []SimEdgeResult
	// Counters accumulated over the measurement window (post-warmup).
	nodeNormal   []int64
	nodePhysical []int64
	nodeFailure  []int64
	edgeCalls    []int64
	edgeRetries  []int64
	edgeHedges   []int64
	edgeAttempts []int64
}
