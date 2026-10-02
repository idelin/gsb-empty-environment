package stcs

import (
	"bytes"
	"container/heap"
)

type mergeItem struct {
	entry Entry
	src   int
}

// mergeHeap orders by key ascending, then by Seq descending so the newest
// version of a key pops first.
type mergeHeap []mergeItem

func (h mergeHeap) Len() int { return len(h) }

func (h mergeHeap) Less(i, j int) bool {
	if c := bytes.Compare(h[i].entry.Key, h[j].entry.Key); c != 0 {
		return c < 0
	}
	return h[i].entry.Seq > h[j].entry.Seq
}

func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *mergeHeap) Push(x any) { *h = append(*h, x.(mergeItem)) }

func (h *mergeHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// MergeTables performs a full k-way merge of the input tables into a single
// new SSTable at outPath. When the same key appears in several inputs (or
// several times across them), only the entry with the highest sequence
// number — the newest version — is kept. It returns the new table's
// metadata and the number of stale entries that were dropped.
func MergeTables(inputs []*Table, outPath string) (*Table, uint64, error) {
	its := make([]*Iterator, 0, len(inputs))
	defer func() {
		for _, it := range its {
			it.Close()
		}
	}()
	h := &mergeHeap{}
	var inCount uint64
	for _, t := range inputs {
		it, err := t.Iterator()
		if err != nil {
			return nil, 0, err
		}
		its = append(its, it)
		inCount += t.Count
		if it.Next() {
			heap.Push(h, mergeItem{entry: it.Entry(), src: len(its) - 1})
		}
	}
	advance := func(src int) {
		if its[src].Next() {
			heap.Push(h, mergeItem{entry: its[src].Entry(), src: src})
		}
	}
	w, err := NewTableWriter(outPath)
	if err != nil {
		return nil, 0, err
	}
	var outCount uint64
	for h.Len() > 0 {
		top := heap.Pop(h).(mergeItem)
		best := top.entry
		advance(top.src)
		// Drain every other copy of this key, keeping the newest.
		for h.Len() > 0 && bytes.Equal((*h)[0].entry.Key, best.Key) {
			dup := heap.Pop(h).(mergeItem)
			if dup.entry.Seq > best.Seq {
				best = dup.entry
			}
			advance(dup.src)
		}
		if err := w.Add(best); err != nil {
			return nil, 0, err
		}
		outCount++
	}
	meta, err := w.Finish()
	if err != nil {
		return nil, 0, err
	}
	return meta, inCount - outCount, nil
}
