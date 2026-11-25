package capsum

// IndexView is the read-only projection of the validated program index used
// by external checkers.
type IndexView struct {
	idx *index
}

// View returns a read-only view of the validated index.
func (x *index) View() *IndexView { return &IndexView{idx: x} }

// ValidatedView validates a program and returns its index view.
func ValidatedView(p *Program) (*IndexView, error) {
	x, err := Validate(p)
	if err != nil {
		return nil, err
	}
	return x.View(), nil
}

// Alphabet returns the finite root/alias-plus-component alphabet.
func (v *IndexView) Alphabet() []string { return append([]string(nil), v.idx.alphabet...) }

// Components returns the component-only alphabet.
func (v *IndexView) Components() []string {
	return append([]string(nil), v.idx.componentAlphabet...)
}

// HandleCount returns the number of distinct queue handles.
func (v *IndexView) HandleCount() int { return len(v.idx.handles) }

// HandleBit returns the queue bit assigned to a handle name.
func (v *IndexView) HandleBit(name string) (uint16, bool) {
	id, ok := v.idx.handleIndex[name]
	if !ok {
		return 0, false
	}
	return uint16(1) << uint(id), true
}
