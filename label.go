package paretopath

type Label struct {
	At      int
	Time    int64
	Cost    int64
	Visited uint16
	Edges   []Edge
}

func compareEdgeSeq(a, b []Edge) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i].From != b[i].From {
			if a[i].From < b[i].From {
				return -1
			}
			return 1
		}
		if a[i].To != b[i].To {
			if a[i].To < b[i].To {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func dominates(a, b *Label) bool {
	if a.Visited&^b.Visited != 0 {
		return false
	}
	if a.Time > b.Time || a.Cost > b.Cost {
		return false
	}
	if a.Time < b.Time || a.Cost < b.Cost {
		return true
	}
	return compareEdgeSeq(a.Edges, b.Edges) <= 0
}

type labelHeap []Label

func (h labelHeap) Len() int { return len(h) }

func (h labelHeap) Less(i, j int) bool {
	if h[i].Time != h[j].Time {
		return h[i].Time < h[j].Time
	}
	if h[i].Cost != h[j].Cost {
		return h[i].Cost < h[j].Cost
	}
	return compareEdgeSeq(h[i].Edges, h[j].Edges) < 0
}

func (h labelHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *labelHeap) Push(x any) {
	*h = append(*h, x.(Label))
}

func (h *labelHeap) Pop() any {
	old := *h
	n := len(old)
	l := old[n-1]
	*h = old[:n-1]
	return l
}
