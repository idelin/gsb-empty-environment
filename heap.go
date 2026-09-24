package ttlcache

// timerNode 是过期最小堆中的一个定时事件。
// gen 为全局单调递增的代际号，与缓存条目中的 gen 做双向校验：
// 只有堆节点与条目双方的 gen 和 expireAt 都一致时，删除才生效。
type timerNode struct {
	key      string
	gen      uint64
	expireAt int64 // UnixNano，半开语义：now >= expireAt 即过期
}

// timerHeap 实现 container/heap.Interface，按 (expireAt, gen) 排序，
// gen 作为同刻事件的 FIFO 次序保证。
type timerHeap []timerNode

func (h timerHeap) Len() int { return len(h) }

func (h timerHeap) Less(i, j int) bool {
	if h[i].expireAt != h[j].expireAt {
		return h[i].expireAt < h[j].expireAt
	}
	return h[i].gen < h[j].gen
}

func (h timerHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *timerHeap) Push(x any) { *h = append(*h, x.(timerNode)) }

func (h *timerHeap) Pop() any {
	old := *h
	n := len(old)
	node := old[n-1]
	old[n-1] = timerNode{} // 释放引用，便于 GC
	*h = old[:n-1]
	return node
}
