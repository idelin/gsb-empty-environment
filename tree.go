package treekem

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
)

// LeafNode is the public content of a non-blank leaf.
type LeafNode struct {
	Position      int
	ID            string
	EncryptionKey []byte
	SignatureKey  []byte
	ParentHash    []byte
	Signature     []byte
}

func (l *LeafNode) clone() *LeafNode {
	if l == nil {
		return nil
	}
	return &LeafNode{
		Position:      l.Position,
		ID:            l.ID,
		EncryptionKey: append([]byte(nil), l.EncryptionKey...),
		SignatureKey:  append([]byte(nil), l.SignatureKey...),
		ParentHash:    append([]byte(nil), l.ParentHash...),
		Signature:     append([]byte(nil), l.Signature...),
	}
}

// ParentNode is a non-blank TreeKEM parent public key and its RFC parent-hash
// state. OriginalChildResolution records the copath resolution bound into the
// parent hash by this model.
type ParentNode struct {
	EncryptionKey           []byte
	ParentHash              []byte
	UnmergedLeaves          []int
	OriginalChildResolution []Index
}

func (p *ParentNode) clone() *ParentNode {
	if p == nil {
		return nil
	}
	return &ParentNode{
		EncryptionKey:           append([]byte(nil), p.EncryptionKey...),
		ParentHash:              append([]byte(nil), p.ParentHash...),
		UnmergedLeaves:          append([]int(nil), p.UnmergedLeaves...),
		OriginalChildResolution: append([]Index(nil), p.OriginalChildResolution...),
	}
}

// TreeNode is either blank or contains exactly one of Leaf/Parent.
type TreeNode struct {
	Index  Index
	Leaf   *LeafNode
	Parent *ParentNode
}

func (n *TreeNode) Clone() *TreeNode {
	if n == nil {
		return nil
	}
	return &TreeNode{Index: n.Index, Leaf: n.Leaf.clone(), Parent: n.Parent.clone()}
}

// Blank reports whether the node has no public key material.
func (n *TreeNode) Blank() bool {
	return n == nil || (n.Leaf == nil && n.Parent == nil)
}

// RatchetTree is a compact RFC 9420 left-balanced ratchet tree. LeafCount is
// the high-water leaf slot count; removed slots remain blank.
type RatchetTree struct {
	LeafCount int
	Nodes     []*TreeNode
}

// Tree is the conventional alias for RatchetTree.
type Tree = RatchetTree

// Node is an alias for the tree's node representation.
type Node = TreeNode

// NewRatchetTree allocates an all-blank compact tree.
func NewRatchetTree(leafCount int) (*RatchetTree, error) {
	if leafCount <= 0 {
		return nil, ErrInvalidLeafCount
	}
	return &RatchetTree{LeafCount: leafCount, Nodes: make([]*TreeNode, Width(leafCount))}, nil
}

// NewSingleLeafTree creates a one-leaf tree.
func NewSingleLeafTree(id PublicIdentity) (*RatchetTree, error) {
	tree, err := NewRatchetTree(1)
	if err != nil {
		return nil, err
	}
	tree.Nodes[0] = &TreeNode{
		Index: 0,
		Leaf: &LeafNode{
			Position:      0,
			ID:            id.ID,
			EncryptionKey: append([]byte(nil), id.EncryptionKey...),
			SignatureKey:  append([]byte(nil), id.SignatureKey...),
		},
	}
	return tree, nil
}

// Clone performs a deep copy so callers can trial-apply commits.
func (t *RatchetTree) Clone() *RatchetTree {
	if t == nil {
		return nil
	}
	clone := &RatchetTree{LeafCount: t.LeafCount, Nodes: make([]*TreeNode, len(t.Nodes))}
	for i, node := range t.Nodes {
		clone.Nodes[i] = node.Clone()
	}
	return clone
}

// Width returns the compact node count.
func (t *RatchetTree) Width() int { return Width(t.LeafCount) }

// Root returns the root index.
func (t *RatchetTree) Root() Index { return RootIndex(t.LeafCount) }

// ValidIndex reports whether x is allocated in this tree.
func (t *RatchetTree) ValidIndex(x Index) bool { return ValidIndex(x, t.LeafCount) }

func (t *RatchetTree) node(x Index) (*TreeNode, error) {
	if !t.ValidIndex(x) || int(x) >= len(t.Nodes) {
		return nil, fmt.Errorf("%w: node %d", ErrInvalidIndex, x)
	}
	return t.Nodes[x], nil
}

// IsBlank reports whether x is absent or blank.
func (t *RatchetTree) IsBlank(x Index) (bool, error) {
	node, err := t.node(x)
	if err != nil {
		return true, err
	}
	return node.Blank(), nil
}

// Left returns the bounded left child.
func (t *RatchetTree) Left(x Index) (Index, error) { return LeftBounded(x, t.LeafCount) }

// Right returns the bounded right child.
func (t *RatchetTree) Right(x Index) (Index, error) { return RightBounded(x, t.LeafCount) }

// Parent returns the bounded actual parent.
func (t *RatchetTree) Parent(x Index) (Index, error) { return ParentBounded(x, t.LeafCount) }

// DirectPath returns the bounded direct path.
func (t *RatchetTree) DirectPath(leafIndex Index) ([]Index, error) {
	return DirectPath(leafIndex, t.LeafCount)
}

// Copath returns the bounded copath.
func (t *RatchetTree) Copath(leafIndex Index) ([]Index, error) {
	return Copath(leafIndex, t.LeafCount)
}

// PublicKeyAt returns the current X25519 public key at a leaf or parent node.
func (t *RatchetTree) PublicKeyAt(x Index) ([]byte, error) {
	node, err := t.node(x)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, fmt.Errorf("treekem: node %d is blank", x)
	}
	switch {
	case node.Leaf != nil:
		return append([]byte(nil), node.Leaf.EncryptionKey...), nil
	case node.Parent != nil:
		return append([]byte(nil), node.Parent.EncryptionKey...), nil
	default:
		return nil, fmt.Errorf("treekem: node %d has no public key", x)
	}
}

// Resolution implements RFC 9420 resolution with precise unmerged_leaves
// handling. Blank leaves resolve to nothing; blank parents concatenate child
// resolutions; a non-blank parent resolves to itself plus every non-blank
// direct-path node below it for each unmerged leaf.
func (t *RatchetTree) Resolution(x Index) ([]Index, error) {
	if !t.ValidIndex(x) {
		return nil, fmt.Errorf("%w: resolution(%d)", ErrInvalidIndex, x)
	}
	node := t.Nodes[x]
	if IsLeaf(x) {
		if node.Blank() {
			return []Index{}, nil
		}
		if node.Leaf == nil {
			return nil, fmt.Errorf("treekem: even index %d is not a leaf", x)
		}
		return []Index{x}, nil
	}

	if node.Blank() {
		leftChild, err := t.Left(x)
		if err != nil {
			return nil, err
		}
		rightChild, err := t.Right(x)
		if err != nil {
			return nil, err
		}
		leftResolution, err := t.Resolution(leftChild)
		if err != nil {
			return nil, err
		}
		rightResolution, err := t.Resolution(rightChild)
		if err != nil {
			return nil, err
		}
		return append(leftResolution, rightResolution...), nil
	}

	if node.Parent == nil {
		return nil, fmt.Errorf("treekem: odd index %d is not a parent", x)
	}
	result := []Index{x}
	if len(node.Parent.UnmergedLeaves) == 0 {
		return result, nil
	}

	unmerged := append([]int(nil), node.Parent.UnmergedLeaves...)
	if !sort.IntsAreSorted(unmerged) {
		sort.Ints(unmerged)
	}
	for _, leafPosition := range unmerged {
		if leafPosition < 0 || leafPosition >= t.LeafCount {
			return nil, fmt.Errorf("treekem: unmerged leaf %d outside tree", leafPosition)
		}
		leafIndex := Leaf(leafPosition)
		path, err := t.DirectPath(leafIndex)
		if err != nil {
			return nil, err
		}
		parentPosition := indexOf(path, x)
		if parentPosition < 0 {
			return nil, fmt.Errorf("treekem: unmerged leaf %d is not below parent %d", leafPosition, x)
		}
		for _, candidate := range path[:parentPosition] {
			rawCandidate, err := t.node(candidate)
			if err != nil {
				return nil, err
			}
			if rawCandidate.Blank() {
				continue
			}
			if !containsIndex(result, candidate) {
				result = append(result, candidate)
			}
		}
	}
	return result, nil
}

// Resolution is a package-level alias for tree.Resolution.
func Resolution(tree *RatchetTree, x Index) ([]Index, error) { return tree.Resolution(x) }

func resolution(tree *RatchetTree, x Index) ([]Index, error) { return tree.Resolution(x) }

func (t *RatchetTree) resolution(x Index) ([]Index, error) { return t.Resolution(x) }

// ActiveLeavesUnder returns active leaf positions descended from x.
func (t *RatchetTree) ActiveLeavesUnder(x Index) ([]int, error) {
	if !t.ValidIndex(x) {
		return nil, fmt.Errorf("%w: leaves under %d", ErrInvalidIndex, x)
	}
	leaves := []int{}
	if err := t.appendActiveLeaves(x, &leaves); err != nil {
		return nil, err
	}
	sort.Ints(leaves)
	return leaves, nil
}

func (t *RatchetTree) appendActiveLeaves(x Index, leaves *[]int) error {
	if IsLeaf(x) {
		node, err := t.node(x)
		if err != nil {
			return err
		}
		if !node.Blank() && node.Leaf != nil {
			*leaves = append(*leaves, node.Leaf.Position)
		}
		return nil
	}
	leftChild, err := t.Left(x)
	if err != nil {
		return err
	}
	rightChild, err := t.Right(x)
	if err != nil {
		return err
	}
	if err := t.appendActiveLeaves(leftChild, leaves); err != nil {
		return err
	}
	return t.appendActiveLeaves(rightChild, leaves)
}

// ActiveLeafPositions returns all non-blank leaf positions.
func (t *RatchetTree) ActiveLeafPositions() []int {
	out := []int{}
	for i := 0; i < t.LeafCount; i++ {
		x := Leaf(i)
		if int(x) >= len(t.Nodes) {
			continue
		}
		node := t.Nodes[x]
		if !node.Blank() && node.Leaf != nil {
			out = append(out, i)
		}
	}
	return out
}

// MemberAt returns the member ID at a leaf position.
func (t *RatchetTree) MemberAt(position int) (string, bool) {
	if position < 0 || position >= t.LeafCount {
		return "", false
	}
	x := Leaf(position)
	if int(x) >= len(t.Nodes) || t.Nodes[x] == nil || t.Nodes[x].Leaf == nil {
		return "", false
	}
	return t.Nodes[x].Leaf.ID, true
}

// ValidateShape checks compact-tree lengths and node/parity invariants.
func (t *RatchetTree) ValidateShape() error {
	if t == nil {
		return errors.New("treekem: nil tree")
	}
	if t.LeafCount <= 0 {
		return ErrInvalidLeafCount
	}
	if len(t.Nodes) != Width(t.LeafCount) {
		return fmt.Errorf("treekem: nodes length %d != width %d", len(t.Nodes), Width(t.LeafCount))
	}
	for x, node := range t.Nodes {
		if node == nil {
			continue
		}
		if node.Index != Index(x) {
			return fmt.Errorf("treekem: node %d has stored index %d", x, node.Index)
		}
		if node.Leaf != nil && node.Parent != nil {
			return fmt.Errorf("treekem: node %d is both leaf and parent", x)
		}
		if IsLeaf(Index(x)) {
			if node.Parent != nil {
				return fmt.Errorf("treekem: parent at even leaf index %d", x)
			}
			if node.Leaf != nil {
				if node.Leaf.Position != x/2 {
					return fmt.Errorf("treekem: leaf %d stores position %d", x, node.Leaf.Position)
				}
				if len(node.Leaf.EncryptionKey) != 32 {
					return fmt.Errorf("treekem: leaf %d has %d-byte encryption key", x, len(node.Leaf.EncryptionKey))
				}
			}
		} else {
			if node.Leaf != nil {
				return fmt.Errorf("treekem: leaf at odd parent index %d", x)
			}
			if node.Parent != nil && len(node.Parent.EncryptionKey) != 32 {
				return fmt.Errorf("treekem: parent %d has %d-byte key", x, len(node.Parent.EncryptionKey))
			}
		}
	}
	return nil
}

// Hash returns a deterministic domain-separated hash of the complete public
// tree, including blank positions and unmerged_leaves.
func (t *RatchetTree) Hash() []byte {
	hb := &hashBuilder{}
	hb.uint32(uint32(t.LeafCount))
	for x, node := range t.Nodes {
		hb.uint32(uint32(x))
		if node == nil || node.Blank() {
			hb.byte(0)
			continue
		}
		if node.Leaf != nil {
			hb.byte(1)
			hb.uint32(uint32(node.Leaf.Position))
			hb.bytes([]byte(node.Leaf.ID))
			hb.bytes(node.Leaf.EncryptionKey)
			hb.bytes(node.Leaf.SignatureKey)
			hb.bytes(node.Leaf.ParentHash)
			hb.bytes(node.Leaf.Signature)
			continue
		}
		hb.byte(2)
		hb.bytes(node.Parent.EncryptionKey)
		hb.bytes(node.Parent.ParentHash)
		hb.positions(node.Parent.UnmergedLeaves)
		hb.indexes(node.Parent.OriginalChildResolution)
	}
	return hb.sum("treekem-ratchet-tree-v1")
}

// Equal reports whether public tree hashes are equal.
func (t *RatchetTree) Equal(other *RatchetTree) bool {
	if t == nil || other == nil {
		return t == other
	}
	return bytes.Equal(t.Hash(), other.Hash())
}

func indexOf(values []Index, target Index) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}

func containsIndex(values []Index, target Index) bool {
	return indexOf(values, target) >= 0
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
