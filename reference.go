package mtf

// RefModel is an independent, deliberately naive reference implementation
// of the MTF transform using a plain list with linear scans and shifts.
// It exists to cross-check the logarithmic-time Model: tests replay whole
// operation sequences against both and compare states at arbitrary
// breakpoints, including after checkpoint/restore.
type RefModel struct {
	list []rune // front of the MTF list is index 0
}

// NewRefModel builds a reference model whose initial order is the alphabet
// definition order.
func NewRefModel(a *Alphabet) *RefModel {
	return &RefModel{list: a.Symbols()}
}

// Encode returns the rank of sym and moves it to the front, O(n).
func (r *RefModel) Encode(sym rune) (int, error) {
	for i, s := range r.list {
		if s == sym {
			copy(r.list[1:i+1], r.list[:i])
			r.list[0] = sym
			return i, nil
		}
	}
	return 0, ErrUnknownSymbol
}

// Decode returns the symbol at rank index and moves it to the front, O(n).
// Out-of-range indices are rejected and the list is left unchanged.
func (r *RefModel) Decode(index int) (rune, error) {
	if index < 0 || index >= len(r.list) {
		return 0, ErrIndexOutOfRange
	}
	sym := r.list[index]
	copy(r.list[1:index+1], r.list[:index])
	r.list[0] = sym
	return sym, nil
}

// Grow appends a new symbol (escape-update protocol) at the front.
func (r *RefModel) Grow(sym rune) {
	r.list = append([]rune{sym}, r.list...)
}

// Reset restores the initial order of the current list contents: callers
// pass the alphabet so the order matches Model.Reset exactly.
func (r *RefModel) Reset(a *Alphabet) {
	r.list = a.Symbols()
}

// Snapshot copies the current list, the reference analogue of Checkpoint.
func (r *RefModel) Snapshot() []rune { return append([]rune(nil), r.list...) }

// Restore replaces the list with a previously taken snapshot.
func (r *RefModel) Restore(snap []rune) { r.list = append([]rune(nil), snap...) }

// Order returns the current MTF order, front first.
func (r *RefModel) Order() []rune { return append([]rune(nil), r.list...) }
