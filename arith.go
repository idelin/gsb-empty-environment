package treekem

import (
	"errors"
	"fmt"
	"math/bits"
)

// Index is a node index in the compact RFC 9420 left-balanced ratchet tree.
// Leaves occupy even indexes and parents occupy odd indexes.
type Index int

var (
	ErrInvalidLeafCount = errors.New("treekem: leaf count must be positive")
	ErrInvalidIndex     = errors.New("treekem: invalid tree index")
	ErrNotParent        = errors.New("treekem: leaf has no children")
	ErrRootHasNoParent  = errors.New("treekem: root has no parent")
)

// Leaf returns node index 2*position for leaf position/index position.
// It is the exported form of RFC 9420 Appendix C's leaf(index).
func Leaf(position int) Index {
	if position < 0 {
		panic("treekem: negative leaf position")
	}
	return Index(position << 1)
}

// NodeIndexAt returns the index of the node at the given level and position.
// Level zero is a leaf. It is the exported node(level, index) arithmetic.
func NodeIndexAt(level, position int) Index {
	if level < 0 || position < 0 {
		panic("treekem: negative node coordinates")
	}
	if level >= 62 {
		panic("treekem: node level too large")
	}
	return Index(position*(1<<(level+1)) + (1 << level) - 1)
}

// Level returns the number of trailing one bits in index. Leaves have level 0.
func Level(x Index) int {
	if x < 0 {
		panic("treekem: negative node index")
	}
	return bits.TrailingZeros(^uint(x))
}

// IsLeaf reports whether x is an even leaf index.
func IsLeaf(x Index) bool { return x&1 == 0 }

// Left returns the ideal left child. For an LBBT truncated by leaf count, the
// left child is always in the compact tree; Right may need RightBounded.
func Left(x Index) Index {
	level := Level(x)
	if level == 0 {
		panic(ErrNotParent)
	}
	return x ^ Index(1<<(level-1))
}

// Right returns the ideal right child in the untruncated arithmetic tree.
// When that index is outside a compact LBBT, use RightBounded or Tree.Right.
func Right(x Index) Index {
	level := Level(x)
	if level == 0 {
		panic(ErrNotParent)
	}
	return x ^ Index(3<<(level-1))
}

// Parent returns the ideal RFC tree-math parent without applying a leaf-count
// bound. For a truncated LBBT, use ParentBounded or Tree.Parent.
func Parent(x Index) Index {
	level := Level(x)
	return (x | Index(1<<level)) &^ Index(1<<(level+1))
}

// Width returns 2*leafCount-1 compact node slots.
func Width(leafCount int) int {
	if leafCount <= 0 {
		panic(ErrInvalidLeafCount)
	}
	return 2*leafCount - 1
}

// RootIndex returns the root of a compact left-balanced tree.
func RootIndex(leafCount int) Index {
	width := Width(leafCount)
	level := bits.Len(uint(width)) - 1
	return Index(1<<level - 1)
}

// ValidIndex reports whether x belongs to the compact tree.
func ValidIndex(x Index, leafCount int) bool {
	return leafCount > 0 && x >= 0 && int(x) < Width(leafCount)
}

func validateIndex(x Index, leafCount int) error {
	if !ValidIndex(x, leafCount) {
		return fmt.Errorf("%w: %d for leafCount=%d", ErrInvalidIndex, x, leafCount)
	}
	return nil
}

// LeftBounded validates and returns the left child in a compact LBBT.
func LeftBounded(x Index, leafCount int) (Index, error) {
	if err := validateIndex(x, leafCount); err != nil {
		return -1, err
	}
	if IsLeaf(x) {
		return -1, fmt.Errorf("%w: index=%d", ErrNotParent, x)
	}
	child := Left(x)
	if err := validateIndex(child, leafCount); err != nil {
		return -1, fmt.Errorf("left child: %w", err)
	}
	return child, nil
}

// RightBounded returns the actual right child in a possibly truncated LBBT.
// If the ideal perfect-tree right child is past the compact array, descend
// through left children until reaching the actual in-tree child.
func RightBounded(x Index, leafCount int) (Index, error) {
	if err := validateIndex(x, leafCount); err != nil {
		return -1, err
	}
	if IsLeaf(x) {
		return -1, fmt.Errorf("%w: index=%d", ErrNotParent, x)
	}
	child := Right(x)
	for !ValidIndex(child, leafCount) {
		if IsLeaf(child) {
			return -1, fmt.Errorf("%w: truncated right descendant of %d", ErrInvalidIndex, x)
		}
		child = Left(child)
	}
	return child, nil
}

// ParentBounded returns the actual parent in a compact, possibly unbalanced
// LBBT. The ideal Parent index can lie outside the compact array; climbing
// through such virtual nodes reaches the in-tree parent.
func ParentBounded(x Index, leafCount int) (Index, error) {
	if err := validateIndex(x, leafCount); err != nil {
		return -1, err
	}
	root := RootIndex(leafCount)
	if x == root {
		return -1, ErrRootHasNoParent
	}
	candidate := Parent(x)
	for !ValidIndex(candidate, leafCount) {
		next := Parent(candidate)
		if next == candidate {
			return -1, fmt.Errorf("%w: cannot bound parent of %d", ErrInvalidIndex, x)
		}
		candidate = next
	}
	if candidate == x {
		return -1, fmt.Errorf("%w: parent self-loop at %d", ErrInvalidIndex, x)
	}
	return candidate, nil
}

// DirectPath returns [leaf, parent(leaf), ..., root].
func DirectPath(leafIndex Index, leafCount int) ([]Index, error) {
	if err := validateIndex(leafIndex, leafCount); err != nil {
		return nil, err
	}
	if !IsLeaf(leafIndex) {
		return nil, fmt.Errorf("%w: direct path start %d is not a leaf", ErrInvalidIndex, leafIndex)
	}
	position := int(leafIndex) / 2
	if position >= leafCount {
		return nil, fmt.Errorf("%w: leaf position %d exceeds leafCount=%d", ErrInvalidIndex, position, leafCount)
	}
	path := []Index{leafIndex}
	root := RootIndex(leafCount)
	current := leafIndex
	for current != root {
		parentIndex, err := ParentBounded(current, leafCount)
		if err != nil {
			return nil, err
		}
		path = append(path, parentIndex)
		current = parentIndex
	}
	return path, nil
}

// Copath returns one sibling for every node in DirectPath except root.
// Thus len(Copath(leaf,n)) == len(DirectPath(leaf,n))-1, equal to the number
// of parent UpdatePath steps.
func Copath(leafIndex Index, leafCount int) ([]Index, error) {
	path, err := DirectPath(leafIndex, leafCount)
	if err != nil {
		return nil, err
	}
	out := make([]Index, 0, len(path)-1)
	for i := 0; i+1 < len(path); i++ {
		child := path[i]
		parentIndex := path[i+1]
		leftChild, err := LeftBounded(parentIndex, leafCount)
		if err != nil {
			return nil, err
		}
		if leftChild == child {
			rightChild, err := RightBounded(parentIndex, leafCount)
			if err != nil {
				return nil, err
			}
			out = append(out, rightChild)
		} else {
			out = append(out, leftChild)
		}
	}
	return out, nil
}

// Sibling returns the bounded sibling of x.
func Sibling(x Index, leafCount int) (Index, error) {
	parentIndex, err := ParentBounded(x, leafCount)
	if err != nil {
		return -1, err
	}
	leftChild, err := LeftBounded(parentIndex, leafCount)
	if err != nil {
		return -1, err
	}
	if leftChild == x {
		return RightBounded(parentIndex, leafCount)
	}
	return leftChild, nil
}

// Snake-case aliases make the RFC Appendix C vocabulary directly available to
// same-package tests and ports.
func leaf(position int) Index { return Leaf(position) }

func node(level, position int) Index { return NodeIndexAt(level, position) }

func level(x Index) int { return Level(x) }

func left(x Index) Index { return Left(x) }

func right(x Index) Index { return Right(x) }

func parent(x Index) Index { return Parent(x) }

func direct_path(leafIndex Index, leafCount int) ([]Index, error) {
	return DirectPath(leafIndex, leafCount)
}

func copath(leafIndex Index, leafCount int) ([]Index, error) {
	return Copath(leafIndex, leafCount)
}

func (t *RatchetTree) direct_path(leafIndex Index) ([]Index, error) {
	return t.DirectPath(leafIndex)
}

func (t *RatchetTree) copath(leafIndex Index) ([]Index, error) {
	return t.Copath(leafIndex)
}

func (t *RatchetTree) parent(x Index) (Index, error) { return t.Parent(x) }

func (t *RatchetTree) left(x Index) (Index, error) { return t.Left(x) }

func (t *RatchetTree) right(x Index) (Index, error) { return t.Right(x) }
