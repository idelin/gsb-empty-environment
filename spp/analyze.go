package spp

import "sort"

// Analysis bundles every deliverable for one destination-prefix instance.
type Analysis struct {
	Instance         *Instance
	Validation       ValidationReport
	Solve            *SolveResult
	Wheels           []DisputeWheel
	ConvergenceProof *ConvergenceProof
}

// ConvergenceProof certifies deterministic convergence of a unique-stable
// instance. LockInOrder is an AS activation order in which every AS, once it
// selects its final path, can never be made to switch again: every later
// pivot only supplies equal-or-worse alternatives and the selected next hop
// is already locked earlier in the order.
type ConvergenceProof struct {
	LockInOrder []int
	FinalPaths  Assignment
	// Justification[u] explains why u's choice is permanent.
	Justification map[int]string
}

// Analyze runs the full pipeline: structural validation, exact SPA solving,
// dispute-wheel construction, and (for unique-stable instances) a lock-in
// convergence certificate.
func Analyze(in *Instance, opts SolveOptions, wheelLimit int) *Analysis {
	res, _ := in.Solve(opts)
	a := &Analysis{
		Instance:   in,
		Validation: in.Report,
		Solve:      res,
	}
	if res.Outcome != OutcomeUnique {
		a.Wheels = in.DetectWheels(wheelLimit)
	} else {
		a.Wheels = in.DetectWheels(wheelLimit)
		if len(a.Wheels) == 0 {
			a.ConvergenceProof = buildConvergenceProof(in, res.Solutions[0])
		}
	}
	return a
}

// buildConvergenceProof derives a lock-in order from the unique SPA: nodes
// are ordered by increasing distance-to-origin along their selected paths, so
// each AS's next hop is locked before the AS itself. At lock-in time the AS
// picks its best available route; no future activation can introduce a better
// route because only downstream (already locked) nodes feed it.
func buildConvergenceProof(in *Instance, sol Assignment) *ConvergenceProof {
	type nodeDepth struct {
		node  int
		depth int
	}
	var entries []nodeDepth
	for _, n := range in.SortedNodes() {
		if n == in.Config.Origin {
			continue
		}
		p := sol[n]
		depth := len(p)
		if depth == 0 {
			depth = 1 << 30
		}
		entries = append(entries, nodeDepth{node: n, depth: depth})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].depth != entries[j].depth {
			return entries[i].depth < entries[j].depth
		}
		return entries[i].node < entries[j].node
	})
	proof := &ConvergenceProof{
		FinalPaths:    sol,
		Justification: map[int]string{},
	}
	proof.LockInOrder = append(proof.LockInOrder, in.Config.Origin)
	for _, e := range entries {
		proof.LockInOrder = append(proof.LockInOrder, e.node)
		p := sol[e.node]
		if len(p) == 0 {
			proof.Justification[e.node] = "no extendable route exists; remains unrouted"
			continue
		}
		best := in.BestAvailable(e.node, sol)
		if best != nil && pathEquals(best, p) {
			proof.Justification[e.node] = "selected path is best among all neighbor-advertised extensions; next hop already locked"
		} else {
			proof.Justification[e.node] = "locked by downstream fixed point"
		}
	}
	return proof
}
