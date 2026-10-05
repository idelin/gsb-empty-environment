package adaptivehuffman

// Symbol values stored in node.symbol.
const (
	symNYT      int32 = -1 // the "not yet transmitted" escape leaf
	symInternal int32 = -2 // internal node
	numSymbols        = 256
)

// DefaultMaxWeight is the default root-weight threshold that triggers a
// rescale. Frame encoding/decoding always uses this value; custom values are
// only meaningful for the symbol-level API when both sides agree on them.
const DefaultMaxWeight uint64 = 1 << 32

// node is a node of the adaptive code tree.
//
// Numbering: every node receives a stable id at creation time (0, 1, 2, ...)
// which never changes. The sibling-property ordering is maintained separately
// in model.order: order[rank] is the node at that rank, and node.rank is the
// inverse. Swapping two nodes exchanges their ranks but never their ids.
type node struct {
	id     int
	rank   int
	weight uint64
	symbol int32 // 0..255 leaf symbol, symNYT, or symInternal
	parent *node
	left   *node
	right  *node
}

// model is the adaptive state shared (independently maintained) by the
// encoder and the decoder. All update decisions are derived exclusively from
// the tree structure and the explicit order slice; no map iteration or other
// nondeterministic ordering is involved, so both sides evolve identically.
type model struct {
	root      *node
	nyt       *node
	order     []*node // order[i] has rank i; rank 0 is always the NYT node
	leaves    [numSymbols]*node
	numSyms   int
	nextID    int
	maxWeight uint64
}

func newModel(maxWeight uint64) *model {
	if maxWeight < 2 {
		maxWeight = DefaultMaxWeight
	}
	nyt := &node{id: 0, symbol: symNYT}
	return &model{
		root:      nyt,
		nyt:       nyt,
		order:     []*node{nyt},
		nextID:    1,
		maxWeight: maxWeight,
	}
}

// update incorporates one occurrence of sym into the tree.
//
// Update rules (identical on both sides):
//  1. If root.weight >= maxWeight, rescale (halve all weights) first.
//  2. If sym has never been seen, spawn: the NYT leaf is replaced by a new
//     internal node I whose left child is a fresh NYT leaf and whose right
//     child is a fresh leaf for sym. I and the new leaf start with weight 1.
//     The increment loop then continues from I's parent.
//     Otherwise the loop starts at sym's leaf.
//  3. Loop: swap the current node with the highest-rank node of equal
//     weight, unless that node is the current node itself or its parent
//     (only the parent can ever be an equal-weight ancestor, because the NYT
//     leaf is the unique zero-weight node). Then increment the current
//     node's weight and continue from its parent.
func (m *model) update(sym byte) {
	if m.root.weight >= m.maxWeight {
		m.rescale()
	}
	var cur *node
	if m.leaves[sym] == nil {
		m.spawn(sym)
		cur = m.leaves[sym].parent.parent // parent of the new internal node
	} else {
		cur = m.leaves[sym]
	}
	for cur != nil {
		if leader := m.blockLeader(cur.weight); leader != cur && leader != cur.parent {
			m.swap(cur, leader)
		}
		cur.weight++
		cur = cur.parent
	}
}

// spawn replaces the NYT leaf with a new internal node carrying a fresh NYT
// leaf (left) and a fresh leaf for sym (right). The new internal node
// inherits the retired NYT node's id, so ids stay dense (0..N-1); the two
// new leaves take the next two ids. The three nodes occupy the old NYT
// node's position in the order list, in the sequence [newNYT, leaf,
// internal]; the leaf and the internal node start at weight 1.
func (m *model) spawn(sym byte) {
	old := m.nyt
	internal := &node{id: old.id, symbol: symInternal, weight: 1}
	newNYT := &node{id: m.nextID, symbol: symNYT}
	m.nextID++
	leaf := &node{id: m.nextID, symbol: int32(sym), weight: 1}
	m.nextID++

	internal.left, internal.right = newNYT, leaf
	newNYT.parent, leaf.parent = internal, internal
	internal.parent = old.parent
	switch {
	case old.parent == nil:
		m.root = internal
	case old.parent.left == old:
		old.parent.left = internal
	default:
		old.parent.right = internal
	}

	r := old.rank
	newOrder := make([]*node, 0, len(m.order)+2)
	newOrder = append(newOrder, m.order[:r]...)
	newOrder = append(newOrder, newNYT, leaf, internal)
	newOrder = append(newOrder, m.order[r+1:]...)
	m.order = newOrder
	for i := r; i < len(m.order); i++ {
		m.order[i].rank = i
	}

	m.nyt = newNYT
	m.leaves[sym] = leaf
	m.numSyms++
}

// blockLeader returns the highest-rank node with weight w. The scan is a
// plain linear search from the top of the order list, so it is well defined
// even in the transient states where weights along the order list are not
// sorted (which can legitimately occur between two increments of one update).
func (m *model) blockLeader(w uint64) *node {
	for i := len(m.order) - 1; i >= 0; i-- {
		if m.order[i].weight == w {
			return m.order[i]
		}
	}
	return nil // unreachable: cur itself always has weight w
}

// swap exchanges the tree positions and ranks of a and b. Neither may be an
// ancestor of the other; node ids are unaffected.
func (m *model) swap(a, b *node) {
	ap, bp := a.parent, b.parent
	aLeft := ap != nil && ap.left == a
	bLeft := bp != nil && bp.left == b
	if ap == nil {
		m.root = b
	} else if aLeft {
		ap.left = b
	} else {
		ap.right = b
	}
	if bp == nil {
		m.root = a
	} else if bLeft {
		bp.left = a
	} else {
		bp.right = a
	}
	a.parent, b.parent = bp, ap
	m.order[a.rank], m.order[b.rank] = b, a
	a.rank, b.rank = b.rank, a.rank
}

// rescale halves all weights: leaves are replaced by ceil(w/2) and internal
// nodes by the sum of their (already rescaled) children. Processing the order
// list from rank 0 upward guarantees children are handled before their
// parents. The NYT leaf stays at weight 0.
func (m *model) rescale() {
	for _, n := range m.order {
		if n.left == nil {
			n.weight = (n.weight + 1) >> 1
		} else {
			n.weight = n.left.weight + n.right.weight
		}
	}
}
