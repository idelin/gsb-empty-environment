package repartition

// recordHeap 按持久化序号输出新拓扑记录，保证切换瞬间保持摄入顺序。
type recordHeap []Record

func (h recordHeap) Len() int { return len(h) }

func (h recordHeap) Less(i, j int) bool { return h[i].seq < h[j].seq }

func (h recordHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Push 实现 heap.Interface。
func (h *recordHeap) Push(x any) { *h = append(*h, x.(Record)) }

// Pop 实现 heap.Interface。
func (h *recordHeap) Pop() any {
	old := *h
	n := len(old)
	r := old[n-1]
	*h = old[:n-1]
	return r
}
