package canon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
)

var errNestedGraph = errors.New("canon: graph node labels must not contain graphs")

// canonicalizeGraph computes the canonical encoding of the rooted graph
// reachable from g.Root. The result depends only on the rooted labeled
// isomorphism class of the graph (labels, ordered adjacency, root), never
// on node pointer identity, allocation order, or discovery order.
//
// Method: color refinement (partition refinement with ordered neighbor
// signatures) followed by bounded individualization-refinement search.
// Color classes are always re-ranked by sorted signature so intermediate
// colors are canonical; individualization branches on every member of the
// smallest non-singleton cell, and the lexicographically smallest leaf
// encoding wins. Because every branch is explored (subject to budget),
// the minimum is independent of any input ordering. If the budget is
// exhausted the search reports ErrBudgetExhausted (explicitly incomplete)
// instead of emitting a possibly non-canonical encoding.
func canonicalizeGraph(g *Graph, budget *int64) ([]byte, error) {
	if g == nil || g.Root == nil {
		return nil, errors.New("canon: graph has nil root")
	}

	// Collect reachable nodes. The BFS index assignment below is used
	// only as scratch; the canonical result never depends on it.
	index := map[*Node]int{g.Root: 0}
	nodes := []*Node{g.Root}
	for i := 0; i < len(nodes); i++ {
		for _, w := range nodes[i].Edges {
			if w == nil {
				return nil, errors.New("canon: graph edge targets a nil node")
			}
			if _, ok := index[w]; !ok {
				index[w] = len(nodes)
				nodes = append(nodes, w)
			}
		}
	}
	n := len(nodes)

	adj := make([][]int, n)
	labels := make([][]byte, n)
	for i, u := range nodes {
		adj[i] = make([]int, len(u.Edges))
		for j, w := range u.Edges {
			adj[i][j] = index[w]
		}
		if containsGraph(u.Label) {
			return nil, errNestedGraph
		}
		var lb []byte
		if err := encodeValue(&lb, u.Label, budget); err != nil {
			return nil, err
		}
		labels[i] = lb
	}

	// Initial color: rank of the label encoding among all label
	// encodings, assigned in sorted order so colors are canonical.
	colors := initialColors(labels)
	colors, err := refine(adj, colors, budget)
	if err != nil {
		return nil, err
	}

	var best []byte
	if err := search(adj, labels, colors, index[g.Root], budget, &best); err != nil {
		return nil, err
	}
	return best, nil
}

func containsGraph(v Value) bool {
	if v.kind == KindGraph {
		return true
	}
	for _, e := range v.aval {
		if containsGraph(e) {
			return true
		}
	}
	for _, p := range v.oval {
		if containsGraph(p.Value) {
			return true
		}
	}
	return false
}

// rankBytes assigns contiguous ranks to byte-string items in sorted order.
func rankBytes(items [][]byte) []int {
	sorted := make([][]byte, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i], sorted[j]) < 0 })
	rank := make(map[string]int, len(items))
	for _, s := range sorted {
		key := string(s)
		if _, ok := rank[key]; !ok {
			rank[key] = len(rank)
		}
	}
	out := make([]int, len(items))
	for i, s := range items {
		out[i] = rank[string(s)]
	}
	return out
}

func initialColors(labels [][]byte) []int { return rankBytes(labels) }

// refine performs color refinement: each round recolors every node by
// the ordered signature (own color, colors of successors in edge order),
// re-ranking colors canonically by sorted signature. It stops when the
// partition is stable and returns the stable canonical coloring.
func refine(adj [][]int, colors []int, budget *int64) ([]int, error) {
	n := len(colors)
	numColors := 0
	for _, c := range colors {
		if c >= numColors {
			numColors = c + 1
		}
	}
	for {
		if *budget <= 0 {
			return nil, ErrBudgetExhausted
		}
		*budget--
		sigs := make([][]byte, n)
		for i := 0; i < n; i++ {
			sig := binary.AppendUvarint(nil, uint64(colors[i]))
			for _, t := range adj[i] {
				sig = binary.AppendUvarint(sig, uint64(colors[t]))
			}
			sigs[i] = sig
		}
		next := rankBytes(sigs)
		nextNum := 0
		for _, c := range next {
			if c >= nextNum {
				nextNum = c + 1
			}
		}
		if nextNum == numColors {
			return next, nil
		}
		colors, numColors = next, nextNum
	}
}

// search performs individualization-refinement search: it picks the
// smallest non-singleton color cell, branches on individualizing each of
// its members, refines, and recurses. At discrete colorings it records
// the lexicographically smallest leaf encoding.
func search(adj [][]int, labels [][]byte, colors []int, root int, budget *int64, best *[]byte) error {
	if *budget <= 0 {
		return ErrBudgetExhausted
	}
	*budget--
	n := len(colors)
	maxColor := 0
	cellSize := map[int]int{}
	for _, c := range colors {
		cellSize[c]++
		if c > maxColor {
			maxColor = c
		}
	}
	target := -1
	for c := 0; c <= maxColor; c++ {
		if cellSize[c] > 1 {
			target = c
			break
		}
	}
	if target < 0 {
		enc := leafEncoding(adj, labels, colors, root)
		if *best == nil || bytes.Compare(enc, *best) < 0 {
			*best = enc
		}
		return nil
	}
	for v := 0; v < n; v++ {
		if colors[v] != target {
			continue
		}
		indiv := make([]int, n)
		copy(indiv, colors)
		indiv[v] = maxColor + 1
		refined, err := refine(adj, indiv, budget)
		if err != nil {
			return err
		}
		if err := search(adj, labels, refined, root, budget, best); err != nil {
			return err
		}
	}
	return nil
}

// leafEncoding renders a discrete coloring as a candidate canonical
// encoding: nodes ordered by color, adjacency expressed in color indices.
func leafEncoding(adj [][]int, labels [][]byte, colors []int, root int) []byte {
	n := len(colors)
	order := make([]int, n) // order[color] = node
	for i, c := range colors {
		order[c] = i
	}
	enc := []byte{tagGraph}
	enc = binary.AppendUvarint(enc, uint64(n))
	enc = binary.AppendUvarint(enc, uint64(colors[root]))
	for c := 0; c < n; c++ {
		i := order[c]
		enc = append(enc, labels[i]...)
		enc = binary.AppendUvarint(enc, uint64(len(adj[i])))
		for _, t := range adj[i] {
			enc = binary.AppendUvarint(enc, uint64(colors[t]))
		}
	}
	return enc
}
