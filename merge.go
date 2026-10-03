package lsm

import "container/heap"

// mergeItem 是归并堆中的一个元素：某输入文件的当前记录。
type mergeItem struct {
	e   Entry
	src int // 来源文件下标
}

// mergeHeap 按 (Key 升序, Seq 降序) 排序，
// 保证同一个 Key 弹出的第一条就是最新版本。
type mergeHeap []mergeItem

func (h mergeHeap) Len() int { return len(h) }

func (h mergeHeap) Less(i, j int) bool {
	a, b := h[i].e, h[j].e
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	if a.Seq != b.Seq {
		return a.Seq > b.Seq
	}
	return h[i].src < h[j].src
}

func (h mergeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x interface{}) { *h = append(*h, x.(mergeItem)) }
func (h *mergeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// kwayMerge 对多个有序 SSTable 做多路归并：
// 输出全局按 Key 有序，且每个 Key 只保留 Seq 最大（最新）的一条记录。
func kwayMerge(tables []*SSTable) ([]Entry, error) {
	iters := make([][]Entry, len(tables))
	pos := make([]int, len(tables))
	h := &mergeHeap{}
	heap.Init(h)
	for i, t := range tables {
		entries, err := readSSTable(t.Path)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			continue
		}
		iters[i] = entries
		heap.Push(h, mergeItem{e: entries[0], src: i})
	}

	var out []Entry
	var lastKey string
	haveLast := false
	for h.Len() > 0 {
		item := heap.Pop(h).(mergeItem)
		pos[item.src]++
		if pos[item.src] < len(iters[item.src]) {
			heap.Push(h, mergeItem{e: iters[item.src][pos[item.src]], src: item.src})
		}
		if haveLast && item.e.Key == lastKey {
			continue // 同一 Key 的旧版本，丢弃
		}
		out = append(out, item.e)
		lastKey = item.e.Key
		haveLast = true
	}
	return out, nil
}

// dropTombstones 在压实到最底层时清理删除标记，物理移除废弃 Key。
func dropTombstones(entries []Entry) []Entry {
	out := entries[:0]
	for _, e := range entries {
		if e.Op != OpDelete {
			out = append(out, e)
		}
	}
	return out
}
