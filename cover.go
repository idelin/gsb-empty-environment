package diag

import "fmt"

// MaxCandidates is the maximum number of root candidates supported by the
// exact subset enumeration (2^20 subsets is the practical ceiling).
const MaxCandidates = 20

// bitSet is a fixed-capacity bit vector over observed faults.
type bitSet []uint64

func newBitSet(n int) bitSet { return make(bitSet, (n+63)/64) }

func (b bitSet) set(i int) { b[i/64] |= 1 << uint(i%64) }

func (b bitSet) orInto(dst bitSet) {
	for i := range dst {
		dst[i] |= b[i]
	}
}

func (b bitSet) equals(o bitSet) bool {
	for i := range b {
		if b[i] != o[i] {
			return false
		}
	}
	return true
}

// FaultWitness is covering evidence: a concrete propagation path from a
// member of the root component to the observed fault.
type FaultWitness struct {
	Fault string   `json:"fault"`
	Path  []string `json:"path"`
}

// CoverEvidence records which observed faults one chosen root explains.
type CoverEvidence struct {
	Root   []string       `json:"root"` // members of the root SCC
	Faults []FaultWitness `json:"faults"`
}

// RootSet is one minimum-cardinality solution.
type RootSet struct {
	// Roots are the chosen components, each given as its member node names.
	Roots    [][]string      `json:"roots"`
	Evidence []CoverEvidence `json:"evidence"`
}

// CoverResult summarizes the minimum-root-set analysis.
type CoverResult struct {
	// Components are all SCCs (member names), cycles included as one entry.
	Components [][]string `json:"components"`
	// Candidates are the source SCCs used as root candidates.
	Candidates [][]string `json:"candidates"`
	// Observed echoes the observed fault list.
	Observed []string `json:"observed"`
	// MinSize is the minimum number of roots needed.
	MinSize int `json:"minSize"`
	// NumSolutions is the total number of minimum solutions found.
	NumSolutions int `json:"numSolutions"`
	// Solutions holds up to maxSolutions minimum solutions.
	Solutions []RootSet `json:"solutions"`
}

// MinRootSets finds all minimum-cardinality sets of root candidates whose
// reachable components cover every observed fault. Candidates are the source
// components of the condensation DAG (every fault is reachable from some
// source, so sources always suffice). It enumerates subsets in increasing
// cardinality and stops at the first covering size, which guarantees
// minimality. maxSolutions caps the stored solutions (<=0 means store all).
func MinRootSets(g *Graph, observed []string, maxSolutions int) (*CoverResult, error) {
	if len(observed) == 0 {
		return nil, fmt.Errorf("diag: no observed faults given")
	}
	comps := StronglyConnectedComponents(g)
	if len(comps.Sources) > MaxCandidates {
		return nil, fmt.Errorf("diag: %d root candidates exceed limit %d", len(comps.Sources), MaxCandidates)
	}
	obsIdx := make([]int, len(observed))
	for i, name := range observed {
		idx, err := g.nodeIndex(name)
		if err != nil {
			return nil, err
		}
		obsIdx[i] = idx
	}

	// Coverage bitmask of each candidate over observed faults.
	ncand := len(comps.Sources)
	cov := make([]bitSet, ncand)
	for ci, src := range comps.Sources {
		reach := comps.reachableFrom(src)
		bs := newBitSet(len(observed))
		for fi, node := range obsIdx {
			if reach[comps.CompOf[node]] {
				bs.set(fi)
			}
		}
		cov[ci] = bs
	}
	full := newBitSet(len(observed))
	for i := range observed {
		full.set(i)
	}

	// Enumerate subsets by increasing size; first covering size is minimal.
	var chosen [][]int
	minSize := -1
	for size := 0; size <= ncand && minSize < 0; size++ {
		forEachSubset(ncand, size, func(sub []int) {
			union := newBitSet(len(observed))
			for _, ci := range sub {
				cov[ci].orInto(union)
			}
			if union.equals(full) {
				chosen = append(chosen, append([]int(nil), sub...))
			}
		})
		if len(chosen) > 0 {
			minSize = size
		}
	}
	if minSize < 0 {
		// Unreachable: sources cover every component, so this only happens
		// if observed faults reference a graph with no nodes.
		return nil, fmt.Errorf("diag: no root set covers all observed faults")
	}

	res := &CoverResult{
		Observed:     append([]string(nil), observed...),
		MinSize:      minSize,
		NumSolutions: len(chosen),
	}
	for _, comp := range comps.Comps {
		res.Components = append(res.Components, compNames(g, comp))
	}
	for _, src := range comps.Sources {
		res.Candidates = append(res.Candidates, compNames(g, comps.Comps[src]))
	}
	limit := len(chosen)
	if maxSolutions > 0 && limit > maxSolutions {
		limit = maxSolutions
	}
	for _, sub := range chosen[:limit] {
		res.Solutions = append(res.Solutions, buildRootSet(g, comps, obsIdx, observed, sub))
	}
	return res, nil
}

func compNames(g *Graph, comp []int) []string {
	names := make([]string, len(comp))
	for i, v := range comp {
		names[i] = g.names[v]
	}
	return names
}

func buildRootSet(g *Graph, comps *Components, obsIdx []int, observed []string, sub []int) RootSet {
	rs := RootSet{}
	for _, ci := range sub {
		src := comps.Sources[ci]
		members := comps.Comps[src]
		rootNames := compNames(g, members)
		rs.Roots = append(rs.Roots, rootNames)
		reach := comps.reachableFrom(src)
		ev := CoverEvidence{Root: rootNames}
		for fi, node := range obsIdx {
			if reach[comps.CompOf[node]] {
				ev.Faults = append(ev.Faults, FaultWitness{
					Fault: observed[fi],
					Path:  witnessPath(g, comps, members, node),
				})
			}
		}
		rs.Evidence = append(rs.Evidence, ev)
	}
	return rs
}

// witnessPath finds a concrete node-level path from any member of the root
// component to the fault node via BFS, serving as covering evidence.
func witnessPath(g *Graph, comps *Components, rootMembers []int, fault int) []string {
	prev := make([]int, g.NumNodes())
	seen := make([]bool, g.NumNodes())
	for i := range prev {
		prev[i] = -1
	}
	var queue []int
	for _, m := range rootMembers {
		if !seen[m] {
			seen[m] = true
			queue = append(queue, m)
		}
	}
	for len(queue) > 0 && !seen[fault] {
		v := queue[0]
		queue = queue[1:]
		for _, w := range g.adj[v] {
			if !seen[w] {
				seen[w] = true
				prev[w] = v
				queue = append(queue, w)
			}
		}
	}
	if !seen[fault] {
		return nil
	}
	var rev []int
	for v := fault; v != -1; v = prev[v] {
		rev = append(rev, v)
	}
	path := make([]string, len(rev))
	for i, v := range rev {
		path[len(rev)-1-i] = g.names[v]
	}
	return path
}

// forEachSubset calls fn for every size-k subset of {0..n-1}.
func forEachSubset(n, k int, fn func([]int)) {
	if k < 0 || k > n {
		return
	}
	sub := make([]int, k)
	var rec func(start, depth int)
	rec = func(start, depth int) {
		if depth == k {
			fn(sub)
			return
		}
		for i := start; i <= n-(k-depth); i++ {
			sub[depth] = i
			rec(i+1, depth+1)
		}
	}
	rec(0, 0)
}

// CycleViolation reports an SCC whose members do not share identical
// fault coverage, which would mean a cycle is mishandled.
type CycleViolation struct {
	Component []string `json:"component"`
	Reason    string   `json:"reason"`
}

// VerifyCyclesSingleRoot computationally verifies, by enumerating every
// member of every non-trivial SCC, that all members of a cycle reach exactly
// the same set of observed faults. This is the check that a cycle is soundly
// treated as ONE root candidate rather than several independent roots.
func VerifyCyclesSingleRoot(g *Graph, observed []string) ([]CycleViolation, error) {
	comps := StronglyConnectedComponents(g)
	obsIdx := make([]int, len(observed))
	for i, name := range observed {
		idx, err := g.nodeIndex(name)
		if err != nil {
			return nil, err
		}
		obsIdx[i] = idx
	}
	// Per-node coverage over observed faults, via BFS on the raw graph.
	coverOf := func(start int) bitSet {
		bs := newBitSet(len(observed))
		seen := make([]bool, g.NumNodes())
		queue := []int{start}
		seen[start] = true
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			for fi, node := range obsIdx {
				if node == v {
					bs.set(fi)
				}
			}
			for _, w := range g.adj[v] {
				if !seen[w] {
					seen[w] = true
					queue = append(queue, w)
				}
			}
		}
		return bs
	}
	var violations []CycleViolation
	for _, comp := range comps.Comps {
		if len(comp) < 2 {
			continue
		}
		want := coverOf(comp[0])
		for _, member := range comp[1:] {
			if got := coverOf(member); !got.equals(want) {
				violations = append(violations, CycleViolation{
					Component: compNames(g, comp),
					Reason: fmt.Sprintf("member %q covers different faults than %q",
						g.names[member], g.names[comp[0]]),
				})
			}
		}
	}
	return violations, nil
}
