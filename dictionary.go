package fuzzy

import (
	"container/heap"
	"sort"
)

// Unlimited is a maxCost value that never prunes the search.
const Unlimited = int(^uint(0) >> 1)

// Hit is one matched word together with its minimum edit cost.
type Hit struct {
	Word string
	Cost int
}

// SearchResult is the outcome of a Dictionary.Search.
type SearchResult struct {
	// Hits holds up to k distinct words ordered by minimum edit cost,
	// ties broken lexicographically.
	Hits []Hit
	// Proven is the number of leading hits whose ranks are guaranteed
	// to be the true top ranks: every word not examined during the
	// search has a cost strictly greater than Hits[Proven-1]. When the
	// budget is exhausted before the whole dictionary is explored, the
	// remaining hits are best-effort.
	Proven int
}

// Dictionary is a set of words stored as a trie so that fuzzy searches
// share the dynamic-programming work of common prefixes.
type Dictionary struct {
	root *node
	size int
}

type node struct {
	children map[rune]*node
	terminal bool
}

// New returns an empty dictionary.
func New() *Dictionary {
	return &Dictionary{root: &node{}}
}

// Insert adds word to the dictionary. Inserting an existing word is a
// no-op: the dictionary holds distinct words.
func (d *Dictionary) Insert(word string) {
	n := d.root
	for _, r := range word {
		if n.children == nil {
			n.children = make(map[rune]*node)
		}
		child, ok := n.children[r]
		if !ok {
			child = &node{}
			n.children[r] = child
		}
		n = child
	}
	if !n.terminal {
		n.terminal = true
		d.size++
	}
}

// Len returns the number of distinct words in the dictionary.
func (d *Dictionary) Len() int {
	return d.size
}

// Search returns the up to k dictionary words with the smallest edit cost
// to query, ordered by cost and then lexicographically.
//
// maxCost is the exploration budget: prefixes whose lower bound on the
// achievable cost exceeds maxCost are pruned. Use Unlimited to explore
// until the top k ranks are proven. When the budget runs out, the returned
// SearchResult.Proven reports how many leading ranks are still guaranteed
// to be correct.
func (d *Dictionary) Search(query string, k, maxCost int, c Costs) SearchResult {
	c.check()
	if k < 0 {
		panic("fuzzy: k must be non-negative")
	}
	if maxCost < 0 {
		panic("fuzzy: maxCost must be non-negative")
	}
	if k == 0 || d.size == 0 {
		return SearchResult{}
	}
	qr := []rune(query)
	n := len(qr)

	frontier := &frontierPQ{}
	prunedBound := Unlimited
	push := func(it *frontierItem) {
		if it.bound > maxCost {
			if it.bound < prunedBound {
				prunedBound = it.bound
			}
			return
		}
		heap.Push(frontier, it)
	}
	rootRow := initialRow(n, c.Del)
	push(&frontierItem{
		bound: rowBound(rootRow, nil),
		node:  d.root,
		cur:   rootRow,
	})

	var hits []Hit
	top := &maxCosts{} // the k smallest candidate costs seen so far
	kth := 0           // valid once len(hits) >= k

	for frontier.Len() > 0 {
		f := (*frontier)[0].bound
		if f > maxCost {
			break // budget exhausted
		}
		if len(hits) >= k && kth < f {
			break // every unexplored word costs more than the k-th hit
		}
		it := heap.Pop(frontier).(*frontierItem)
		if it.node.terminal {
			cost := it.cur[n]
			hits = append(hits, Hit{Word: it.prefix, Cost: cost})
			heap.Push(top, cost)
			if top.Len() > k {
				heap.Pop(top)
			}
			if len(hits) >= k {
				kth = (*top)[0]
			}
		}
		for r, child := range it.node.children {
			row := extendRow(qr, it.ch, r, it.prev, it.cur, c)
			push(&frontierItem{
				bound:  rowBound(row, it.cur),
				node:   child,
				prev:   it.cur,
				cur:    row,
				ch:     r,
				prefix: it.prefix + string(r),
			})
		}
	}

	// Every word not examined has cost >= f: the smallest lower bound
	// among frontier nodes and nodes pruned by the budget.
	f := prunedBound
	if frontier.Len() > 0 && (*frontier)[0].bound < f {
		f = (*frontier)[0].bound
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Cost != hits[j].Cost {
			return hits[i].Cost < hits[j].Cost
		}
		return hits[i].Word < hits[j].Word
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	proven := 0
	for _, h := range hits {
		if h.Cost < f {
			proven++
		}
	}
	return SearchResult{Hits: hits, Proven: proven}
}

// frontierItem is a trie node awaiting expansion. The lower bound is
// monotonic along every root-to-leaf path, and items are popped in
// non-decreasing bound order, so when the smallest bound in the frontier
// is f every unexplored word has cost >= f.
type frontierItem struct {
	bound  int
	node   *node
	prev   []int // DP row of the grandparent prefix, nil at depth 1
	cur    []int // DP row of this prefix
	ch     rune  // last rune of prefix
	prefix string
}

type frontierPQ []*frontierItem

func (pq frontierPQ) Len() int { return len(pq) }

func (pq frontierPQ) Less(i, j int) bool {
	if pq[i].bound != pq[j].bound {
		return pq[i].bound < pq[j].bound
	}
	return pq[i].prefix < pq[j].prefix
}

func (pq frontierPQ) Swap(i, j int) { pq[i], pq[j] = pq[j], pq[i] }

func (pq *frontierPQ) Push(x interface{}) {
	*pq = append(*pq, x.(*frontierItem))
}

func (pq *frontierPQ) Pop() interface{} {
	old := *pq
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*pq = old[:n-1]
	return it
}

// maxCosts is a max-heap of ints used to track the k-th smallest
// candidate cost.
type maxCosts []int

func (h maxCosts) Len() int            { return len(h) }
func (h maxCosts) Less(i, j int) bool  { return h[i] > h[j] }
func (h maxCosts) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *maxCosts) Push(x interface{}) { *h = append(*h, x.(int)) }

func (h *maxCosts) Pop() interface{} {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}
