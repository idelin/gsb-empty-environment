package kt

// BoundaryKind distinguishes the two legal boundary roles.
type BoundaryKind uint8

const (
	// BoundaryLeaf marks an unchanged region: the boundary terminates at an
	// explicit leaf preimage present (and equal) in both epochs.
	BoundaryLeaf BoundaryKind = 1
	// BoundaryChanged marks a region modified by an authorized migration: its
	// subtree hashes in both epochs are carried explicitly and replayed
	// against the migration log during verification.
	BoundaryChanged BoundaryKind = 2
	// BoundaryEmpty marks a region that is an empty subtree in both epochs.
	// It is the minimal boundary for an untouched all-empty region.
	BoundaryEmpty BoundaryKind = 3
)

// BoundaryNode is one minimal boundary of a cross-epoch consistency proof.
type BoundaryNode struct {
	Kind   BoundaryKind
	Prefix BitPrefix

	// BoundaryLeaf fields: full leaf preimage (label equals Prefix).
	Leaf LabeledLeaf

	// BoundaryChanged fields: subtree hashes in From/To epochs.
	OldHash Hash
	NewHash Hash
}

// LabeledLeaf carries a full leaf preimage for an unchanged boundary.
type LabeledLeaf struct {
	Label BitPrefix
	Value LeafValue
}

// ConsistencyProof is an append-only consistency statement between two
// (possibly non-adjacent) epochs. Outside the disjoint minimal boundary set,
// every position in the From tree must reproduce unchanged in the To tree.
type ConsistencyProof struct {
	FromEpoch  uint32
	ToEpoch    uint32
	LabelDepth int
	FromRoot   Hash
	ToRoot     Hash
	Boundaries []BoundaryNode
}

// checkStructural validates the boundary set independent of verifier state:
//
//   - epoch ordering and root presence;
//   - every boundary is one of the two legal kinds with consistent fields;
//   - boundary prefixes are a non-overlapping antichain;
//   - the antichain is a complete cover of the label space (unique
//     reconstructability), so no region is left unproven;
//   - no redundant boundary node is present.
func (p *ConsistencyProof) checkStructural() error {
	if p.FromEpoch == 0 || p.ToEpoch == 0 || p.FromEpoch >= p.ToEpoch {
		return ErrProofEpoch
	}
	if p.LabelDepth < 0 || p.LabelDepth > MaxLabelBits {
		return ErrLabelDepth
	}
	if p.FromRoot == (Hash{}) || p.ToRoot == (Hash{}) {
		return ErrRootMismatch
	}
	if len(p.Boundaries) == 0 {
		return ErrEmptyBoundary
	}
	prefixes := make([]BitPrefix, 0, len(p.Boundaries))
	for _, b := range p.Boundaries {
		if b.Prefix.Len() > p.LabelDepth {
			return ErrBoundaryDepth
		}
		switch b.Kind {
		case BoundaryLeaf:
			if b.Prefix.Len() != p.LabelDepth || !b.Leaf.Label.Equal(b.Prefix) {
				return ErrBoundaryShape
			}
		case BoundaryChanged:
			if b.OldHash == (Hash{}) || b.NewHash == (Hash{}) {
				return ErrBoundaryShape
			}
		case BoundaryEmpty:
			// No extra fields; position and role fully determine the hash.
		default:
			return ErrBoundaryKind
		}
		prefixes = append(prefixes, b.Prefix)
	}
	if !Disjoint(prefixes) {
		// An ancestor/descendant pair means either overlap (invalid) or a
		// redundant node (the descendant is covered by the ancestor).
		return ErrRedundantBoundary
	}
	if err := checkCover(prefixes, p.LabelDepth); err != nil {
		return err
	}
	return nil
}

// checkCover walks the label space against the boundary prefix set. Empty
// regions are fully covered by a single position-bound empty subtree, so the
// only requirement is that every non-empty leaf position has a boundary.
func checkCover(prefixes []BitPrefix, depth int) error {
	set := map[string]struct{}{}
	for _, p := range prefixes {
		set[p.String()] = struct{}{}
	}
	var walk func(prefix BitPrefix) error
	walk = func(prefix BitPrefix) error {
		if _, ok := set[prefix.String()]; ok {
			return nil
		}
		if prefix.Len() == depth {
			return ErrIncompleteCover
		}
		p0, err := prefix.AppendBit(0)
		if err != nil {
			return err
		}
		p1, err := prefix.AppendBit(1)
		if err != nil {
			return err
		}
		if err := walk(p0); err != nil {
			return err
		}
		return walk(p1)
	}
	return walk(EmptyPrefix())
}

// reconstructRoot rebuilds a tree root from the boundary set for the given
// epoch side. For BoundaryLeaf nodes the leaf hash is recomputed from the
// preimage; for BoundaryChanged nodes hashes come from the proof (and are
// replayed against migrations separately).
func (p *ConsistencyProof) reconstructRoot(epoch uint32, useNew bool) (Hash, error) {
	byKey := make(map[string]BoundaryNode, len(p.Boundaries))
	for _, b := range p.Boundaries {
		byKey[b.Prefix.String()] = b
	}
	hashAt := func(b BoundaryNode) Hash {
		switch b.Kind {
		case BoundaryLeaf:
			return LeafHash(epoch, b.Leaf.Label, b.Leaf.Value.Commitment())
		case BoundaryEmpty:
			return recomputeEmpty(epoch, p.LabelDepth, b.Prefix)
		}
		if useNew {
			return b.NewHash
		}
		return b.OldHash
	}
	var rec func(prefix BitPrefix, depth int) (Hash, error)
	rec = func(prefix BitPrefix, depth int) (Hash, error) {
		if b, ok := byKey[prefix.String()]; ok {
			return hashAt(b), nil
		}
		p0, err := prefix.AppendBit(0)
		if err != nil {
			return Hash{}, err
		}
		p1, err := prefix.AppendBit(1)
		if err != nil {
			return Hash{}, err
		}
		if depth == p.LabelDepth {
			// A boundary was expected at this leaf but is absent.
			return Hash{}, ErrIncompleteCover
		}
		lh, err := rec(p0, depth+1)
		if err != nil {
			return Hash{}, err
		}
		rh, err := rec(p1, depth+1)
		if err != nil {
			return Hash{}, err
		}
		// Height is the number of levels beneath this node.
		return InteriorHash(epoch, p.LabelDepth-depth, prefix, lh, rh), nil
	}
	return rec(EmptyPrefix(), 0)
}

var (
	// ErrProofEpoch indicates invalid proof epoch ordering.
	ErrProofEpoch = prefixError("kt: consistency proof epoch ordering invalid")
	// ErrEmptyBoundary indicates an empty boundary set.
	ErrEmptyBoundary = prefixError("kt: consistency proof has no boundaries")
	// ErrBoundaryDepth indicates a boundary deeper than label depth.
	ErrBoundaryDepth = prefixError("kt: boundary prefix too deep")
	// ErrBoundaryShape indicates malformed boundary fields.
	ErrBoundaryShape = prefixError("kt: malformed boundary node shape")
	// ErrBoundaryKind indicates an unknown boundary kind.
	ErrBoundaryKind = prefixError("kt: unknown boundary kind")
	// ErrRedundantBoundary indicates overlapping/redundant boundary nodes.
	ErrRedundantBoundary = prefixError("kt: redundant boundary node rejected")
	// ErrIncompleteCover indicates boundaries do not cover the label space.
	ErrIncompleteCover = prefixError("kt: boundary set does not cover label space")
)
