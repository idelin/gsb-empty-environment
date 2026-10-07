package bisim

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Quotient is the quotient graph of an automaton under its coarsest
// bisimulation: one node per equivalence block, plus the mapping from
// old states to new block ids.
type Quotient struct {
	// BlockOf maps each original state to its block id in [0, NumBlocks).
	BlockOf []int
	// NumBlocks is the number of blocks (states of the quotient graph).
	NumBlocks int
	// Output[b] is the common output label of every state in block b.
	Output []string
	// Edges[b][label] is the sorted, deduplicated list of target blocks
	// reachable from block b via label. A missing key means no state of
	// the block has that transition.
	Edges []map[string][]int
}

// BisimulationQuotient computes the coarsest bisimulation partition by
// partition refinement and returns the quotient graph together with the
// old-state -> new-block mapping.
//
// The initial partition separates states by output label only; the
// refinement then splits blocks whenever two members disagree on the set
// of (edge label, target block) moves. A state with no transition for a
// label therefore never merges with a state that has one (even into a
// rejecting block), and acceptance alone never causes a merge.
func (a *Automaton) BisimulationQuotient() *Quotient {
	n := len(a.outputs)
	labels := a.Labels()

	// Initial partition: group by output label.
	blockOf := make([]int, n)
	outKeys := make(map[string]int)
	var outList []string
	for s := 0; s < n; s++ {
		id, ok := outKeys[a.outputs[s]]
		if !ok {
			id = len(outList)
			outKeys[a.outputs[s]] = id
			outList = append(outList, a.outputs[s])
		}
		blockOf[s] = id
	}

	// Refine until stable: split by the full move signature
	// {(label, block of target)} over deduplicated successors.
	for {
		sig := make([]string, n)
		for s := 0; s < n; s++ {
			var b strings.Builder
			for _, l := range labels {
				targets := a.adj[s][l]
				if len(targets) == 0 {
					continue
				}
				b.WriteString(l)
				b.WriteByte(':')
				seen := make(map[int]bool)
				var blocks []int
				for _, t := range targets {
					if !seen[blockOf[t]] {
						seen[blockOf[t]] = true
						blocks = append(blocks, blockOf[t])
					}
				}
				sort.Ints(blocks)
				for i, bl := range blocks {
					if i > 0 {
						b.WriteByte(',')
					}
					b.WriteString(strconv.Itoa(bl))
				}
				b.WriteByte(';')
			}
			sig[s] = b.String()
		}

		type key struct {
			block int
			sig   string
		}
		ids := make(map[key]int)
		next := make([]int, n)
		count := 0
		for s := 0; s < n; s++ {
			k := key{blockOf[s], sig[s]}
			id, ok := ids[k]
			if !ok {
				id = count
				ids[k] = id
				count++
			}
			next[s] = id
		}
		if count == numDistinct(blockOf) {
			break
		}
		blockOf = next
	}

	// Renumber blocks canonically: ordered by the smallest member state.
	order := make(map[int]int)
	canon := make([]int, n)
	nb := 0
	for s := 0; s < n; s++ {
		id, ok := order[blockOf[s]]
		if !ok {
			id = nb
			order[blockOf[s]] = id
			nb++
		}
		canon[s] = id
	}

	q := &Quotient{
		BlockOf:   canon,
		NumBlocks: nb,
		Output:    make([]string, nb),
		Edges:     make([]map[string][]int, nb),
	}
	for b := 0; b < nb; b++ {
		q.Edges[b] = make(map[string][]int)
	}
	for s := 0; s < n; s++ {
		q.Output[q.BlockOf[s]] = a.outputs[s]
	}
	for s := 0; s < n; s++ {
		from := q.BlockOf[s]
		for _, l := range labels {
			for _, t := range a.adj[s][l] {
				to := q.BlockOf[t]
				lst := q.Edges[from][l]
				i := sort.SearchInts(lst, to)
				if i >= len(lst) || lst[i] != to {
					lst = append(lst, 0)
					copy(lst[i+1:], lst[i:])
					lst[i] = to
					q.Edges[from][l] = lst
				}
			}
		}
	}
	return q
}

func numDistinct(v []int) int {
	seen := make(map[int]bool)
	for _, x := range v {
		seen[x] = true
	}
	return len(seen)
}

// String renders the quotient graph for debugging.
func (q *Quotient) String() string {
	var b strings.Builder
	for bl := 0; bl < q.NumBlocks; bl++ {
		fmt.Fprintf(&b, "block %d output=%q\n", bl, q.Output[bl])
		var labels []string
		for l := range q.Edges[bl] {
			labels = append(labels, l)
		}
		sort.Strings(labels)
		for _, l := range labels {
			fmt.Fprintf(&b, "  -%s-> %v\n", l, q.Edges[bl][l])
		}
	}
	return b.String()
}
