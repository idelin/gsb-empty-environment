package netsec

// subtract returns a disjoint rectangle cover of a \ b. The result
// holds at most 2*N rectangles, where N is the number of dimensions
// constrained by b.
func (d Domain) subtract(a, b Rect) []Rect {
	if !a.Intersects(b) {
		return []Rect{a}
	}
	var out []Rect
	rem := a.Clone()
	if rem.Values == nil {
		rem.Values = map[string]map[string]bool{}
	}
	for _, dim := range d {
		bVals, ok := b.Values[dim.Name]
		if !ok {
			continue
		}
		aVals := d.valuesOf(rem, dim)
		var outside map[string]bool
		for v := range aVals {
			if !bVals[v] {
				if outside == nil {
					outside = map[string]bool{}
				}
				outside[v] = true
			}
		}
		if len(outside) == 0 {
			continue
		}
		piece := rem.Clone()
		if piece.Values == nil {
			piece.Values = map[string]map[string]bool{}
		}
		piece.Values[dim.Name] = outside
		out = append(out, piece)
		inside := map[string]bool{}
		for v := range aVals {
			if bVals[v] {
				inside[v] = true
			}
		}
		rem.Values[dim.Name] = inside
	}
	// rem now equals a ∩ b and is dropped.
	return out
}

// subtractAll subtracts every rectangle in subs from each rectangle
// in rects, preserving disjointness of the input.
func (d Domain) subtractAll(rects, subs []Rect) []Rect {
	out := rects
	for _, s := range subs {
		var next []Rect
		for _, r := range out {
			next = append(next, d.subtract(r, s)...)
		}
		out = next
		if len(out) == 0 {
			break
		}
	}
	return out
}
