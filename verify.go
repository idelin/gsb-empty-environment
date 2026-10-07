package graphcanon

import "sort"

// This file is the independent verifier. It deliberately shares no logic
// with the canonicalizer (canon.go / encode.go): isomorphism here is
// decided by exhaustive permutation search, and every claimed proof
// permutation is validated directly against the graph structure. Nothing
// is hashed and nothing is truncated at cycles.

// VerifyPermutation independently checks that perm is an isomorphism from
// a to b, where node i of a corresponds to node perm[i] of b. It validates
// the complete structure: bijectivity, root preservation, label equality,
// and exact adjacency multiplicities (parallel edges included).
func VerifyPermutation(a, b *Graph, perm []int) bool {
	if a.n != b.n || len(perm) != a.n {
		return false
	}
	seen := make([]bool, b.n)
	for i, p := range perm {
		if p < 0 || p >= b.n || seen[p] {
			return false
		}
		seen[p] = true
		if a.labels[i] != b.labels[p] {
			return false
		}
	}
	if perm[a.root] != b.root {
		return false
	}
	for i := 0; i < a.n; i++ {
		pi := perm[i]
		for j := 0; j < a.n; j++ {
			if a.adj[i][j] != b.adj[pi][perm[j]] {
				return false
			}
		}
	}
	return true
}

// AreIsomorphic decides rooted labeled directed-multigraph isomorphism by
// exhaustive enumeration of candidate permutations. Candidates are pruned
// only by locally checkable necessary conditions (root, label, degree
// signature, and consistency with already assigned nodes); every complete
// candidate is then validated by VerifyPermutation. The search is exact:
// it returns true iff an isomorphism exists.
func AreIsomorphic(a, b *Graph) bool {
	if a.n != b.n {
		return false
	}
	n := a.n

	// Isomorphism invariant: the multiset of (label, in-degree, out-degree)
	// node signatures must match, where degrees count multiplicities.
	sa := make([]nodeSig, n)
	sb := make([]nodeSig, n)
	for i := 0; i < n; i++ {
		sa[i] = signatureOf(a, i)
		sb[i] = signatureOf(b, i)
	}
	sortedA := append([]nodeSig(nil), sa...)
	sortedB := append([]nodeSig(nil), sb...)
	sort.Slice(sortedA, func(i, j int) bool { return sortedA[i].less(sortedA[j]) })
	sort.Slice(sortedB, func(i, j int) bool { return sortedB[i].less(sortedB[j]) })
	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}

	// Assign the root first to maximize early pruning.
	order := make([]int, 0, n)
	order = append(order, a.root)
	for i := 0; i < n; i++ {
		if i != a.root {
			order = append(order, i)
		}
	}

	perm := make([]int, n)
	used := make([]bool, n)
	assigned := make([]bool, n)
	var search func(k int) bool
	search = func(k int) bool {
		if k == n {
			return VerifyPermutation(a, b, perm)
		}
		i := order[k]
		for j := 0; j < n; j++ {
			if used[j] || sa[i] != sb[j] {
				continue
			}
			if (i == a.root) != (j == b.root) {
				continue
			}
			// Check consistency with every already assigned node and the
			// self-loop before descending.
			ok := a.adj[i][i] == b.adj[j][j]
			for i2 := 0; i2 < n && ok; i2++ {
				if !assigned[i2] {
					continue
				}
				j2 := perm[i2]
				if a.adj[i][i2] != b.adj[j][j2] ||
					a.adj[i2][i] != b.adj[j2][j] {
					ok = false
				}
			}
			if !ok {
				continue
			}
			perm[i] = j
			used[j] = true
			assigned[i] = true
			if search(k + 1) {
				return true
			}
			used[j] = false
			assigned[i] = false
		}
		return false
	}
	return search(0)
}

// nodeSig is an isomorphism-invariant node signature.
type nodeSig struct {
	label    string
	in, out  int
	selfLoop int
}

func signatureOf(g *Graph, i int) nodeSig {
	s := nodeSig{label: g.labels[i], selfLoop: g.adj[i][i]}
	for k := 0; k < g.n; k++ {
		s.out += g.adj[i][k]
		s.in += g.adj[k][i]
	}
	return s
}

func (s nodeSig) less(o nodeSig) bool {
	if s.label != o.label {
		return s.label < o.label
	}
	if s.in != o.in {
		return s.in < o.in
	}
	if s.out != o.out {
		return s.out < o.out
	}
	return s.selfLoop < o.selfLoop
}
