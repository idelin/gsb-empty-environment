package crossmesh

import "fmt"

// IndependentAcceptorMaxNodes bounds the exhaustive semantic acceptor. It
// intentionally enumerates simple root-to-node paths, so it must never be run
// against the full-size graph; production verification uses the polynomial
// engine, while the acceptor serves as an independent cross-check on small
// graphs (notably in tests).
const IndependentAcceptorMaxNodes = 12

// IndependentAcceptor recomputes authority from the raw graph using explicit
// depth-first enumeration of every simple path ending at a trusted root. It
// shares no code with the Engine fixed point: it does not reduce the graph,
// does not compute (node, budget) states, and applies every per-hop constraint
// directly on the concrete chain under inspection.
type IndependentAcceptor struct {
	eng *Engine
}

// NewIndependentAcceptor requires a small graph and fails closed otherwise.
func NewIndependentAcceptor(eng *Engine) (*IndependentAcceptor, error) {
	if len(eng.nodes) > IndependentAcceptorMaxNodes {
		return nil, fmt.Errorf(
			"crossmesh: independent acceptor refuses graph of %d nodes (> %d): exhaustive path enumeration is test-only",
			len(eng.nodes), IndependentAcceptorMaxNodes)
	}
	return &IndependentAcceptor{eng: eng}, nil
}

// Accept recomputes the full voxel set of node and reports whether it exactly
// matches the engine's solved authority. A mismatch means an implementation
// defect (or a validator defect), never an authorization policy change.
func (a *IndependentAcceptor) Accept(node string) (bool, error) {
	idx, ok := a.eng.nodeIndex[node]
	if !ok {
		return false, fmt.Errorf("crossmesh: unknown node %q", node)
	}
	expected, err := a.eng.AuthorizedVoxels(node)
	if err != nil {
		return false, err
	}
	got := a.enumerate(idx)
	return got.equals(expected), nil
}

// enumerate walks every simple path starting at each trusted root and records,
// at target, the exact intersection of the single chain that reached it.
// Voxel fragments from two chains are only unioned as whole chain results, so
// dimensions from different parent chains can never be stitched together.
func (a *IndependentAcceptor) enumerate(target int) *vSet {
	eng := a.eng
	onPath := make([]bool, len(eng.nodes))
	pathEdges := make([]edgeCover, 0, len(eng.nodes))
	pathLimits := make([]int, 0, len(eng.nodes))
	result := newVSet()

	var dfs func(v int)
	dfs = func(v int) {
		if len(pathEdges) > 0 {
			if _, isRoot := eng.roots[v]; isRoot {
				// Returning to any trust anchor closes the chain; root nodes
				// never receive delegated slices through incoming edges.
				return
			}
		}
		if v == target && len(pathEdges) > 0 {
			_, targetIsRoot := eng.roots[target]
			if !targetIsRoot && a.pathBudgetOK(pathLimits) {
				result.unionInto(a.chainIntersection(pathEdges))
			}
		}
		onPath[v] = true
		for _, out := range eng.outAdj[v] {
			if onPath[out.target] {
				// Only acyclic chains are admissible; a path that would close a
				// loop contributes nothing and cannot bootstrap authority.
				continue
			}
			pathEdges = append(pathEdges, out.cover)
			pathLimits = append(pathLimits, out.limit)
			dfs(out.target)
			pathEdges = pathEdges[:len(pathEdges)-1]
			pathLimits = pathLimits[:len(pathLimits)-1]
		}
		onPath[v] = false
	}

	for r := range eng.roots {
		dfs(r)
	}
	return result
}

// pathBudgetOK validates the hop-by-hop decreasing-budget rule required by the
// package: each edge's MaxPathLen is a budget that must be at least the number
// of edges that follow it on the same chain. The two formulations coincide
// for a correctly built chain; here we mirror the engine's per-edge limit
// against the count of deeper edges fixed after it.
func (a *IndependentAcceptor) pathBudgetOK(limits []int) bool {
	for i, limit := range limits {
		following := len(limits) - 1 - i
		if limit < following {
			return false
		}
	}
	return true
}

// chainIntersection intersects the three-dimensional products of every edge on
// one concrete chain, including every excludedSubtrees check already baked
// into the edge covers.
func (a *IndependentAcceptor) chainIntersection(edges []edgeCover) *vSet {
	if len(edges) == 0 {
		return newVSet()
	}
	cellCount := len(a.eng.scopeU.cells)
	intervalCount := len(a.eng.timeU.bounds) - 1
	cur := fullVSet()
	for _, cov := range edges {
		cur = intersectByCover(cur, cov, cellCount, intervalCount)
		if cur.isEmpty() {
			return newVSet()
		}
	}
	return cur
}
