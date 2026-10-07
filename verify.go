package graphsum

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Verify independently checks a claimed Summary against the graph.
//
// It is deliberately implemented as a separate code path from Canonical:
//   - the minimal encoding is recomputed with an independent serialization
//     format (length-prefixed, sparse) and an independent recursive
//     permutation enumeration (Heap's algorithm), so a bug in the
//     producer's encoder or traversal cannot be masked by symmetry;
//   - the claimed numbering is re-encoded with the producer's format and
//     must attain the claimed encoding;
//   - the automorphism group is recomputed by testing every permutation of
//     the nodes for the automorphism property directly (root, labels,
//     directed edges, and parallel-edge multiplicities all preserved), and
//     the claimed orbits must equal the orbits of that group.
//
// A proof that passes Verify never relies on hashing or recursion that
// truncates at cycles: every one of the n! permutations is inspected.
func Verify(g *Graph, s *Summary) error {
	if err := g.validateForSummary(); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("nil summary")
	}
	ids := g.NodeIDs()
	n := len(ids)

	// 1. Independently recompute the minimal canonical form and the
	//    automorphism orbits by brute-force permutation testing.
	minEnc, orbitSets := bruteForceCanonical(g, ids)
	if s.Encoding != minEnc {
		return fmt.Errorf("encoding mismatch: claimed %q, independently computed %q", s.Encoding, minEnc)
	}

	// 2. The claimed numbering must be a permutation of the node IDs that
	//    attains the claimed encoding under the producer's format.
	if len(s.Numbering) != n {
		return fmt.Errorf("numbering has %d entries, graph has %d nodes", len(s.Numbering), n)
	}
	seen := make(map[int]bool, n)
	for _, id := range s.Numbering {
		if _, ok := g.labels[id]; !ok {
			return fmt.Errorf("numbering references unknown node %d", id)
		}
		if seen[id] {
			return fmt.Errorf("numbering repeats node %d", id)
		}
		seen[id] = true
	}
	if enc := verifyEncode(g, s.Numbering); enc != s.Encoding {
		return fmt.Errorf("claimed numbering does not produce claimed encoding")
	}

	// 3. Claimed orbits must equal the independently computed automorphism
	//    orbits (as a partition of the node set).
	claimed := normalizeOrbits(s.Orbits)
	if !samePartition(claimed, orbitSets) {
		return fmt.Errorf("orbit partition mismatch: claimed %v, independently computed %v", claimed, orbitSets)
	}
	return nil
}

// verifyEncode is the checker's own implementation of the canonical
// encoding format specified in canon.go. It is written independently
// (straightforward map lookups and string building, no shared codec), so a
// defect in the producer's optimized encoder cannot propagate here. The
// format itself is part of the canonical-form specification and must match.
func verifyEncode(g *Graph, numbering []int) string {
	rootPos := -1
	if g.hasRoot {
		for i, id := range numbering {
			if id == g.root {
				rootPos = i
				break
			}
		}
	}
	var b strings.Builder
	b.WriteString("r=")
	b.WriteString(strconv.Itoa(rootPos))
	b.WriteString("|n=")
	for i, id := range numbering {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(escape(g.labels[id]))
	}
	b.WriteString("|e=")
	n := len(numbering)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i > 0 || j > 0 {
				b.WriteByte(';')
			}
			cell := g.adj[numbering[i]][numbering[j]]
			labels := make([]string, 0, len(cell))
			for l := range cell {
				labels = append(labels, l)
			}
			sort.Strings(labels)
			for k, l := range labels {
				if k > 0 {
					b.WriteByte(',')
				}
				b.WriteString(escape(l))
				b.WriteByte('x')
				b.WriteString(strconv.Itoa(cell[l]))
			}
		}
	}
	return b.String()
}

// bruteForceCanonical is the independent checker core. It enumerates all
// permutations recursively (Heap's algorithm) to find the minimal
// canonical encoding, and separately tests every permutation directly for
// the automorphism property to derive the orbits.
func bruteForceCanonical(g *Graph, ids []int) (string, [][]int) {
	n := len(ids)
	if n == 0 {
		return g.encode(nil), nil
	}
	// Checker-side precomputation, built directly from the adjacency maps:
	// escaped labels and serialized cells per ids position pair.
	idx := make(map[int]int, n)
	labelStr := make([]string, n)
	for i, id := range ids {
		idx[id] = i
		labelStr[i] = escape(g.labels[id])
	}
	cellStr := make([][]string, n)
	for i := range cellStr {
		cellStr[i] = make([]string, n)
		for j := range cellStr[i] {
			cellStr[i][j] = serializeCell(g.adj[ids[i]][ids[j]])
		}
	}
	rootIdx := -1
	if g.hasRoot {
		rootIdx = idx[g.root]
	}

	perm := make([]int, n)
	copy(perm, ids)
	// pos[i] holds the ids-position sitting at canonical position i.
	pos := make([]int, n)

	best := ""
	buf := make([]byte, 0, 256)
	var autos [][]int
	var walk func(k int)
	walk = func(k int) {
		if k <= 1 {
			for i, id := range perm {
				pos[i] = idx[id]
			}
			buf = buf[:0]
			buf = append(buf, "r="...)
			rp := -1
			if rootIdx >= 0 {
				for i := range pos {
					if pos[i] == rootIdx {
						rp = i
						break
					}
				}
			}
			buf = strconv.AppendInt(buf, int64(rp), 10)
			buf = append(buf, "|n="...)
			for i := 0; i < n; i++ {
				if i > 0 {
					buf = append(buf, ',')
				}
				buf = append(buf, labelStr[pos[i]]...)
			}
			buf = append(buf, "|e="...)
			for i := 0; i < n; i++ {
				row := cellStr[pos[i]]
				for j := 0; j < n; j++ {
					if i > 0 || j > 0 {
						buf = append(buf, ';')
					}
					buf = append(buf, row[pos[j]]...)
				}
			}
			if best == "" || string(buf) < best {
				best = string(buf)
			}
			if isAutomorphism(g, pos, n, rootIdx) {
				autos = append(autos, append([]int(nil), perm...))
			}
			return
		}
		for i := 0; i < k; i++ {
			walk(k - 1)
			if k%2 == 0 {
				perm[i], perm[k-1] = perm[k-1], perm[i]
			} else {
				perm[0], perm[k-1] = perm[k-1], perm[0]
			}
		}
	}
	walk(n)

	// Orbits of the independently found automorphism group: union each node
	// with its image under every automorphism. autos[k][i] is the image of
	// ids[i] under automorphism k.
	parent := make(map[int]int, n)
	for _, id := range ids {
		parent[id] = id
	}
	var find func(x int) int
	find = func(x int) int {
		root := x
		for parent[root] != root {
			root = parent[root]
		}
		for parent[x] != root {
			parent[x], x = root, parent[x]
		}
		return root
	}
	for _, a := range autos {
		for i, id := range ids {
			ra, rb := find(id), find(a[i])
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
	for _, m := range groups {
		sort.Ints(m)
		orbits = append(orbits, m)
	}
	sort.Slice(orbits, func(a, b int) bool { return orbits[a][0] < orbits[b][0] })
	return best, orbits
}

// serializeCell renders one adjacency cell as "labelxcount" pairs sorted by
// label (the checker's own rendering of the canonical cell format).
func serializeCell(cell map[string]int) string {
	if len(cell) == 0 {
		return ""
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
	return b.String()
}

// isAutomorphism reports whether the bijection i -> pos[i] (in ids-position
// space) preserves root, labels, directed edges, and parallel-edge
// multiplicities.
func isAutomorphism(g *Graph, pos []int, n int, rootIdx int) bool {
	if rootIdx >= 0 && pos[rootIdx] != rootIdx {
		return false
	}
	ids := g.NodeIDs()
	for i := 0; i < n; i++ {
		if g.labels[ids[i]] != g.labels[ids[pos[i]]] {
			return false
		}
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if !sameCell(g.adj[ids[i]][ids[j]], g.adj[ids[pos[i]]][ids[pos[j]]]) {
				return false
			}
		}
	}
	return true
}

func sameCell(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for l, c := range a {
		if b[l] != c {
			return false
		}
	}
	return true
}

// normalizeOrbits returns a copy with members and the orbit list sorted.
func normalizeOrbits(orbits [][]int) [][]int {
	out := make([][]int, 0, len(orbits))
	for _, members := range orbits {
		m := make([]int, len(members))
		copy(m, members)
		sort.Ints(m)
		out = append(out, m)
	}
	sort.Slice(out, func(a, b int) bool {
		if len(out[a]) == 0 || len(out[b]) == 0 {
			return len(out[a]) < len(out[b])
		}
		return out[a][0] < out[b][0]
	})
	return out
}

func samePartition(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
