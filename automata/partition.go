package automata

import (
	"fmt"
	"sort"
	"strings"
)

// Partition is an equivalence partition of the states of an Automaton.
// Block IDs are canonical: blocks are ordered by their smallest member.
type Partition struct {
	BlockOf []int   // BlockOf[s] is the block ID of state s, in [0, NumBlocks).
	Blocks  [][]int // Blocks[b] lists the member states, sorted ascending.
}

// NumBlocks returns the number of blocks.
func (p *Partition) NumBlocks() int { return len(p.Blocks) }

// CoarsestBisimulation computes the coarsest partition P of the states such
// that any two states in the same block
//  1. carry the same output label, and
//  2. for every edge label l, have the same multiset of l-successor blocks
//     (counting parallel edges).
//
// The result is the greatest fixed point of the usual refinement operator,
// computed by iterative signature refinement. Missing transitions are
// distinguished from transitions to any state (including rejecting ones),
// because both alter the successor multiset.
func CoarsestBisimulation(a *Automaton) *Partition {
	succ := a.succIndex()

	// Initial partition: group by output label.
	blockOf := make([]int, a.NumStates)
	byOutput := make(map[string][]int)
	for s := 0; s < a.NumStates; s++ {
		byOutput[a.Outputs[s]] = append(byOutput[a.Outputs[s]], s)
	}
	outputs := make([]string, 0, len(byOutput))
	for o := range byOutput {
		outputs = append(outputs, o)
	}
	sort.Strings(outputs)
	for id, o := range outputs {
		for _, s := range byOutput[o] {
			blockOf[s] = id
		}
	}
	numBlocks := len(outputs)

	// Refine until stable: split every block by the signature
	//   label -> (successor block -> multiplicity).
	for {
		sigs := make([]string, a.NumStates)
		for s := 0; s < a.NumStates; s++ {
			sigs[s] = signature(succ[s], blockOf)
		}
		// Split: within each current block, group states by signature.
		newBlockOf := make([]int, a.NumStates)
		type groupKey struct {
			block int
			sig   string
		}
		groups := make(map[groupKey][]int)
		var order []groupKey // deterministic: first-seen order by state id
		for s := 0; s < a.NumStates; s++ {
			k := groupKey{blockOf[s], sigs[s]}
			if _, ok := groups[k]; !ok {
				order = append(order, k)
			}
			groups[k] = append(groups[k], s)
		}
		// Sort groups by smallest member for canonical IDs.
		sort.Slice(order, func(i, j int) bool {
			return groups[order[i]][0] < groups[order[j]][0]
		})
		newNum := 0
		for _, k := range order {
			for _, s := range groups[k] {
				newBlockOf[s] = newNum
			}
			newNum++
		}
		if newNum == numBlocks {
			break
		}
		blockOf = newBlockOf
		numBlocks = newNum
	}

	blocks := make([][]int, numBlocks)
	for s := 0; s < a.NumStates; s++ {
		blocks[blockOf[s]] = append(blocks[blockOf[s]], s)
	}
	return &Partition{BlockOf: blockOf, Blocks: blocks}
}

// signature encodes label -> sorted list of (successor block, multiplicity).
func signature(succ map[string]map[int]int, blockOf []int) string {
	labels := make([]string, 0, len(succ))
	for l := range succ {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	var b strings.Builder
	for _, l := range labels {
		targets := succ[l]
		type tc struct {
			block, count int
		}
		pairs := make([]tc, 0, len(targets))
		for to, n := range targets {
			pairs = append(pairs, tc{blockOf[to], n})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].block < pairs[j].block })
		fmt.Fprintf(&b, "%s:[", l)
		for _, p := range pairs {
			fmt.Fprintf(&b, "%d^%d,", p.block, p.count)
		}
		b.WriteString("]")
	}
	return b.String()
}

// QEdge is one edge of the quotient graph, with aggregated multiplicity.
type QEdge struct {
	From  int
	Label string
	To    int
	Count int
}

// Quotient is the quotient graph of an Automaton under a bisimulation
// partition: nodes are blocks, edges are block-level transitions with
// multiplicities (per representative state).
type Quotient struct {
	Part    *Partition
	Outputs []string // Outputs[b] is the common output label of block b.
	Edges   []QEdge  // Sorted by (From, Label, To).
}

// QuotientGraph builds the quotient graph for a partition. The partition must
// be a bisimulation (every member of a block has identical block-level
// behavior); use CoarsestBisimulation to obtain one.
func QuotientGraph(a *Automaton, p *Partition) *Quotient {
	q := &Quotient{Part: p, Outputs: make([]string, p.NumBlocks())}
	for b, members := range p.Blocks {
		q.Outputs[b] = a.Outputs[members[0]]
	}
	// Aggregate edges per block representative.
	type key struct {
		from, to int
		label    string
	}
	agg := make(map[key]int)
	for b, members := range p.Blocks {
		rep := members[0]
		for _, e := range a.Edges {
			if e.From != rep {
				continue
			}
			agg[key{b, p.BlockOf[e.To], e.Label}]++
		}
	}
	for k, n := range agg {
		q.Edges = append(q.Edges, QEdge{From: k.from, Label: k.label, To: k.to, Count: n})
	}
	sort.Slice(q.Edges, func(i, j int) bool {
		x, y := q.Edges[i], q.Edges[j]
		if x.From != y.From {
			return x.From < y.From
		}
		if x.Label != y.Label {
			return x.Label < y.Label
		}
		return x.To < y.To
	})
	return q
}

// Mapping returns the old-to-new state mapping as a human-readable,
// deterministically ordered string: "old->new" pairs sorted by old state.
func (p *Partition) Mapping() string {
	var b strings.Builder
	for s, blk := range p.BlockOf {
		if s > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d->%d", s, blk)
	}
	return b.String()
}
