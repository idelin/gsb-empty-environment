package readproof

// Range is a half-open interval of block indices [Start, End).
type Range struct {
	Start uint64
	End   uint64
}

// Node is an authenticated proof node. Leaf nodes carry Data; internal
// sibling commitments carry nil Data together with the covered half-open
// interval [Start, End).
type Node struct {
	Start uint64
	End   uint64
	Hash  [32]byte
	Data  []byte // nil for internal sibling commitments
}

// IsLeaf reports whether the node is a leaf commitment.
func (n Node) IsLeaf() bool { return n.End-n.Start == 1 && n.Start < n.End }

// treeNode is the recursive full-tree reference representation.
type treeNode struct {
	start    uint64
	end      uint64
	hash     [32]byte
	left     *treeNode
	right    *treeNode
	leafData []byte // non-nil only for leaves
}

// Tree is an independently usable full-tree reference. It is built from the
// complete file contents and serves as ground truth for proof generation
// and incremental-update tests.
type Tree struct {
	length   uint64
	root     *treeNode
	rootHash [32]byte
}

func buildNode(leaves [][]byte, start, end uint64) *treeNode {
	node := &treeNode{start: start, end: end}
	if end-start == 1 {
		data := leaves[start]
		node.leafData = data
		node.hash = LeafHash(start, data)
		return node
	}
	sp := start + splitPoint(end-start)
	node.left = buildNode(leaves, start, sp)
	node.right = buildNode(leaves, sp, end)
	node.hash = innerHash(start, end, node.left.hash, node.right.hash)
	return node
}

// BuildTree constructs the full Merkle tree for an entire file. data must
// split into BlockSize blocks with only the final block shorter; an empty
// input produces the standalone empty commitment.
func BuildTree(data []byte) *Tree {
	t := &Tree{length: uint64(len(data))}
	if len(data) == 0 {
		t.rootHash = EmptyRoot
		return t
	}
	n := NumLeaves(uint64(len(data)))
	leaves := make([][]byte, n)
	for i := uint64(0); i < n; i++ {
		off := i * BlockSize
		end := off + BlockSize
		if end > uint64(len(data)) {
			end = uint64(len(data))
		}
		leaves[i] = data[off:end]
	}
	t.root = buildNode(leaves, 0, n)
	t.rootHash = t.root.hash
	return t
}

// Root returns the root commitment of the tree.
func (t *Tree) Root() [32]byte { return t.rootHash }

// Length returns the committed file length in bytes.
func (t *Tree) Length() uint64 { return t.length }

func (t *Tree) findNode(start, end uint64) *treeNode {
	var walk func(n *treeNode) *treeNode
	walk = func(n *treeNode) *treeNode {
		if n.start == start && n.end == end {
			return n
		}
		if end <= n.left.end {
			return walk(n.left)
		}
		return walk(n.right)
	}
	return walk(t.root)
}

// canonicalize validates ranges against a file of n leaves. With
// allowMerge false it requires strictly sorted disjoint intervals; with
// allowMerge true adjacent and overlapping intervals are merged so a single
// read request can be deduplicated.
func canonicalize(ranges []Range, n uint64, allowMerge bool) ([]Range, error) {
	if len(ranges) == 0 {
		return nil, ErrEmptyRange
	}
	out := make([]Range, 0, len(ranges))
	for i, r := range ranges {
		if r.Start >= r.End {
			return nil, ErrEmptyRange
		}
		if r.End > n {
			return nil, ErrRangeOutOfBounds
		}
		if i > 0 {
			prev := ranges[i-1]
			if !allowMerge && r.Start < prev.Start {
				return nil, ErrRangeUnordered
			}
			if r.Start < prev.End {
				if !allowMerge {
					return nil, ErrRangeUnordered
				}
			}
		}
		if len(out) > 0 && r.Start < out[len(out)-1].End {
			if !allowMerge {
				return nil, ErrRangeUnordered
			}
			if r.End > out[len(out)-1].End {
				out[len(out)-1].End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// MergeRanges normalizes possibly overlapping block ranges into sorted,
// disjoint, merged ranges bounded by a file of n leaves.
func MergeRanges(ranges []Range, n uint64) ([]Range, error) {
	return canonicalize(ranges, n, true)
}
