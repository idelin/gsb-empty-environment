package main

import "container/heap"

type bfItem struct {
	forest []*Node
	key    string
	dist   int
}

type bfHeap []bfItem

func (h bfHeap) Len() int            { return len(h) }
func (h bfHeap) Less(i, j int) bool  { return h[i].dist < h[j].dist }
func (h bfHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *bfHeap) Push(x interface{}) { *h = append(*h, x.(bfItem)) }
func (h *bfHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// bruteForceDistance recomputes the edit distance by exhaustive Dijkstra
// search over forest states. It is only practical for short trees and is
// meant as an independent cross-check of the dynamic program.
//
// Zero-cost operations cannot make the search loop: every generated state is
// a different forest, each state is settled at most once via the best-known
// distance map, and the node-count cap bounds the state space.
//
// Returns (distance, true), or (_, false) when the search is inconclusive
// (state cap exceeded or goal unreachable within the caps).
func bruteForceDistance(source, target *Node, c *CostTable, maxStates int) (int, bool) {
	// The alphabet covers both trees, every label the cost table mentions
	// (a cheap rename or delete may route through a label that occurs in
	// neither tree) and one wildcard standing for all remaining labels.
	alphabet := c.labelUniverse(source, target)

	// There is always an optimal script that renames first, then deletes,
	// then inserts (the mapping-based script), whose intermediate forests
	// never exceed max(|source|, |target|) nodes. Capping the search at
	// that size therefore keeps the exhaustive search sound.
	maxNodes := max(Size(source), Size(target))

	goal := SerializeForest([]*Node{target})
	start := []*Node{source.Clone()}
	startKey := SerializeForest(start)
	best := map[string]int{startKey: 0}
	h := &bfHeap{{forest: start, key: startKey, dist: 0}}
	heap.Init(h)
	states := 0
	for h.Len() > 0 {
		cur := heap.Pop(h).(bfItem)
		if d, ok := best[cur.key]; ok && cur.dist > d {
			continue
		}
		if cur.key == goal {
			return cur.dist, true
		}
		states++
		if states > maxStates {
			return 0, false
		}
		for _, nb := range bfNeighbors(cur.forest, alphabet, maxNodes, c) {
			nd := cur.dist + nb.cost
			key := SerializeForest(nb.forest)
			if old, ok := best[key]; ok && old <= nd {
				continue
			}
			best[key] = nd
			heap.Push(h, bfItem{forest: nb.forest, key: key, dist: nd})
		}
	}
	return 0, false
}

type bfNeighbor struct {
	forest []*Node
	cost   int
}

// bfNeighbors enumerates every forest reachable in one primitive edit step:
// rename of any node, delete of any node (children lift into its place), or
// insert of a new node that adopts a (possibly empty) consecutive run of its
// new parent's children.
func bfNeighbors(forest []*Node, alphabet []string, maxNodes int, c *CostTable) []bfNeighbor {
	var out []bfNeighbor
	n := forestSize(forest)

	// Collect every node as a path of child indices from the top level.
	var paths [][]int
	var labels []*Node
	var walk func(nodes []*Node, prefix []int)
	walk = func(nodes []*Node, prefix []int) {
		for i, nd := range nodes {
			p := append(append([]int{}, prefix...), i)
			paths = append(paths, p)
			labels = append(labels, nd)
			walk(nd.Children, p)
		}
	}
	walk(forest, nil)

	nodeAt := func(f []*Node, path []int) *Node {
		nd := f[path[0]]
		for _, i := range path[1:] {
			nd = nd.Children[i]
		}
		return nd
	}

	for pi, path := range paths {
		// Renames (never to the same label: a no-op rename would only add
		// zero-cost self loops).
		for _, l := range alphabet {
			if l == labels[pi].Label {
				continue
			}
			nf := cloneForest(forest)
			nodeAt(nf, path).Label = l
			out = append(out, bfNeighbor{forest: nf, cost: c.RenCost(labels[pi].Label, l)})
		}
		// Delete: the node's children take its place.
		nf := cloneForest(forest)
		if len(path) == 1 {
			i := path[0]
			victim := nf[i]
			spliced := make([]*Node, 0, len(nf)-1+len(victim.Children))
			spliced = append(spliced, nf[:i]...)
			spliced = append(spliced, victim.Children...)
			spliced = append(spliced, nf[i+1:]...)
			nf = spliced
		} else {
			parent := nodeAt(nf, path[:len(path)-1])
			i := path[len(path)-1]
			victim := parent.Children[i]
			spliced := make([]*Node, 0, len(parent.Children)-1+len(victim.Children))
			spliced = append(spliced, parent.Children[:i]...)
			spliced = append(spliced, victim.Children...)
			spliced = append(spliced, parent.Children[i+1:]...)
			parent.Children = spliced
		}
		out = append(out, bfNeighbor{forest: nf, cost: c.DelCost(labels[pi].Label)})
	}

	// Inserts: under every possible parent (nil = top level), at every
	// position, adopting every possible consecutive run of following
	// children. This is the exact inverse of the delete above.
	if n+1 <= maxNodes {
		// parentPath[i] is the path of the node whose child list receives
		// the insert; an empty path with top==true means the top level.
		type parentRef struct {
			path  []int
			count int
		}
		parents := []parentRef{{path: nil, count: len(forest)}}
		for pi := range labels {
			parents = append(parents, parentRef{path: paths[pi], count: len(labels[pi].Children)})
		}
		for _, pr := range parents {
			childCount := pr.count
			for pos := 0; pos <= childCount; pos++ {
				for _, l := range alphabet {
					for adopt := 0; adopt <= childCount-pos; adopt++ {
						nf := cloneForest(forest)
						var siblings *[]*Node
						if pr.path == nil {
							siblings = &nf
						} else {
							siblings = &nodeAt(nf, pr.path).Children
						}
						ch := *siblings
						nn := &Node{Label: l}
						ch = append(ch, nil)
						copy(ch[pos+1:], ch[pos:])
						ch[pos] = nn
						adopted := make([]*Node, adopt)
						copy(adopted, ch[pos+1:pos+1+adopt])
						nn.Children = adopted
						ch = append(ch[:pos+1], ch[pos+1+adopt:]...)
						*siblings = ch
						out = append(out, bfNeighbor{forest: nf, cost: c.InsCost(l)})
					}
				}
			}
		}
	}
	return out
}
