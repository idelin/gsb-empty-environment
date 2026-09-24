package expcache

import "time"

// timer 是过期最小堆中的定时事件节点。
// 它携带 (key, gen, deadline) 三元组：只有当代际 gen 与缓存条目当前代际一致、
// 且 deadline 与条目当前截止时间一致（双向校验）时，弹出才允许删除条目。
type timer struct {
	key      string
	gen      uint64
	deadline time.Time
}

// timerHeap 是按 (deadline, key, gen) 字典序排列的最小堆，
// 确定性的次序让批量到期事件的触发顺序可重现，便于与参考模型对照。
type timerHeap []timer

func (h timerHeap) Len() int { return len(h) }

func (h timerHeap) Less(i, j int) bool {
	if !h[i].deadline.Equal(h[j].deadline) {
		return h[i].deadline.Before(h[j].deadline)
	}
	if h[i].key != h[j].key {
		return h[i].key < h[j].key
	}
	return h[i].gen < h[j].gen
}

func (h timerHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *timerHeap) Push(x any) { *h = append(*h, x.(timer)) }

func (h *timerHeap) Pop() any {
	old := *h
	n := len(old)
	t := old[n-1]
	old[n-1] = timer{} // 释放引用，帮助 GC
	*h = old[:n-1]
	return t
}
