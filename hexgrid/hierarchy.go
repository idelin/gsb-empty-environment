package hexgrid

// Parent returns the cell's parent at the given (coarser) resolution.
// The parent is the nearest cell center of the coarser sublattice, so
// containment is exact and center-based.
func (c Cell) Parent(res int) (Cell, error) {
	cc, err := decodeCell(c)
	if err != nil {
		return 0, err
	}
	if res < 0 || res > cc.res {
		return 0, ErrInvalidResolution
	}
	cur := cc.c
	for l := cc.res; l > res; l-- {
		cur = parentCoord(cur)
	}
	return cellFromCoord(cc.face, cur, res)
}

// Children enumerates all cells at the given (finer) resolution whose
// ancestor chain leads to c. A non-pentagon cell has exactly 7^d
// children d levels down; pentagon cells have fewer because one sector
// is missing at the icosahedron vertices.
func (c Cell) Children(res int) ([]Cell, error) {
	cc, err := decodeCell(c)
	if err != nil {
		return nil, err
	}
	if res < cc.res || res > MaxRes {
		return nil, ErrInvalidResolution
	}
	depth := res - cc.res
	total := 1
	for i := 0; i < depth; i++ {
		total *= 7
	}
	out := make([]Cell, 0, total)
	seen := make(map[Cell]struct{}, total)
	var firstErr error
	var rec func(cv vec2, level int)
	rec = func(cv vec2, level int) {
		if firstErr != nil {
			return
		}
		if level == depth {
			cell, err := cellFromCoord(cc.face, cv, res)
			if err != nil {
				firstErr = err
				return
			}
			if _, ok := seen[cell]; !ok {
				seen[cell] = struct{}{}
				out = append(out, cell)
			}
			return
		}
		base := mulMc(cv)
		for d := 0; d < 7; d++ {
			rec(base.add(unitVecs[d]), level+1)
		}
	}
	rec(cc.c, 0)
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// IsAncestorOf reports whether c is an ancestor of (or equal to) o.
func (c Cell) IsAncestorOf(o Cell) bool {
	if c.Resolution() > o.Resolution() {
		return false
	}
	p, err := o.Parent(c.Resolution())
	return err == nil && p == c
}
