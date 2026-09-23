package mtf

import "math/rand"

// Model is the MTF state over an Alphabet. It is safe to copy a model only
// via Checkpoint/Restore; use Reset to return to the canonical order.
type Model struct {
	alpha *Alphabet
	root  *node
	nodes map[byte]*node
	rng   *rand.Rand
}

// NewModel builds a model whose initial MTF order is the alphabet's
// canonical order.
func NewModel(a *Alphabet) *Model {
	return newModelFromOrder(a, a.Symbols())
}

func newModelFromOrder(a *Alphabet, order []byte) *Model {
	m := &Model{
		alpha: a,
		nodes: make(map[byte]*node, len(order)),
		rng:   rand.New(rand.NewSource(987654321)),
	}
	for _, s := range order {
		n := &node{sym: s, prio: m.rng.Uint64(), size: 1}
		m.nodes[s] = n
		m.root = merge(m.root, n)
	}
	return m
}

// Alphabet returns the model's alphabet.
func (m *Model) Alphabet() *Alphabet { return m.alpha }

// Order returns the current MTF order, front first.
func (m *Model) Order() []byte {
	out := make([]byte, 0, m.alpha.Size())
	inorder(m.root, &out)
	return out
}

// Reset restores the MTF order to the alphabet's canonical order.
func (m *Model) Reset() {
	m.root = nil
	m.nodes = make(map[byte]*node, m.alpha.Size())
	for _, s := range m.alpha.Symbols() {
		n := &node{sym: s, prio: m.rng.Uint64(), size: 1}
		m.nodes[s] = n
		m.root = merge(m.root, n)
	}
}

// EncodeIndex returns the current index of sym and moves it to the front.
// Unknown symbols are rejected with ErrUnknownSymbol and leave the state
// unchanged; handling them is the caller's explicit choice (see
// BlockEncoder with EscapeUnknown).
func (m *Model) EncodeIndex(sym byte) (int, error) {
	n, ok := m.nodes[sym]
	if !ok {
		return -1, ErrUnknownSymbol
	}
	r := rank(n)
	m.moveToFront(n, r)
	return r, nil
}

// DecodeIndex returns the symbol at index i and moves it to the front.
// Out-of-range indices are rejected with ErrIndexOutOfRange and leave the
// state unchanged.
func (m *Model) DecodeIndex(i int) (byte, error) {
	if i < 0 || i >= sz(m.root) {
		return 0, ErrIndexOutOfRange
	}
	n := kth(m.root, i)
	m.moveToFront(n, i)
	return n.sym, nil
}

func (m *Model) moveToFront(n *node, r int) {
	if r == 0 {
		return
	}
	l, mr := split(m.root, r)
	mid, rest := split(mr, 1)
	m.root = merge(mid, merge(l, rest))
}

// extend applies the shared escape-update protocol: the alphabet grows by
// sym (bumping its version), the new symbol conceptually enters at the end
// (index == old size, the escape index) and is then moved to the front.
// Both encoder and decoder must apply this identically.
func (m *Model) extend(sym byte) error {
	if err := m.alpha.Extend(sym); err != nil {
		return err
	}
	n := &node{sym: sym, prio: m.rng.Uint64(), size: 1}
	m.nodes[sym] = n
	m.root = merge(m.root, n)
	m.moveToFront(n, m.alpha.Size()-1)
	return nil
}
