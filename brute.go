package flowsolve

import "errors"

// ErrTooLarge is returned by BruteForce when the enumeration space exceeds
// the supplied budget.
var ErrTooLarge = errors.New("flowsolve: enumeration space too large")

// BruteForce decides feasibility by exhaustively enumerating every flow
// within the edge bounds. It is intended for cross-checking Solve on small
// instances; maxCombos bounds the total number of combinations examined.
func BruteForce(n *Network, maxCombos int64) (bool, error) {
	if err := checkInput(n); err != nil {
		return false, err
	}
	combos := int64(1)
	for _, e := range n.Edges {
		combos *= e.Hi - e.Lo + 1
		if combos > maxCombos {
			return false, ErrTooLarge
		}
	}
	flow := make([]int64, len(n.Edges))
	return enumerate(n, 0, flow), nil
}

func checkInput(n *Network) error {
	if n.NumNodes < 0 || len(n.Demand) != n.NumNodes {
		return ErrBadNode
	}
	var sum int64
	for _, d := range n.Demand {
		sum += d
	}
	if sum != 0 {
		return ErrDemandSum
	}
	for _, e := range n.Edges {
		if e.From < 0 || e.From >= n.NumNodes || e.To < 0 || e.To >= n.NumNodes {
			return ErrBadNode
		}
		if e.Lo > e.Hi {
			return ErrBadBounds
		}
	}
	return nil
}

func enumerate(n *Network, idx int, flow []int64) bool {
	if idx == len(n.Edges) {
		return VerifyFlow(n, flow) == nil
	}
	e := n.Edges[idx]
	for f := e.Lo; f <= e.Hi; f++ {
		flow[idx] = f
		if enumerate(n, idx+1, flow) {
			return true
		}
	}
	return false
}
