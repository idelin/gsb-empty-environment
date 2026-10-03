package sim

// OpKind identifies the kind of operation an event carries.
type OpKind uint8

const (
	OpAddSparse OpKind = iota
	OpAddDense
	OpRemove
	OpRemoveRange
	OpAnd
	OpOr
	OpXor
	OpCheckpoint
)

func (k OpKind) String() string {
	switch k {
	case OpAddSparse:
		return "add-sparse"
	case OpAddDense:
		return "add-dense"
	case OpRemove:
		return "remove"
	case OpRemoveRange:
		return "remove-range"
	case OpAnd:
		return "and"
	case OpOr:
		return "or"
	case OpXor:
		return "xor"
	case OpCheckpoint:
		return "checkpoint"
	}
	return "unknown"
}

// Event is a discrete-time simulation event. Events are processed in
// (Time, Seq) order, which makes the schedule fully deterministic.
type Event struct {
	Time   uint64
	Seq    uint64
	Kind   OpKind
	Target int
}

// eventHeap is a priority queue ordered by (Time, Seq).
type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	if h[i].Time != h[j].Time {
		return h[i].Time < h[j].Time
	}
	return h[i].Seq < h[j].Seq
}

func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(Event)) }

func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = Event{}
	*h = old[:n-1]
	return e
}
