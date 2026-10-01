package sim

// EventType identifies the kind of an event. The numeric order defines the
// processing order of events sharing the same tick: transmissions complete
// first, then weight changes, then withdrawals, then cancellations, and
// arrivals last. Ties beyond that are broken by submission sequence number,
// so the same script always replays identically.
type EventType int

const (
	EvTxComplete EventType = iota
	EvFlowWeight
	EvTenantWeight
	EvWithdraw
	EvCancel
	EvArrive
)

// Event is a scheduled occurrence at an integer tick. Payload fields used
// depend on Type.
type Event struct {
	Tick int
	Type EventType
	Seq  int

	Packet   *Packet // EvArrive, EvTxComplete
	PacketID int     // EvCancel
	FlowID   int     // EvFlowWeight, EvWithdraw
	TenantID int     // EvTenantWeight
	Weight   float64 // EvFlowWeight, EvTenantWeight
}

// eventHeap is a priority queue ordered by (Tick, Type, Seq).
type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.Tick != b.Tick {
		return a.Tick < b.Tick
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.Seq < b.Seq
}

func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(Event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	ev := old[n-1]
	*h = old[:n-1]
	return ev
}
