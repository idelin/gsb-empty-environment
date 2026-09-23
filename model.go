package mtf

import "errors"

// ErrIndexOutOfRange is returned when a decode index is not a valid MTF rank.
// The model state is left untouched when this error is returned.
var ErrIndexOutOfRange = errors.New("mtf: index out of range")

// ErrUnknownSymbol is returned by Encode for symbols outside the alphabet
// when the unknown-symbol policy is PolicyReject.
var ErrUnknownSymbol = errors.New("mtf: unknown symbol")

// fenwick is a binary indexed tree over unit weights giving O(log n)
// prefix sums and order-statistic selection.
type fenwick struct {
	tree []int
}

func newFenwick(n int) *fenwick { return &fenwick{tree: make([]int, n+1)} }

func (f *fenwick) add(i, delta int) {
	for i++; i < len(f.tree); i += i & -i {
		f.tree[i] += delta
	}
}

// sum returns the number of occupied slots in [0, i).
func (f *fenwick) sum(i int) int {
	s := 0
	for ; i > 0; i -= i & -i {
		s += f.tree[i]
	}
	return s
}

// selectK returns the slot holding the k-th occupied slot (0-based).
func (f *fenwick) selectK(k int) int {
	i, acc := 0, 0
	for b := highestPow2(len(f.tree)); b > 0; b >>= 1 {
		if i+b < len(f.tree) && acc+f.tree[i+b] <= k {
			i += b
			acc += f.tree[i]
		}
	}
	return i
}

func highestPow2(n int) int {
	p := 1
	for p<<1 <= n {
		p <<= 1
	}
	return p
}

// Model is the MTF state over an alphabet. Rank queries, order-statistic
// selection and move-to-front all run in O(log n) via a Fenwick tree over
// slots; symbols are prepended into fresh slots instead of shifting the
// whole alphabet, and the layout is compacted (amortized) when slots run
// out. Model is not safe for concurrent use.
type Model struct {
	alpha  *Alphabet
	slotOf []int // alphabet id -> slot
	symOf  []int // slot -> alphabet id, -1 if free
	bit    *fenwick
	lo     int // next prepend slot is lo-1
}

// NewModel builds a model whose initial MTF order equals the alphabet order.
func NewModel(a *Alphabet) *Model {
	m := &Model{alpha: a}
	m.Reset()
	return m
}

// Alphabet returns the model's current alphabet.
func (m *Model) Alphabet() *Alphabet { return m.alpha }

// capacity targets at least len(alpha) free prepend slots per compaction.
func capacity(n int) int {
	c := 2*n + 16
	return c
}

// Reset restores the initial state: MTF order equals the (current) alphabet
// order. Symbols added via the escape protocol are kept in the alphabet.
func (m *Model) Reset() {
	n := m.alpha.Len()
	cap := capacity(n)
	m.slotOf = make([]int, n)
	m.symOf = make([]int, cap)
	for i := range m.symOf {
		m.symOf[i] = -1
	}
	m.bit = newFenwick(cap)
	m.lo = cap / 2
	for id := 0; id < n; id++ {
		slot := m.lo + id
		m.slotOf[id] = slot
		m.symOf[slot] = id
		m.bit.add(slot, 1)
	}
}

// compact rebuilds the slot layout centered in a fresh array, preserving
// the current MTF order. Runs in O(n log n), amortized against O(n) prepends.
func (m *Model) compact() {
	n := m.alpha.Len()
	total := m.bit.sum(len(m.symOf))
	order := make([]int, 0, total)
	for k := 0; k < total; k++ {
		order = append(order, m.symOf[m.bit.selectK(k)])
	}
	cap := capacity(n)
	m.symOf = make([]int, cap)
	for i := range m.symOf {
		m.symOf[i] = -1
	}
	for i := range m.slotOf {
		m.slotOf[i] = -1
	}
	m.bit = newFenwick(cap)
	m.lo = cap / 2
	for pos, id := range order {
		slot := m.lo + pos
		m.slotOf[id] = slot
		m.symOf[slot] = id
		m.bit.add(slot, 1)
	}
}

// moveToFront moves the symbol at the given slot to the front, O(log n).
func (m *Model) moveToFront(slot int) {
	id := m.symOf[slot]
	m.bit.add(slot, -1)
	m.symOf[slot] = -1
	if m.lo == 0 {
		m.compact()
		// The symbol was removed before compaction; if compaction somehow
		// kept it (it cannot), remove it from its new slot as well.
		if s := m.slotOf[id]; s >= 0 {
			m.bit.add(s, -1)
			m.symOf[s] = -1
		}
	}
	m.lo--
	m.slotOf[id] = m.lo
	m.symOf[m.lo] = id
	m.bit.add(m.lo, 1)
}

// grow appends a freshly introduced symbol (escape-update protocol) to the
// alphabet and places it at the front of the MTF order.
func (m *Model) grow(sym rune) {
	next, err := m.alpha.Extend(sym)
	if err != nil { // cannot happen: caller checked the symbol is unknown
		panic(err)
	}
	m.alpha = next
	m.slotOf = append(m.slotOf, -1)
	if m.lo == 0 {
		m.compact()
	}
	m.lo--
	id := next.Len() - 1
	m.slotOf[id] = m.lo
	m.symOf[m.lo] = id
	m.bit.add(m.lo, 1)
}

// Encode returns the current MTF rank of sym and moves it to the front.
// Unknown symbols are rejected; callers wanting the escape-update protocol
// should use Encoder with PolicyEscapeUpdate.
func (m *Model) Encode(sym rune) (int, error) {
	id, ok := m.alpha.ID(sym)
	if !ok {
		return 0, ErrUnknownSymbol
	}
	rank := m.bit.sum(m.slotOf[id])
	m.moveToFront(m.slotOf[id])
	return rank, nil
}

// Decode returns the symbol at the given MTF rank and moves it to the front.
// An out-of-range index is rejected with ErrIndexOutOfRange and the model
// state is left unchanged.
func (m *Model) Decode(index int) (rune, error) {
	if index < 0 || index >= m.alpha.Len() {
		return 0, ErrIndexOutOfRange
	}
	slot := m.bit.selectK(index)
	sym, _ := m.alpha.Symbol(m.symOf[slot])
	m.moveToFront(slot)
	return sym, nil
}

// Checkpoint is an opaque snapshot of the full model state, intended to be
// taken at block boundaries. Restoring it reproduces the exact state, so
// resumed processing matches uninterrupted processing.
type Checkpoint struct {
	alpha   *Alphabet
	slotOf  []int
	symOf   []int
	bitTree []int
	lo      int
}

// Checkpoint captures the current model state in O(n).
func (m *Model) Checkpoint() Checkpoint {
	return Checkpoint{
		alpha:   m.alpha,
		slotOf:  append([]int(nil), m.slotOf...),
		symOf:   append([]int(nil), m.symOf...),
		bitTree: append([]int(nil), m.bit.tree...),
		lo:      m.lo,
	}
}

// Restore returns the model to a previously captured checkpoint.
func (m *Model) Restore(cp Checkpoint) {
	m.alpha = cp.alpha
	m.slotOf = append([]int(nil), cp.slotOf...)
	m.symOf = append([]int(nil), cp.symOf...)
	m.bit = &fenwick{tree: append([]int(nil), cp.bitTree...)}
	m.lo = cp.lo
}

// Order returns the symbols in current MTF order (front first).
// Intended for verification against the reference implementation.
func (m *Model) Order() []rune {
	out := make([]rune, 0, m.alpha.Len())
	for k := 0; k < m.alpha.Len(); k++ {
		sym, _ := m.alpha.Symbol(m.symOf[m.bit.selectK(k)])
		out = append(out, sym)
	}
	return out
}
