package factor

import (
	"fmt"
	"sort"
	"strings"
)

// Partition is a set partition of the variables {0..n-1} into disjoint
// non-empty blocks. The zero value is not valid; construct partitions with
// NewPartition, DiscretePartition, or OneBlockPartition, which put the
// partition into canonical form: variables inside each block are sorted
// ascending, and blocks are sorted by their smallest variable (block
// numbering order). Canonical form makes partitions comparable with Equal.
type Partition struct {
	Blocks [][]int
}

// NewPartition returns the canonical form of the partition given by blocks.
// It panics if blocks is not a set partition of {0..n-1} for some n.
func NewPartition(blocks [][]int) Partition {
	n := 0
	for _, b := range blocks {
		if len(b) == 0 {
			panic("factor: empty block in partition")
		}
		for _, v := range b {
			if v+1 > n {
				n = v + 1
			}
		}
	}
	seen := make([]bool, n)
	out := make([][]int, len(blocks))
	for i, b := range blocks {
		nb := append([]int(nil), b...)
		sort.Ints(nb)
		for _, v := range nb {
			if v < 0 || seen[v] {
				panic("factor: blocks do not form a set partition")
			}
			seen[v] = true
		}
		out[i] = nb
	}
	for _, ok := range seen {
		if !ok {
			panic("factor: blocks do not cover all variables")
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return Partition{Blocks: out}
}

// DiscretePartition returns the all-singletons partition of n variables.
func DiscretePartition(n int) Partition {
	blocks := make([][]int, n)
	for i := range blocks {
		blocks[i] = []int{i}
	}
	return Partition{Blocks: blocks}
}

// OneBlockPartition returns the single-block partition of n variables.
func OneBlockPartition(n int) Partition {
	block := make([]int, n)
	for i := range block {
		block[i] = i
	}
	return Partition{Blocks: [][]int{block}}
}

// NumVars returns the number of variables covered by the partition.
func (p Partition) NumVars() int {
	n := 0
	for _, b := range p.Blocks {
		n += len(b)
	}
	return n
}

// Equal reports whether p and q are the same canonical partition.
func (p Partition) Equal(q Partition) bool {
	if len(p.Blocks) != len(q.Blocks) {
		return false
	}
	for i := range p.Blocks {
		if len(p.Blocks[i]) != len(q.Blocks[i]) {
			return false
		}
		for j := range p.Blocks[i] {
			if p.Blocks[i][j] != q.Blocks[i][j] {
				return false
			}
		}
	}
	return true
}

// blockLabels returns, for each variable, the index of its block.
func (p Partition) blockLabels() []int {
	labels := make([]int, p.NumVars())
	for i, b := range p.Blocks {
		for _, v := range b {
			labels[v] = i
		}
	}
	return labels
}

// Refines reports whether p is at least as fine as q: every block of p is
// contained in some block of q.
func (p Partition) Refines(q Partition) bool {
	lp, lq := p.blockLabels(), q.blockLabels()
	// Map each p-block to the q-block of its first variable; every other
	// variable of the same p-block must land in the same q-block.
	for v := 0; v < len(lp); v++ {
		for w := v + 1; w < len(lp); w++ {
			if lp[v] == lp[w] && lq[v] != lq[w] {
				return false
			}
		}
	}
	return true
}

// Meet returns the finest common refinement of p and q: blocks are the
// non-empty intersections of a block of p with a block of q.
func Meet(p, q Partition) Partition {
	lp, lq := p.blockLabels(), q.blockLabels()
	type key struct{ a, b int }
	groups := map[key][]int{}
	var order []key
	for v := 0; v < len(lp); v++ {
		k := key{lp[v], lq[v]}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], v)
	}
	blocks := make([][]int, len(order))
	for i, k := range order {
		blocks[i] = groups[k]
	}
	return NewPartition(blocks)
}

// AllPartitions enumerates every set partition of {0..n-1} in canonical
// form, using restricted growth strings. The count follows the Bell
// numbers: 1, 2, 5, 15, 52, 203 for n = 1..6.
func AllPartitions(n int) []Partition {
	if n < 1 {
		return nil
	}
	var out []Partition
	rgs := make([]int, n)
	var rec func(i, maxBlock int)
	rec = func(i, maxBlock int) {
		if i == n {
			blocks := make([][]int, maxBlock+1)
			for v, b := range rgs {
				blocks[b] = append(blocks[b], v)
			}
			out = append(out, Partition{Blocks: blocks})
			return
		}
		for b := 0; b <= maxBlock+1; b++ {
			rgs[i] = b
			nb := maxBlock
			if b > nb {
				nb = b
			}
			rec(i+1, nb)
		}
	}
	rec(1, 0)
	return out
}

// String renders the partition canonically, e.g. "{0,2}|{1}|{3,4,5}".
func (p Partition) String() string {
	parts := make([]string, len(p.Blocks))
	for i, b := range p.Blocks {
		vs := make([]string, len(b))
		for j, v := range b {
			vs[j] = fmt.Sprint(v)
		}
		parts[i] = "{" + strings.Join(vs, ",") + "}"
	}
	return strings.Join(parts, "|")
}
