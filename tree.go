// Package huffman implements an adaptive (dynamic) Huffman codec using the
// FGK algorithm (Faller-Gallager-Knuth). The encoder and decoder each
// maintain an identical, incrementally updated binary tree that satisfies
// the sibling property, so no preliminary pass over the input is required.
//
// Symbols are the 256 byte values plus a dedicated end-of-stream symbol.
// A not-yet-transmitted (NYT) node is used to introduce new symbols into
// the tree; unseen symbols are emitted as the NYT code followed by the
// raw 9-bit symbol value.
package huffman

// Symbol values. Bytes 0-255 map to themselves; eofSymbol marks the end of
// the stream so decoding is self-terminating without external framing.
const (
	eofSymbol uint16 = 256
	nytSymbol uint16 = 257 // internal marker, never emitted as data

	// rawSymbolBits is the number of bits used to transmit a previously
	// unseen symbol literally (must cover eofSymbol).
	rawSymbolBits = 9

	// maxNodes bounds the tree size: at most 258 leaves (256 bytes + NYT +
	// EOF) and 257 internal nodes. Memory usage is therefore constant with
	// respect to the input length.
	maxNodes = 2*258 - 1
)

// node is a single node of the adaptive Huffman tree.
type node struct {
	weight uint64 // number of occurrences in the subtree
	order  int    // implicit numbering used by the sibling property
	symbol uint16 // valid only for leaves

	parent *node
	left   *node
	right  *node
}

func (n *node) isLeaf() bool { return n.left == nil && n.right == nil }

// tree is an FGK adaptive Huffman tree.
type tree struct {
	root   *node
	nyt    *node
	leaves map[uint16]*node
	nodes  []*node // every node ever created, for block-leader lookup
	next   int     // next order number to hand out (decreasing)
}

func newTree() *tree {
	t := &tree{
		leaves: make(map[uint16]*node),
		next:   maxNodes,
	}
	t.nyt = t.newNode(nytSymbol, nil)
	t.root = t.nyt
	return t
}

func (t *tree) newNode(symbol uint16, parent *node) *node {
	n := &node{symbol: symbol, parent: parent, order: t.next}
	t.next--
	t.nodes = append(t.nodes, n)
	return n
}

// update incorporates one occurrence of symbol into the tree, restoring the
// sibling property via block-leader swaps (FGK).
func (t *tree) update(symbol uint16) {
	var current *node
	if leaf, seen := t.leaves[symbol]; seen {
		current = leaf
	} else {
		current = t.split(symbol)
	}
	for current != nil {
		if leader := t.blockLeader(current); leader != current && leader != current.parent {
			t.swap(current, leader)
		}
		current.weight++
		current = current.parent
	}
}

// split turns the NYT leaf into an internal node whose children are a
// fresh NYT leaf and a leaf for the new symbol, and returns the internal
// node so weight propagation can start there. The NYT node object is
// reused as the internal node, keeping its order number; the two children
// receive the next two (lower) order numbers, preserving the invariant
// that parents always outrank their children.
func (t *tree) split(symbol uint16) *node {
	internal := t.nyt
	t.nyt = t.newNode(nytSymbol, internal)
	leaf := t.newNode(symbol, internal)
	internal.left = t.nyt
	internal.right = leaf
	t.leaves[symbol] = leaf
	return internal
}

// blockLeader returns the node with the highest order number among all
// nodes sharing n's weight.
func (t *tree) blockLeader(n *node) *node {
	leader := n
	for _, other := range t.nodes {
		if other.weight == n.weight && other.order > leader.order {
			leader = other
		}
	}
	return leader
}

// swap exchanges the positions of two nodes in the tree (neither may be an
// ancestor of the other) along with their order numbers.
func (t *tree) swap(a, b *node) {
	a.order, b.order = b.order, a.order
	ap, bp := a.parent, b.parent
	if ap.left == a {
		ap.left = b
	} else {
		ap.right = b
	}
	if bp.left == b {
		bp.left = a
	} else {
		bp.right = a
	}
	a.parent, b.parent = bp, ap
}

// codeOf returns the path from the root to n as a slice of bits
// (false = left, true = right).
func codeOf(n *node) []bool {
	var reversed []bool
	for cur := n; cur.parent != nil; cur = cur.parent {
		reversed = append(reversed, cur.parent.right == cur)
	}
	bits := make([]bool, len(reversed))
	for i, bit := range reversed {
		bits[len(reversed)-1-i] = bit
	}
	return bits
}
