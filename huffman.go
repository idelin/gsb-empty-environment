// Package adaptivehuffman implements the FGK (Faller-Gallager-Knuth)
// adaptive Huffman coding algorithm.
//
// Encoding and decoding run in a single streaming pass: no preliminary
// scan of the input is required. Both sides maintain an identical
// dynamically updated binary tree that satisfies the sibling property,
// introduce previously unseen bytes through an NYT (Not Yet Transmitted)
// marker, and stay synchronized bit for bit. The alphabet is the 256
// possible byte values, so the tree holds at most 256 symbol leaves
// plus the NYT leaf and never exceeds 2*256+1 = 513 nodes; memory
// usage is constant with respect to the input length.
package adaptivehuffman

const (
	symNYT      = -1 // Not Yet Transmitted marker
	symInternal = -2 // internal (non-leaf) node

	maxSymbols = 256
	// maxOrder is the order number assigned to the root. A full tree has
	// 2*maxSymbols+1 nodes and every split consumes two order numbers,
	// so all order numbers stay non-negative.
	maxOrder = 2 * maxSymbols
)

// node is a node of the adaptive Huffman tree. Leaves carry a byte
// symbol (0-255) or the NYT marker; internal nodes carry symInternal.
type node struct {
	symbol int
	weight uint64
	order  int // higher order == closer to the root in the numbering
	parent *node
	left   *node
	right  *node
}

func (n *node) isLeaf() bool { return n.left == nil }

// tree is the adaptive Huffman tree shared (independently maintained)
// by the encoder and the decoder.
type tree struct {
	root   *node
	nyt    *node
	leaves map[byte]*node
	all    []*node // every node ever created, for block-leader lookup
}

func newTree() *tree {
	root := &node{symbol: symNYT, order: maxOrder}
	return &tree{
		root:   root,
		nyt:    root,
		leaves: make(map[byte]*node),
		all:    []*node{root},
	}
}

// insert introduces a previously unseen byte by splitting the NYT node
// into a new NYT (left) and a leaf for b (right), then rebalances.
func (t *tree) insert(b byte) {
	internal := t.nyt
	internal.symbol = symInternal
	newNYT := &node{symbol: symNYT, order: internal.order - 2, parent: internal}
	leaf := &node{symbol: int(b), order: internal.order - 1, parent: internal}
	internal.left = newNYT
	internal.right = leaf
	t.nyt = newNYT
	t.leaves[b] = leaf
	t.all = append(t.all, newNYT, leaf)
	t.update(leaf)
}

// update increments the weight of n and propagates the increment up to
// the root, swapping nodes as needed to preserve the sibling property.
func (t *tree) update(n *node) {
	for n != nil {
		if leader := t.leader(n.weight); leader != n && leader != n.parent {
			t.swap(n, leader)
		}
		n.weight++
		n = n.parent
	}
}

// leader returns the node with the highest order number among all nodes
// whose weight equals w (the leader of the weight block).
func (t *tree) leader(w uint64) *node {
	var best *node
	for _, nd := range t.all {
		if nd.weight == w && (best == nil || nd.order > best.order) {
			best = nd
		}
	}
	return best
}

// swap exchanges the positions of two nodes in the tree along with
// their order numbers. Node identities (and hence the leaves map and
// the nyt pointer) are unaffected.
func (t *tree) swap(a, b *node) {
	ap, bp := a.parent, b.parent
	// Capture child positions before mutating: ap and bp may be the
	// same node when a and b are siblings.
	aIsLeft := ap != nil && ap.left == a
	bIsLeft := bp != nil && bp.left == b
	if ap != nil {
		if aIsLeft {
			ap.left = b
		} else {
			ap.right = b
		}
	}
	if bp != nil {
		if bIsLeft {
			bp.left = a
		} else {
			bp.right = a
		}
	}
	a.parent, b.parent = bp, ap
	a.order, b.order = b.order, a.order
	if t.root == a {
		t.root = b
	} else if t.root == b {
		t.root = a
	}
}

// pathBits returns the code of n as the bit sequence from the root
// down to n (0 = left, 1 = right).
func pathBits(n *node) []byte {
	var rev []byte
	for n.parent != nil {
		if n.parent.right == n {
			rev = append(rev, 1)
		} else {
			rev = append(rev, 0)
		}
		n = n.parent
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
