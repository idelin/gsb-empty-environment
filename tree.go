package kt

import (
	"crypto/sha256"
	"sort"
)

// LeafValue is the value stored under a label.
//
//   - PublicKey is the directory entry whose transparency is being logged.
//   - Revoked marks an entry as abolished. A revoked entry must never silently
//     reappear (see the package-level resurrection guard).
//   - RevocCounter is the directory-wide monotonic revocation counter value at
//     the time the leaf was last written; it is 0 for an unrevoked initial
//     issuance and strictly grows whenever revocations occur.
type LeafValue struct {
	PublicKey    []byte
	Revoked      bool
	RevocCounter uint64
}

// Commitment hashes a leaf value into a position-independent commitment that
// is embedded into LeafHash.
func (v LeafValue) Commitment() Hash {
	flag := byte(0)
	if v.Revoked {
		flag = 1
	}
	return taggedHash(domainLeaf, v.PublicKey, []byte{flag}, u64Bytes(v.RevocCounter))
}

// Equal compares two leaf values.
func (v LeafValue) Equal(o LeafValue) bool {
	if v.Revoked != o.Revoked || v.RevocCounter != o.RevocCounter {
		return false
	}
	if len(v.PublicKey) != len(o.PublicKey) {
		return false
	}
	for i := range v.PublicKey {
		if v.PublicKey[i] != o.PublicKey[i] {
			return false
		}
	}
	return true
}

// nodeKind enumerates the three structural node roles.
type nodeKind uint8

const (
	kindEmpty nodeKind = iota
	kindLeaf
	kindInterior
)

// node is an in-memory binary Merkle prefix-tree node.
type node struct {
	kind   nodeKind
	hash   Hash
	height int
	prefix BitPrefix

	// leaf fields
	label BitPrefix
	value LeafValue

	// interior fields
	children [2]*node
}

// Tree is an immutable binary Merkle prefix tree bound to a single epoch.
// Labels have a uniform fixed depth (in bits), making the "fully expanded"
// reference construction explicit: every root-to-leaf path has exactly
// LabelDepth bits, and all other positions are empty nodes.
type Tree struct {
	epoch      uint32
	labelDepth int
	leaves     map[string]LeafValue // key = label.String()
	root       *node
}

// Entry is one labeled leaf for tree construction.
type Entry struct {
	Label BitPrefix
	Value LeafValue
}

// BuildTree constructs the fully expanded prefix tree for one epoch. All
// labels must have exactly labelDepth bits (<= 256) and must be unique.
func BuildTree(epoch uint32, labelDepth int, entries []Entry) (*Tree, error) {
	if labelDepth < 0 || labelDepth > MaxLabelBits {
		return nil, ErrLabelTooLong
	}
	t := &Tree{
		epoch:      epoch,
		labelDepth: labelDepth,
		leaves:     make(map[string]LeafValue, len(entries)),
	}
	list := make([]leafKV, 0, len(entries))
	for _, e := range entries {
		label, value := e.Label, e.Value
		if label.Len() != labelDepth {
			return nil, ErrLabelDepth
		}
		key := label.String()
		if _, dup := t.leaves[key]; dup {
			return nil, ErrDuplicateLabel
		}
		t.leaves[key] = value
		list = append(list, leafKV{label, value})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].label.Compare(list[j].label) < 0 })
	t.root = t.build(EmptyPrefix(), 0, list)
	return t, nil
}

type leafKV struct {
	label BitPrefix
	value LeafValue
}

// build recursively materializes the minimal unique subtree for the sorted
// leaf set, padding every branch with empty nodes out to labelDepth.
func (t *Tree) build(prefix BitPrefix, depth int, entries []leafKV) *node {
	if len(entries) == 0 {
		// Build the fully expanded empty subtree so navigation and inclusion
		// paths never encounter a nil interior node.
		return t.buildEmpty(prefix, depth)
	}
	if depth == t.labelDepth {
		e := entries[0]
		return &node{
			kind:   kindLeaf,
			height: 0,
			prefix: prefix,
			label:  e.label,
			value:  e.value,
			hash:   LeafHash(t.epoch, e.label, e.value.Commitment()),
		}
	}
	var left, right []leafKV
	for _, e := range entries {
		if e.label.BitAt(depth) == 0 {
			left = append(left, e)
		} else {
			right = append(right, e)
		}
	}
	p0, _ := prefix.AppendBit(0)
	p1, _ := prefix.AppendBit(1)
	ln := t.build(p0, depth+1, left)
	rn := t.build(p1, depth+1, right)
	h := InteriorHash(t.epoch, t.labelDepth-depth, prefix, ln.hash, rn.hash)
	return &node{
		kind:     kindInterior,
		height:   t.labelDepth - depth,
		prefix:   prefix,
		hash:     h,
		children: [2]*node{ln, rn},
	}
}

// buildEmpty materializes a fully expanded all-empty subtree.
func (t *Tree) buildEmpty(prefix BitPrefix, depth int) *node {
	if depth == t.labelDepth {
		return t.emptyNode(prefix, depth)
	}
	p0, _ := prefix.AppendBit(0)
	p1, _ := prefix.AppendBit(1)
	ln := t.buildEmpty(p0, depth+1)
	rn := t.buildEmpty(p1, depth+1)
	return &node{
		kind:     kindInterior,
		height:   t.labelDepth - depth,
		prefix:   prefix,
		hash:     InteriorHash(t.epoch, t.labelDepth-depth, prefix, ln.hash, rn.hash),
		children: [2]*node{ln, rn},
	}
}

func (t *Tree) emptyNode(prefix BitPrefix, depth int) *node {
	return &node{
		kind:   kindEmpty,
		height: t.labelDepth - depth,
		prefix: prefix,
		hash:   EmptyHash(t.epoch, t.labelDepth-depth, prefix),
	}
}

// Epoch returns the epoch number the tree is bound to.
func (t *Tree) Epoch() uint32 { return t.epoch }

// LabelDepth returns the fixed label depth in bits.
func (t *Tree) LabelDepth() int { return t.labelDepth }

// RootHash returns the signed-input tree root hash.
func (t *Tree) RootHash() Hash {
	return t.root.hash
}

// Lookup returns the leaf value under label and whether it exists.
func (t *Tree) Lookup(label BitPrefix) (LeafValue, bool) {
	v, ok := t.leaves[label.String()]
	return v, ok
}

// InclusionStep is one level of a Merkle inclusion/non-inclusion path: the
// hash of the sibling and the bit selecting the node's own child position.
type InclusionStep struct {
	SiblingHash Hash
	BitIsOne    bool
}

// InclusionProof proves that Leaf (when Present) or the absence of Label is
// committed under RootHash of the tree.
type InclusionProof struct {
	Epoch      uint32
	Label      BitPrefix
	Present    bool
	Leaf       LeafValue
	Steps      []InclusionStep
	Root       Hash
	LabelDepth int
}

// ProveInclusion builds an inclusion or non-inclusion proof for label.
func (t *Tree) ProveInclusion(label BitPrefix) (*InclusionProof, error) {
	if label.Len() != t.labelDepth {
		return nil, ErrLabelDepth
	}
	proof := &InclusionProof{
		Epoch:      t.epoch,
		Label:      label,
		Root:       t.RootHash(),
		LabelDepth: t.labelDepth,
	}
	if v, ok := t.leaves[label.String()]; ok {
		proof.Present = true
		proof.Leaf = v
	}
	cur := t.root
	for depth := 0; depth < t.labelDepth; depth++ {
		bit := label.BitAt(depth)
		sib := cur.children[1-bit]
		proof.Steps = append(proof.Steps, InclusionStep{
			SiblingHash: sib.hash,
			BitIsOne:    bit == 1,
		})
		cur = cur.children[bit]
	}
	if proof.Present {
		if cur.kind != kindLeaf || !cur.label.Equal(label) {
			return nil, ErrInternal
		}
	} else if cur.kind != kindEmpty {
		return nil, ErrInternal
	}
	return proof, nil
}

// Verify recomputes the root from the proof and checks it against Root.
func (p *InclusionProof) Verify() error {
	if p.Label.Len() != p.LabelDepth || len(p.Steps) != p.LabelDepth {
		return ErrBadInclusionProof
	}
	var h Hash
	if p.Present {
		h = LeafHash(p.Epoch, p.Label, p.Leaf.Commitment())
	} else {
		h = EmptyHash(p.Epoch, 0, p.Label)
	}
	prefix := p.Label
	for i := len(p.Steps) - 1; i >= 0; i-- {
		parent, err := prefix.Parent()
		if err != nil {
			return err
		}
		step := p.Steps[i]
		left, right := h, step.SiblingHash
		if step.BitIsOne {
			left, right = step.SiblingHash, h
		}
		h = InteriorHash(p.Epoch, p.LabelDepth-parent.Len(), parent, left, right)
		prefix = parent
	}
	if h != p.Root {
		return ErrRootMismatch
	}
	return nil
}

// SubtreeHash returns the hash of the node at prefix, together with its
// height. It is used by migration verification and proof generation.
func (t *Tree) SubtreeHash(prefix BitPrefix) (Hash, int, error) {
	if prefix.Len() > t.labelDepth {
		return Hash{}, 0, ErrLabelDepth
	}
	if prefix.Len() == 0 {
		return t.root.hash, t.root.height, nil
	}
	cur := t.root
	for depth := 0; depth < prefix.Len(); depth++ {
		cur = cur.children[prefix.BitAt(depth)]
		if cur == nil {
			return Hash{}, 0, ErrNotInterior
		}
	}
	return cur.hash, cur.height, nil
}

// sum256 is a small helper used by tests.
func sum256(b []byte) Hash { return sha256.Sum256(b) }

// recomputeEmpty returns the root hash of the fully expanded empty subtree
// rooted at prefix in a tree of the given epoch and total label depth. It
// mirrors Tree.buildEmpty without requiring a tree instance.
func recomputeEmpty(epoch uint32, labelDepth int, prefix BitPrefix) Hash {
	var rec func(p BitPrefix, depth int) Hash
	rec = func(p BitPrefix, depth int) Hash {
		if depth == labelDepth {
			return EmptyHash(epoch, 0, p)
		}
		p0, _ := p.AppendBit(0)
		p1, _ := p.AppendBit(1)
		lh := rec(p0, depth+1)
		rh := rec(p1, depth+1)
		return InteriorHash(epoch, labelDepth-depth, p, lh, rh)
	}
	return rec(prefix, prefix.Len())
}

var (
	// ErrLabelDepth indicates a label of the wrong fixed depth.
	ErrLabelDepth = prefixError("kt: label has wrong fixed depth")
	// ErrDuplicateLabel indicates duplicate labels in a tree build.
	ErrDuplicateLabel = prefixError("kt: duplicate label in tree")
	// ErrBadInclusionProof indicates a structurally invalid inclusion proof.
	ErrBadInclusionProof = prefixError("kt: malformed inclusion proof")
	// ErrRootMismatch indicates a recomputed root does not match.
	ErrRootMismatch = prefixError("kt: root hash mismatch")
	// ErrNotInterior indicates navigation through a non-interior node.
	ErrNotInterior = prefixError("kt: expected interior node")
	// ErrInternal indicates an internal invariant failure.
	ErrInternal = prefixError("kt: internal invariant failure")
)

type prefixError string

func (e prefixError) Error() string { return string(e) }
