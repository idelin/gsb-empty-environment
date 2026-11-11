package shareplan

import "fmt"

// LeafShare is one independently identified share delivered to a leaf
// occurrence. The same participant may hold several LeafShare values; each
// has a distinct ShareID and belongs to exactly one producing node.
type LeafShare struct {
	// ShareID uniquely identifies this leaf occurrence.
	ShareID string
	// NodeID is the policy node (leaf) for which the share was produced.
	NodeID string
	// Participant is the leaf holder.
	Participant string
	// X is the public Shamir abscissa at the producing node.
	X int
	// Y is the private share value in GF(257).
	Y int
}

// NodeShare records an internal child share used for direct node-level
// combining and for exhaustive tests.
type NodeShare struct {
	// NodeID is the child node holding this share.
	NodeID string
	// X is the public abscissa at the parent node.
	X int
	// Y is the share value in GF(257).
	Y int
}

// Plan is the result of splitting a secret under a policy. It retains the
// public material needed to combine shares while never storing the secret.
type Plan struct {
	root        *Node
	idx         *Policy
	leafByID    map[string]*LeafShare
	leaves      []*LeafShare
	internal    map[string][]NodeShare // parent node ID -> child shares
	nodeSecrets map[string]int         // node ID -> node secret (root excluded)
}

// Root returns the policy root.
func (p *Plan) Root() *Node { return p.root }

// Participants returns the sorted distinct participant names.
func (p *Plan) Participants() []string { return p.idx.Participants() }

// LeafShares returns all independently identified leaf shares in DFS order.
func (p *Plan) Leaves() []*LeafShare {
	out := make([]*LeafShare, len(p.leaves))
	copy(out, p.leaves)
	return out
}

// LeafByID returns the leaf share with the given identifier.
func (p *Plan) LeafByID(shareID string) (*LeafShare, bool) {
	share, ok := p.leafByID[shareID]
	return share, ok
}

// InternalShares returns the child shares produced directly by nodeID.
func (p *Plan) InternalShares(nodeID string) []NodeShare {
	src := p.internal[nodeID]
	out := make([]NodeShare, len(src))
	copy(out, src)
	return out
}

// Threshold returns the t-of-n threshold of an internal node.
func (p *Plan) Threshold(nodeID string) (t, n int, ok bool) {
	meta, exists := p.idx.nodes[nodeID]
	if !exists || meta.leaf != nil {
		return 0, 0, false
	}
	return meta.threshold, len(meta.childIDs), true
}

// Split recursively splits secret under root over GF(257). secret must be a
// field element; byte-sized secrets 0..255 are always accepted, and 256 is
// also a valid field element. rng may be nil to use crypto/rand.
func Split(root *Node, secret int, rng interface{ Read([]byte) (int, error) }) (*Plan, error) {
	if secret < 0 || secret >= P {
		return nil, ErrInvalidSecret
	}
	idx, err := Validate(root)
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		root:        root,
		idx:         idx,
		leafByID:    make(map[string]*LeafShare),
		internal:    make(map[string][]NodeShare),
		nodeSecrets: make(map[string]int),
	}

	shareIDs := idx.leafShareIDs()

	var build func(nodeID string, nodeSecret int) error
	build = func(nodeID string, nodeSecret int) error {
		meta := idx.nodes[nodeID]
		if meta.leaf != nil {
			shareID := shareIDs[nodeID]
			leaf := &LeafShare{
				ShareID:     shareID,
				NodeID:      nodeID,
				Participant: meta.leaf.participant,
				X:           1,
				Y:           nodeSecret,
			}
			plan.leafByID[shareID] = leaf
			plan.leaves = append(plan.leaves, leaf)
			return nil
		}

		shares, err := shamirSplit(nodeSecret, meta.threshold, len(meta.childIDs), rng)
		if err != nil {
			return err
		}
		for i, childID := range meta.childIDs {
			share := NodeShare{NodeID: childID, X: shares[i].X, Y: shares[i].Y}
			plan.internal[nodeID] = append(plan.internal[nodeID], share)
			plan.nodeSecrets[childID] = share.Y
			if err := build(childID, share.Y); err != nil {
				return err
			}
		}
		return nil
	}

	if err := build("root", secret); err != nil {
		return nil, err
	}
	return plan, nil
}

// shamirSplit produces n points of a random polynomial of degree t-1 whose
// constant term is secret, using distinct nonzero abscissae 1..n.
func shamirSplit(secret, t, n int, rng interface{ Read([]byte) (int, error) }) ([]NodeShare, error) {
	coefficients := make([]int, t)
	coefficients[0] = secret
	for i := 1; i < t; i++ {
		v, err := randField(rng)
		if err != nil {
			return nil, err
		}
		coefficients[i] = v
	}

	shares := make([]NodeShare, n)
	for i := 0; i < n; i++ {
		x := i + 1
		shares[i] = NodeShare{X: x, Y: evalPoly(coefficients, x)}
	}
	return shares, nil
}

func (p *Plan) String() string {
	return fmt.Sprintf("shareplan.Plan{participants=%d, leaves=%d}", len(p.idx.participants), len(p.leaves))
}
