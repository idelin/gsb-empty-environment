package lsm

import "container/heap"

// entryIterator 是 Memtable 快照与 SSTable 的统一迭代抽象。
type entryIterator interface {
	Next() bool
	Entry() Entry
	Close() error
}

type sliceIterator struct {
	entries []Entry
	idx     int
}

func newSliceIterator(entries []Entry) *sliceIterator {
	return &sliceIterator{entries: entries, idx: -1}
}

func (it *sliceIterator) Next() bool {
	it.idx++
	return it.idx < len(it.entries)
}

func (it *sliceIterator) Entry() Entry { return it.entries[it.idx] }

func (it *sliceIterator) Close() error { return nil }

type heapItem struct {
	e   Entry
	src int // 数据源编号,越小越新
}

type mergeHeap []heapItem

func (h mergeHeap) Len() int { return len(h) }

func (h mergeHeap) Less(i, j int) bool {
	if h[i].e.Key != h[j].e.Key {
		return h[i].e.Key < h[j].e.Key
	}
	return h[i].src < h[j].src
}

func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *mergeHeap) Push(x interface{}) { *h = append(*h, x.(heapItem)) }

func (h *mergeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// mergeIterator 对多个有序数据源做 k 路归并。
// 调用方必须按"新到旧"的顺序提供数据源(src 0 最新)。
// 同一个 Key 只产出最新版本——可能是一条 Tombstone,
// 是否丢弃 Tombstone 由调用方(Scan / Compaction)决定。
type mergeIterator struct {
	iters   []entryIterator
	h       mergeHeap
	cur     Entry
	lastKey string
	hasLast bool
}

func newMergeIterator(iters []entryIterator) *mergeIterator {
	m := &mergeIterator{iters: iters}
	for i, it := range iters {
		if it.Next() {
			m.h = append(m.h, heapItem{e: it.Entry(), src: i})
		}
	}
	heap.Init(&m.h)
	return m
}

func (m *mergeIterator) Next() bool {
	for len(m.h) > 0 {
		item := heap.Pop(&m.h).(heapItem)
		if m.iters[item.src].Next() {
			heap.Push(&m.h, heapItem{e: m.iters[item.src].Entry(), src: item.src})
		}
		if m.hasLast && item.e.Key == m.lastKey {
			continue // 同 Key 的旧版本,被更新版本(或 Tombstone)屏蔽
		}
		m.hasLast = true
		m.lastKey = item.e.Key
		m.cur = item.e
		return true
	}
	return false
}

func (m *mergeIterator) Entry() Entry { return m.cur }
