package graphsum

import (
	"bytes"
	"sort"
	"strconv"
	"strings"
)

// Summary is the canonical description of a graph.
type Summary struct {
	// Encoding is the lexicographically minimal serialization over all
	// node numberings. Isomorphic graphs share it; non-isomorphic graphs
	// (respecting root, labels, direction, and parallel-edge counts) differ.
	Encoding string
	// Numbering maps canonical position -> stable node ID. It is the
	// lexicographically smallest numbering (by node-ID sequence) among all
	// numberings that attain Encoding.
	Numbering []int
	// Orbits are the automorphism orbits: maximal groups of node IDs that
	// some automorphism of the graph maps onto each other. Each orbit is
	// sorted ascending; orbits are sorted by first element.
	Orbits [][]int
}

// codec precomputes everything permutation-independent about a graph so
// that encoding one numbering is pure concatenation. Cell serializations
// are built once; each of the up to 40320 numberings then costs O(n^2)
// buffer appends.
type codec struct {
	ids     []int
	index   map[int]int // node ID -> position in ids
	rootID  int
	hasRoot bool
	labels  []string   // escaped label per ids position
	cells   [][]string // cells[i][j]: serialized labeled multiplicities ids[i]->ids[j]
}

func newCodec(g *Graph) *codec {
	ids := g.NodeIDs()
	n := len(ids)
	c := &codec{
		ids:     ids,
		index:   make(map[int]int, n),
		rootID:  g.root,
		hasRoot: g.hasRoot,
		labels:  make([]string, n),
		cells:   make([][]string, n),
	}
	for i, id := range ids {
		c.index[id] = i
		c.labels[i] = escape(g.labels[id])
	}
	for i := range c.cells {
		c.cells[i] = make([]string, n)
	}
	for i, u := range ids {
		for j, v := range ids {
			cell := g.adj[u][v]
			if len(cell) == 0 {
				continue
			}
			labels := make([]string, 0, len(cell))
			for l := range cell {
				labels = append(labels, l)
			}
			sort.Strings(labels)
			var b strings.Builder
			for k, l := range labels {
				if k > 0 {
					b.WriteByte(',')
				}
				b.WriteString(escape(l))
				b.WriteByte('x')
				b.WriteString(strconv.Itoa(cell[l]))
			}
			c.cells[i][j] = b.String()
		}
	}
	return c
}

// appendEnc appends the encoding of the given numbering to buf.
// numbering[i] is the node ID at canonical position i; inv is scratch
// space of len(ids) used for the inverse permutation.
//
// Layout: root position | escaped node labels in canonical order |
// one cell per (i,j) in canonical order. Parallel edges appear as
// multiplicities; edge insertion order is irrelevant by construction.
func (c *codec) appendEnc(buf []byte, numbering []int, inv []int) []byte {
	n := len(c.ids)
	for pos, id := range numbering {
		inv[pos] = c.index[id]
	}
	buf = append(buf, "r="...)
	rootPos := -1
	if c.hasRoot {
		for pos, id := range numbering {
			if id == c.rootID {
				rootPos = pos
				break
			}
		}
	}
	buf = strconv.AppendInt(buf, int64(rootPos), 10)
	buf = append(buf, "|n="...)
	for i := range numbering {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, c.labels[inv[i]]...)
	}
	buf = append(buf, "|e="...)
	first := true
	for i := 0; i < n; i++ {
		ci := c.cells[inv[i]]
		for j := 0; j < n; j++ {
			if !first {
				buf = append(buf, ';')
			}
			first = false
			buf = append(buf, ci[inv[j]]...)
		}
	}
	return buf
}

// encode serializes the graph under the given numbering (numbering[i] is
// the node ID placed at canonical position i).
func (g *Graph) encode(numbering []int) string {
	c := newCodec(g)
	return string(c.appendEnc(nil, numbering, make([]int, len(c.ids))))
}

// escape makes labels safe for the positional encoding format.
func escape(s string) string {
	r := strings.NewReplacer(
		"\\", "\\\\",
		"|", "\\p",
		",", "\\c",
		";", "\\s",
		"=", "\\e",
	)
	return r.Replace(s)
}

// permutations enumerates all permutations of ids in lexicographic order
// (iterative next-permutation), calling fn for each. Enumeration stops
// early if fn returns false.
func permutations(ids []int, fn func(perm []int) bool) {
	perm := make([]int, len(ids))
	copy(perm, ids) // ids must be sorted ascending for lexicographic order
	for {
		if !fn(perm) {
			return
		}
		if !nextPerm(perm) {
			return
		}
	}
}

// nextPerm advances p to the next lexicographic permutation.
func nextPerm(p []int) bool {
	n := len(p)
	i := n - 2
	for i >= 0 && p[i] >= p[i+1] {
		i--
	}
	if i < 0 {
		return false
	}
	j := n - 1
	for p[j] <= p[i] {
		j--
	}
	p[i], p[j] = p[j], p[i]
	for lo, hi := i+1, n-1; lo < hi; lo, hi = lo+1, hi-1 {
		p[lo], p[hi] = p[hi], p[lo]
	}
	return true
}

// Canonical computes the canonical summary by exact enumeration of all n!
// numberings. Every numbering attaining the minimal encoding is retained:
// those numberings are exactly Aut(g) composed with any one of them, which
// yields the automorphism orbits.
func Canonical(g *Graph) (*Summary, error) {
	if err := g.validateForSummary(); err != nil {
		return nil, err
	}
	ids := g.NodeIDs()
	if len(ids) == 0 {
		return &Summary{Encoding: g.encode(nil), Numbering: nil, Orbits: nil}, nil
	}

	c := newCodec(g)
	buf := make([]byte, 0, 256)
	inv := make([]int, len(ids))
	var best []byte
	var optimal [][]int
	permutations(ids, func(perm []int) bool {
		buf = c.appendEnc(buf[:0], perm, inv)
		switch {
		case best == nil || bytes.Compare(buf, best) < 0:
			best = append(best[:0], buf...)
			optimal = optimal[:0]
			optimal = append(optimal, append([]int(nil), perm...))
		case bytes.Equal(buf, best):
			optimal = append(optimal, append([]int(nil), perm...))
		}
		return true
	})

	// Numbering: lexicographically smallest optimal numbering by ID
	// sequence. Enumeration is lexicographic, so optimal[0] qualifies.
	numbering := optimal[0]

	// Orbits: nodes sharing a canonical position across optimal numberings
	// are automorphism-equivalent. Union-find over positions.
	orbits := orbitsFromOptimal(ids, optimal)

	return &Summary{Encoding: string(best), Numbering: numbering, Orbits: orbits}, nil
}

// orbitsFromOptimal unions nodes that occupy the same canonical position in
// any two optimal numberings.
func orbitsFromOptimal(ids []int, optimal [][]int) [][]int {
	n := len(ids)
	parent := make(map[int]int, n)
	for _, id := range ids {
		parent[id] = id
	}
	var find func(x int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	for pos := 0; pos < n; pos++ {
		for k := 1; k < len(optimal); k++ {
			ra, rb := find(optimal[0][pos]), find(optimal[k][pos])
			if ra != rb {
				parent[ra] = rb
			}
		}
	}
	groups := make(map[int][]int)
	for _, id := range ids {
		r := find(id)
		groups[r] = append(groups[r], id)
	}
	orbits := make([][]int, 0, len(groups))
	for _, members := range groups {
		sort.Ints(members)
		orbits = append(orbits, members)
	}
	sort.Slice(orbits, func(a, b int) bool { return orbits[a][0] < orbits[b][0] })
	return orbits
}
