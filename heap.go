package desim

// eventHeap is a priority queue ordered by (Tick, Cat, Seq).
type eventHeap []*Event

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.Tick != b.Tick {
		return a.Tick < b.Tick
	}
	if a.Cat != b.Cat {
		return a.Cat < b.Cat
	}
	return a.Seq < b.Seq
}

func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(*Event)) }

func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}
