package graphcanon

import "sort"

// Result is the canonical form of a graph.
type Result struct {
	// Code is the canonical encoding: the lexicographically minimal
	// encoding over all root-fixing numberings. Two graphs are isomorphic
	// (as rooted, labeled, directed multigraphs) iff their Codes are equal.
	Code string

	// Numbering maps stable node identities (original indices) to
	// canonical positions. Numbering[g.Root()] == 0 always. Among all
	// numberings achieving Code, the lexicographically smallest one is
	// chosen so the mapping is deterministic.
	Numbering []int

	// Orbits are the automorphism orbits of the graph, expressed in stable
	// (original) node identities. Each orbit is sorted ascending and the
	// list is sorted by first element, so it is deterministic.
	Orbits [][]int

	// Automorphisms is the full automorphism group of the canonical form,
	// as permutations of canonical positions (sigma[p] = image of p).
	// Together with Numbering it is a complete, independently verifiable
	// proof of the symmetry structure: conjugating sigma through Numbering
	// yields an automorphism of the original graph that the independent
	// checker VerifyPermutation validates.
	Automorphisms [][]int
}

// Canonicalize computes the exact canonical form of g by enumerating all
// root-fixing numberings and taking the lexicographically minimal encoding.
// For n <= 8 this is at most 7! = 5040 full encodings, so the result is
// exact: no hashing, no refinement heuristics, no cycle truncation.
func Canonicalize(g *Graph) *Result {
	n := g.n

	// Enumerate every numbering (orig -> canonical position) that keeps the
	// root at canonical position 0, and keep the minimal encoding. Ties are
	// broken by the lexicographically smallest numbering for determinism.
	nonRoot := make([]int, 0, n-1)
	for i := 0; i < n; i++ {
		if i != g.root {
			nonRoot = append(nonRoot, i)
		}
	}
	numbering := make([]int, n)
	numbering[g.root] = 0
	usedPos := make([]bool, n)
	usedPos[0] = true

	best := ""
	var bestNum []int
	var enum func(idx int)
	enum = func(idx int) {
		if idx == len(nonRoot) {
			code := encode(g, numbering)
			if bestNum == nil || code < best ||
				(code == best && numberingLess(numbering, bestNum)) {
				best = code
				bestNum = append([]int(nil), numbering...)
			}
			return
		}
		orig := nonRoot[idx]
		for pos := 1; pos < n; pos++ {
			if usedPos[pos] {
				continue
			}
			usedPos[pos] = true
			numbering[orig] = pos
			enum(idx + 1)
			usedPos[pos] = false
		}
	}
	enum(0)

	// Canonical view of the graph under the winning numbering.
	inv := invertPerm(bestNum)
	clab := make([]string, n)
	cadj := make([][]int, n)
	for p := 0; p < n; p++ {
		clab[p] = g.labels[inv[p]]
		cadj[p] = make([]int, n)
		for q := 0; q < n; q++ {
			cadj[p][q] = g.adj[inv[p]][inv[q]]
		}
	}

	// Full automorphism group of the canonical form: every permutation
	// sigma of canonical positions with sigma[0] == 0 that preserves
	// labels and adjacency multiplicities. Partial assignments are pruned
	// only by constraints that are already fully determined, so the
	// enumeration stays exact.
	var aut [][]int
	sigma := make([]int, n)
	used := make([]bool, n)
	var autEnum func(p int)
	autEnum = func(p int) {
		if p == n {
			aut = append(aut, append([]int(nil), sigma...))
			return
		}
		for c := 0; c < n; c++ {
			if used[c] {
				continue
			}
			if p == 0 && c != 0 {
				continue // root stays at canonical position 0
			}
			if clab[p] != clab[c] {
				continue
			}
			ok := cadj[p][p] == cadj[c][c]
			for q := 0; q < p && ok; q++ {
				if cadj[p][q] != cadj[c][sigma[q]] ||
					cadj[q][p] != cadj[sigma[q]][c] {
					ok = false
				}
			}
			if !ok {
				continue
			}
			sigma[p] = c
			used[c] = true
			autEnum(p + 1)
			used[c] = false
		}
	}
	autEnum(0)

	return &Result{
		Code:          best,
		Numbering:     bestNum,
		Orbits:        computeOrbits(n, bestNum, aut),
		Automorphisms: aut,
	}
}

// numberingLess reports whether a is lexicographically smaller than b.
func numberingLess(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// invertPerm returns the inverse of a permutation.
func invertPerm(perm []int) []int {
	inv := make([]int, len(perm))
	for i, p := range perm {
		inv[p] = i
	}
	return inv
}

// computeOrbits computes the orbits of the canonical positions under the
// given automorphism group and maps them back to stable (original) node
// identities via numbering. The result is normalized: each orbit sorted
// ascending, orbits sorted by first element.
func computeOrbits(n int, numbering []int, group [][]int) [][]int {
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(x int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	for _, sigma := range group {
		for c := 0; c < n; c++ {
			parent[find(c)] = find(sigma[c])
		}
	}
	inv := invertPerm(numbering)
	groups := make(map[int][]int)
	for c := 0; c < n; c++ {
		r := find(c)
		groups[r] = append(groups[r], inv[c])
	}
	orbits := make([][]int, 0, len(groups))
	for _, members := range groups {
		sort.Ints(members)
		orbits = append(orbits, members)
	}
	sort.Slice(orbits, func(i, j int) bool { return orbits[i][0] < orbits[j][0] })
	return orbits
}
