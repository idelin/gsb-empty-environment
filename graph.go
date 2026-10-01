package canon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// GNode is a node of a rooted directed graph. Label is any tree Value
// (nil is treated as Null). Out is the ordered list of successors;
// order is significant (array semantics) and preserved. Shared
// references and cycles (including self-loops) are allowed.
type GNode struct {
	Label Value
	Out   []*GNode
}

// ErrBudgetExceeded is returned by EncodeGraph when the bounded
// individualization-refinement search runs out of budget. The canonical
// form is then undetermined and the result is explicitly incomplete:
// no encoding is produced.
var ErrBudgetExceeded = errors.New("canon: canonicalization budget exceeded, result incomplete")

// DefaultBudget is the search budget used when EncodeGraph gets budget <= 0.
// It counts refinement rounds performed during the search.
const DefaultBudget = 100000

// EncodeGraph returns the canonical encoding of the graph reachable from
// root. Two graphs produce identical bytes iff they are isomorphic as
// labeled, edge-ordered, rooted graphs — regardless of node creation
// order or traversal order. Discovery order is never used as the node
// numbering; the numbering is derived from a canonical discrete coloring
// obtained by partition refinement plus bounded individualization-
// refinement search (lexicographically smallest certificate wins).
//
// budget bounds the number of refinement rounds; budget <= 0 selects
// DefaultBudget. If the budget is exhausted, ErrBudgetExceeded is
// returned and the encoding is left undefined.
func EncodeGraph(root *GNode, budget int) ([]byte, error) {
	if root == nil {
		return nil, errors.New("canon: nil graph root")
	}
	if budget <= 0 {
		budget = DefaultBudget
	}
	nodes, idx := reachable(root)
	labels := make([][]byte, len(nodes))
	for i, n := range nodes {
		label := n.Label
		if label == nil {
			label = Null{}
		}
		lk, err := Encode(label)
		if err != nil {
			return nil, fmt.Errorf("canon: node %d label: %w", i, err)
		}
		labels[i] = lk
	}
	s := &searcher{
		nodes:  nodes,
		idx:    idx,
		labels: labels,
		root:   idx[root],
		budget: budget,
	}
	if err := s.search(initialPartition(labels)); err != nil {
		return nil, err
	}
	rank := inversePerm(s.bestOrder)
	buf := []byte{tagGraph}
	buf = binary.AppendUvarint(buf, uint64(len(s.bestOrder)))
	for _, i := range s.bestOrder {
		buf = append(buf, labels[i]...)
		buf = binary.AppendUvarint(buf, uint64(len(nodes[i].Out)))
		for _, m := range nodes[i].Out {
			buf = binary.AppendUvarint(buf, uint64(rank[idx[m]]))
		}
	}
	buf = binary.AppendUvarint(buf, uint64(rank[s.root]))
	return buf, nil
}

// reachable collects nodes in BFS discovery order. This order is only an
// internal working indexing; it never leaks into the canonical output.
func reachable(root *GNode) ([]*GNode, map[*GNode]int) {
	idx := map[*GNode]int{root: 0}
	queue := []*GNode{root}
	nodes := []*GNode{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, m := range n.Out {
			if m == nil {
				continue
			}
			if _, ok := idx[m]; !ok {
				idx[m] = len(nodes)
				nodes = append(nodes, m)
				queue = append(queue, m)
			}
		}
	}
	return nodes, idx
}

// initialPartition colors nodes by the rank of their label encoding.
func initialPartition(labels [][]byte) []int {
	sorted := make([][]byte, len(labels))
	copy(sorted, labels)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i], sorted[j]) < 0 })
	rank := make(map[string]int, len(labels))
	for _, l := range sorted {
		k := string(l)
		if _, ok := rank[k]; !ok {
			rank[k] = len(rank)
		}
	}
	color := make([]int, len(labels))
	for i, l := range labels {
		color[i] = rank[string(l)]
	}
	return color
}

type searcher struct {
	nodes     []*GNode
	idx       map[*GNode]int
	labels    [][]byte
	root      int
	budget    int
	best      []byte // best (smallest) certificate so far
	bestOrder []int  // node indices in canonical order
}

// search performs individualization-refinement over the given partition.
func (s *searcher) search(color []int) error {
	if s.budget <= 0 {
		return ErrBudgetExceeded
	}
	s.budget--
	color = s.refine(color)

	maxColor := 0
	sizes := map[int]int{}
	for _, c := range color {
		sizes[c]++
		if c > maxColor {
			maxColor = c
		}
	}
	// Target cell: the non-singleton cell with the smallest color id.
	// This choice depends only on the coloring, not on discovery order.
	target := -1
	for c := 0; c <= maxColor; c++ {
		if sizes[c] > 1 {
			target = c
			break
		}
	}
	if target == -1 {
		cert, order := s.certificate(color)
		if s.best == nil || bytes.Compare(cert, s.best) < 0 {
			s.best = cert
			s.bestOrder = order
		}
		return nil
	}
	// Individualize each member of the target cell in turn and keep the
	// smallest resulting certificate, so the outcome is independent of
	// the order in which members are visited.
	for i, c := range color {
		if c != target {
			continue
		}
		next := make([]int, len(color))
		copy(next, color)
		next[i] = maxColor + 1
		if err := s.search(next); err != nil {
			return err
		}
	}
	return nil
}

// refine performs color refinement (1-WL style) until stable. Each
// node's new signature is its own color followed by the ordered colors
// of its successors; signatures are re-ranked by sorted order, which is
// isomorphism-invariant.
func (s *searcher) refine(color []int) []int {
	n := len(s.nodes)
	distinct := countDistinct(color)
	for {
		sigs := make([][]byte, n)
		for i, nd := range s.nodes {
			sig := binary.AppendUvarint(nil, uint64(color[i]))
			for _, m := range nd.Out {
				sig = binary.AppendUvarint(sig, uint64(color[s.idx[m]]))
			}
			sigs[i] = sig
		}
		sorted := make([][]byte, n)
		copy(sorted, sigs)
		sort.Slice(sorted, func(a, b int) bool { return bytes.Compare(sorted[a], sorted[b]) < 0 })
		rank := make(map[string]int, n)
		for _, sig := range sorted {
			k := string(sig)
			if _, ok := rank[k]; !ok {
				rank[k] = len(rank)
			}
		}
		next := make([]int, n)
		for i, sig := range sigs {
			next[i] = rank[string(sig)]
		}
		if len(rank) == distinct {
			return next
		}
		distinct = len(rank)
		color = next
	}
}

// certificate serializes a discrete coloring: nodes in color order,
// each as label + ordered successor ranks, plus the root rank.
func (s *searcher) certificate(color []int) ([]byte, []int) {
	order := argsort(color)
	rank := inversePerm(order)
	cert := binary.AppendUvarint(nil, uint64(len(order)))
	for _, i := range order {
		cert = append(cert, s.labels[i]...)
		cert = binary.AppendUvarint(cert, uint64(len(s.nodes[i].Out)))
		for _, m := range s.nodes[i].Out {
			cert = binary.AppendUvarint(cert, uint64(rank[s.idx[m]]))
		}
	}
	cert = binary.AppendUvarint(cert, uint64(rank[s.root]))
	return cert, order
}

func countDistinct(color []int) int {
	seen := make(map[int]struct{}, len(color))
	for _, c := range color {
		seen[c] = struct{}{}
	}
	return len(seen)
}

func argsort(color []int) []int {
	order := make([]int, len(color))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return color[order[a]] < color[order[b]] })
	return order
}

func inversePerm(order []int) []int {
	rank := make([]int, len(order))
	for pos, i := range order {
		rank[i] = pos
	}
	return rank
}

// DecodeGraph parses a canonical graph encoding and rebuilds the graph,
// restoring shared references and cycles. Only the subgraph reachable
// from the root is represented. All counts and indices are validated
// before allocation and dereference.
func DecodeGraph(data []byte) (*GNode, error) {
	d := &decoder{buf: data}
	root, err := d.graph()
	if err != nil {
		return nil, err
	}
	if len(d.buf) != 0 {
		return nil, ErrTrailingData
	}
	return root, nil
}

func (d *decoder) graph() (*GNode, error) {
	tag, err := d.readByte()
	if err != nil {
		return nil, err
	}
	if tag != tagGraph {
		return nil, fmt.Errorf("canon: expected graph tag, got 0x%02x", tag)
	}
	n, err := d.readCount()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errors.New("canon: graph must contain at least one node")
	}
	labels := make([]Value, n)
	edges := make([][]uint64, n)
	for i := uint64(0); i < n; i++ {
		labels[i], err = d.value(0)
		if err != nil {
			return nil, fmt.Errorf("canon: node %d label: %w", i, err)
		}
		deg, err := d.readCount()
		if err != nil {
			return nil, err
		}
		es := make([]uint64, deg)
		for j := range es {
			t, err := d.readUvarint()
			if err != nil {
				return nil, err
			}
			if t >= n {
				return nil, fmt.Errorf("canon: edge target %d out of range (%d nodes)", t, n)
			}
			es[j] = t
		}
		edges[i] = es
	}
	rootIdx, err := d.readUvarint()
	if err != nil {
		return nil, err
	}
	if rootIdx >= n {
		return nil, fmt.Errorf("canon: root index %d out of range (%d nodes)", rootIdx, n)
	}
	nodes := make([]*GNode, n)
	for i := range nodes {
		nodes[i] = &GNode{Label: labels[i]}
	}
	for i := range nodes {
		out := make([]*GNode, len(edges[i]))
		for j, t := range edges[i] {
			out[j] = nodes[t]
		}
		nodes[i].Out = out
	}
	return nodes[rootIdx], nil
}
