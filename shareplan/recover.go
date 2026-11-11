package shareplan

import "errors"

var (
	// ErrNoShares is returned when reconstruction is attempted with no
	// shares.
	ErrNoShares = errors.New("shareplan: no shares presented")
	// ErrUnknownShare is returned when a share ID is not part of the plan.
	ErrUnknownShare = errors.New("shareplan: unknown share id")
	// ErrDuplicateShare is returned when the same share is presented twice.
	ErrDuplicateShare = errors.New("shareplan: duplicate share id")
	// ErrMixedNodes is returned when a node-level combination uses shares
	// produced for different policy nodes.
	ErrMixedNodes = errors.New("shareplan: shares from different nodes cannot be mixed")
	// ErrDuplicateX is returned when two shares at one node reuse an
	// abscissa.
	ErrDuplicateX = errors.New("shareplan: duplicate share abscissa at node")
	// ErrNotEnoughShares is returned when fewer than threshold shares are
	// available for a node.
	ErrNotEnoughShares = errors.New("shareplan: not enough shares for node threshold")
	// ErrNodeNotInternal is returned when Combine targets a leaf or an
	// unknown node.
	ErrNodeNotInternal = errors.New("shareplan: combine target is not an internal node")
)

// LagrangeShare is one point used in node-level reconstruction together with
// the Lagrange coefficient that turns its value into a contribution toward
// f(0).
type LagrangeShare struct {
	NodeID string
	X      int
	Y      int
	// Coeff satisfies secret = sum(Coeff_i * Y_i) in GF(257).
	Coeff int
}

// Combine reconstructs the secret of nodeID from the child shares produced
// directly by that node. Every presented share must belong to nodeID:
// combining shares from different nodes is rejected with ErrMixedNodes. At
// least the node threshold of distinct shares is required.
func (p *Plan) Combine(nodeID string, shares []NodeShare) (secret int, lagrange []LagrangeShare, err error) {
	meta, ok := p.idx.nodes[nodeID]
	if !ok || meta.leaf != nil {
		return 0, nil, ErrNodeNotInternal
	}
	if len(shares) == 0 {
		return 0, nil, ErrNoShares
	}

	expectedX := make(map[string]int, len(meta.childIDs))
	for _, share := range p.internal[nodeID] {
		expectedX[share.NodeID] = share.X
	}

	seenChild := make(map[string]struct{}, len(shares))
	seenX := make(map[int]struct{}, len(shares))
	points := make([]point, 0, len(shares))
	for _, share := range shares {
		wantX, known := expectedX[share.NodeID]
		if !known {
			if _, otherNode := p.idx.nodes[share.NodeID]; otherNode {
				return 0, nil, ErrMixedNodes
			}
			return 0, nil, ErrUnknownShare
		}
		if _, dup := seenChild[share.NodeID]; dup {
			return 0, nil, ErrDuplicateShare
		}
		seenChild[share.NodeID] = struct{}{}
		if share.Y < 0 || share.Y >= P {
			return 0, nil, ErrInvalidValue
		}
		if _, dup := seenX[share.X]; dup {
			return 0, nil, ErrDuplicateX
		}
		if share.X != wantX {
			return 0, nil, ErrUnknownShare
		}
		seenX[share.X] = struct{}{}
		points = append(points, point{x: share.X, y: share.Y})
	}

	if len(points) < meta.threshold {
		return 0, nil, ErrNotEnoughShares
	}

	secret, coeffs := interpolateAtZero(points[:meta.threshold])
	lagrange = make([]LagrangeShare, meta.threshold)
	for i := range lagrange {
		lagrange[i] = LagrangeShare{
			NodeID: shares[i].NodeID,
			X:      points[i].x,
			Y:      points[i].y,
			Coeff:  coeffs[i],
		}
	}
	return secret, lagrange, nil
}

// Recover reconstructs the root secret from independently identified leaf
// shares. Shares are propagated bottom-up and only shares produced for the
// same node are ever combined; an unknown, duplicated or out-of-range share
// aborts recovery. The presented set succeeds exactly when its participants
// satisfy the policy.
func (p *Plan) Recover(leafShares []*LeafShare) (int, error) {
	if len(leafShares) == 0 {
		return 0, ErrNoShares
	}

	presented := make(map[string]*LeafShare, len(leafShares))
	for _, share := range leafShares {
		if share == nil {
			return 0, ErrUnknownShare
		}
		known, ok := p.leafByID[share.ShareID]
		if !ok || known.NodeID != share.NodeID {
			return 0, ErrUnknownShare
		}
		if share.Y < 0 || share.Y >= P {
			return 0, ErrInvalidValue
		}
		if _, dup := presented[share.ShareID]; dup {
			return 0, ErrDuplicateShare
		}
		presented[share.ShareID] = share
	}

	leafIDs := p.idx.leafShareIDs()
	var walk func(nodeID string) (int, bool)
	walk = func(nodeID string) (int, bool) {
		meta := p.idx.nodes[nodeID]
		if meta.leaf != nil {
			shareID := leafIDs[nodeID]
			share, ok := presented[shareID]
			if !ok {
				return 0, false
			}
			return share.Y, true
		}

		points := make([]point, 0, meta.threshold)
		for _, childID := range meta.childIDs {
			y, ok := walk(childID)
			if !ok {
				continue
			}
			x := childX(p, nodeID, childID)
			points = append(points, point{x: x, y: y})
			if len(points) == meta.threshold {
				break
			}
		}
		if len(points) < meta.threshold {
			return 0, false
		}
		secret, _ := interpolateAtZero(points)
		return secret, true
	}

	secret, ok := walk("root")
	if !ok {
		return 0, ErrNotEnoughShares
	}
	return secret, nil
}

// childX returns the abscissa assigned to childID by its parent node.
func childX(p *Plan, parentID, childID string) int {
	for _, share := range p.internal[parentID] {
		if share.NodeID == childID {
			return share.X
		}
	}
	panic("shareplan: internal index missing child share")
}

type point struct{ x, y int }

// interpolateAtZero returns f(0) and the per-point Lagrange coefficients
// such that f(0) = sum(coeff[i] * points[i].y) over GF(257).
func interpolateAtZero(points []point) (int, []int) {
	coeffs := make([]int, len(points))
	secret := 0
	for i, pi := range points {
		num := 1
		den := 1
		for j, pj := range points {
			if i == j {
				continue
			}
			num = mul(num, sub(0, pj.x))
			den = mul(den, sub(pi.x, pj.x))
		}
		coeffs[i] = mul(num, inverse(den))
		secret = add(secret, mul(coeffs[i], pi.y))
	}
	return secret, coeffs
}
