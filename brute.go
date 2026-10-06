package flowbounds

import "errors"

// BruteForceFeasible exhaustively enumerates every in-range flow value
// for every edge and reports whether any assignment satisfies all node
// demands. It is independent of Solve and intended for cross-checking
// small instances; the search space grows as the product of the edge
// ranges, so it returns an error once that exceeds maxCombos.
func BruteForceFeasible(n *Network, maxCombos int64) (bool, error) {
	if err := n.Validate(); err != nil {
		return false, err
	}
	combos := int64(1)
	for _, e := range n.Edges {
		combos *= e.Upper - e.Lower + 1
		if combos > maxCombos {
			return false, errors.New("flowbounds: instance too large for brute force")
		}
	}

	net := make([]int64, n.NumNodes)
	flow := make([]int64, len(n.Edges))
	var search func(i int) bool
	search = func(i int) bool {
		if i == len(n.Edges) {
			for v := 0; v < n.NumNodes; v++ {
				if net[v] != n.Demand[v] {
					return false
				}
			}
			return true
		}
		e := n.Edges[i]
		for f := e.Lower; f <= e.Upper; f++ {
			flow[i] = f
			if e.From != e.To {
				net[e.To] += f
				net[e.From] -= f
			}
			if search(i + 1) {
				return true
			}
			if e.From != e.To {
				net[e.To] -= f
				net[e.From] += f
			}
		}
		return false
	}
	return search(0), nil
}
