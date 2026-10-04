package main

import (
	"container/heap"
	"sort"
)

// ExhaustiveDistance recomputes the edit distance by Dijkstra search over
// forest states, using the raw cost table and the raw operation semantics.
// Zero-cost operations cannot make it loop: states are finite (node count is
// capped and the label universe is finite) and each state is settled once
// via the best-distance map. Returns (distance, statesSettled, conclusive).
func ExhaustiveDistance(t1, t2 *Tree, c *CostTable, maxStates int) (int, int, bool) {
	universe := labelUniverse(t1, t2, c)
	universe[freshLabel(universe)] = true
	labels := make([]string, 0, len(universe))
	for l := range universe {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	n1, n2 := countNodes(t1), countNodes(t2)
	maxN := n1
	if n2 > maxN {
		maxN = n2
	}

	start := []*Tree{cloneTree(t1)}
	goal := encodeForest([]*Tree{t2})

	dist := map[string]int{encodeForest(start): 0}
	pq := &forestPQ{{f: start, key: encodeForest(start), d: 0}}
	heap.Init(pq)

	states := 0
	for pq.Len() > 0 {
		it := heap.Pop(pq).(*fqItem)
		if it.d > dist[it.key] {
			continue
		}
		if it.key == goal {
			return it.d, states, true
		}
		states++
		if states > maxStates {
			return 0, states, false
		}
		for _, sc := range successors(it.f, labels, maxN, c) {
			k := encodeForest(sc.f)
			nd := it.d + sc.cost
			if cur, seen := dist[k]; !seen || nd < cur {
				dist[k] = nd
				heap.Push(pq, &fqItem{f: sc.f, key: k, d: nd})
			}
		}
	}
	return 0, states, false
}

type succ struct {
	f    []*Tree
	cost int
}

// successors enumerates every single-operation successor of a forest.
func successors(f []*Tree, labels []string, maxNodes int, c *CostTable) []succ {
	var out []succ
	paths := allPaths(f)
	for _, p := range paths {
		node := nodeAt(f, p)
		// delete: children lift into place; cost is for this one node only
		out = append(out, succ{deleteAtPath(f, p), c.Del(node.Label)})
		// rename
		for _, L := range labels {
			if L != node.Label {
				out = append(out, succ{renameAtPath(f, p, L), c.Ren(node.Label, L)})
			}
		}
	}
	if countForest(f) < maxNodes {
		// insert at every parent (including top level), index, adoption count
		parents := append([][]int{nil}, paths...)
		for _, pp := range parents {
			ch := childrenAt(f, pp)
			for index := 0; index <= len(ch); index++ {
				for adopt := 0; adopt+index <= len(ch); adopt++ {
					for _, L := range labels {
						out = append(out, succ{insertAtPath(f, pp, index, adopt, L), c.Ins(L)})
					}
				}
			}
		}
	}
	return out
}

func countForest(f []*Tree) int {
	n := 0
	for _, t := range f {
		n += countNodes(t)
	}
	return n
}

func allPaths(f []*Tree) [][]int {
	var paths [][]int
	var rec func(children []*Tree, prefix []int)
	rec = func(children []*Tree, prefix []int) {
		for i, ch := range children {
			p := append(append([]int{}, prefix...), i)
			paths = append(paths, p)
			rec(ch.Children, p)
		}
	}
	rec(f, nil)
	return paths
}

func nodeAt(f []*Tree, path []int) *Tree {
	cur := f
	for i, idx := range path {
		if i == len(path)-1 {
			return cur[idx]
		}
		cur = cur[idx].Children
	}
	return nil
}

func childrenAt(f []*Tree, parentPath []int) []*Tree {
	if len(parentPath) == 0 {
		return f
	}
	return nodeAt(f, parentPath).Children
}

// deleteAtPath removes the node at path, lifting its children into place.
func deleteAtPath(f []*Tree, path []int) []*Tree {
	idx := path[0]
	if len(path) == 1 {
		node := f[idx]
		out := make([]*Tree, 0, len(f)-1+len(node.Children))
		out = append(out, f[:idx]...)
		out = append(out, node.Children...)
		out = append(out, f[idx+1:]...)
		return out
	}
	out := append([]*Tree{}, f...)
	victim := f[idx]
	out[idx] = &Tree{Label: victim.Label, Children: deleteAtPath(victim.Children, path[1:])}
	return out
}

func renameAtPath(f []*Tree, path []int, label string) []*Tree {
	idx := path[0]
	out := append([]*Tree{}, f...)
	victim := f[idx]
	if len(path) == 1 {
		out[idx] = &Tree{Label: label, Children: victim.Children}
	} else {
		out[idx] = &Tree{Label: victim.Label, Children: renameAtPath(victim.Children, path[1:], label)}
	}
	return out
}

// insertAtPath inserts a new node as the index-th child of the node at
// parentPath (empty = forest top level); the new node adopts the adopt
// consecutive children starting at index. Inverse of deleteAtPath.
func insertAtPath(f []*Tree, parentPath []int, index, adopt int, label string) []*Tree {
	if len(parentPath) == 0 {
		return insertIntoList(f, index, adopt, label)
	}
	idx := parentPath[0]
	out := append([]*Tree{}, f...)
	victim := f[idx]
	out[idx] = &Tree{Label: victim.Label, Children: insertAtPath(victim.Children, parentPath[1:], index, adopt, label)}
	return out
}

func insertIntoList(list []*Tree, index, adopt int, label string) []*Tree {
	nw := &Tree{Label: label, Children: append([]*Tree{}, list[index:index+adopt]...)}
	out := make([]*Tree, 0, len(list)-adopt+1)
	out = append(out, list[:index]...)
	out = append(out, nw)
	out = append(out, list[index+adopt:]...)
	return out
}

// --- priority queue ---

type fqItem struct {
	f     []*Tree
	key   string
	d     int
	index int
}

type forestPQ []*fqItem

func (q forestPQ) Len() int           { return len(q) }
func (q forestPQ) Less(i, j int) bool { return q[i].d < q[j].d }
func (q forestPQ) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index = i
	q[j].index = j
}
func (q *forestPQ) Push(x any) {
	it := x.(*fqItem)
	it.index = len(*q)
	*q = append(*q, it)
}
func (q *forestPQ) Pop() any {
	old := *q
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*q = old[:n-1]
	return it
}
