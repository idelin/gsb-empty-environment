package revco

// intersect returns the overlap of two half-open ranges.
func intersect(a, b Range) Range {
	lo := a.Off
	if b.Off > lo {
		lo = b.Off
	}
	hi := a.End()
	if b.End() < hi {
		hi = b.End()
	}
	if hi <= lo {
		return Range{}
	}
	return Range{Off: lo, Len: hi - lo}
}

// subtract returns the portions of a not covered by b.
func subtract(a, b Range) []Range {
	ov := intersect(a, b)
	if ov.Empty() {
		if a.Empty() {
			return nil
		}
		return []Range{a}
	}
	var out []Range
	if ov.Off > a.Off {
		out = append(out, Range{Off: a.Off, Len: ov.Off - a.Off})
	}
	if ov.End() < a.End() {
		out = append(out, Range{Off: ov.End(), Len: a.End() - ov.End()})
	}
	return out
}

// mergeAdjacent sorts and merges touching or overlapping ranges.
func mergeAdjacent(in []Range) []Range {
	if len(in) < 2 {
		return in
	}
	rs := make([]Range, len(in))
	copy(rs, in)
	for i := 1; i < len(rs); i++ {
		key := rs[i]
		j := i - 1
		for j >= 0 && rs[j].Off > key.Off {
			rs[j+1] = rs[j]
			j--
		}
		rs[j+1] = key
	}
	out := rs[:1]
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r.Empty() {
			continue
		}
		if last.Empty() {
			*last = r
			continue
		}
		if r.Off <= last.End() {
			if r.End() > last.End() {
				last.Len = r.End() - last.Off
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// unionRanges merges a set of ranges.
func unionRanges(parts []Range) []Range {
	var nonEmpty []Range
	for _, r := range parts {
		if !r.Empty() {
			nonEmpty = append(nonEmpty, r)
		}
	}
	if len(nonEmpty) == 0 {
		return nil
	}
	return mergeAdjacent(nonEmpty)
}

// subtractSet returns ranges covered by a but not by any range in bs.
func subtractSet(a Range, bs []Range) []Range {
	pending := []Range{a}
	for _, b := range bs {
		var next []Range
		for _, p := range pending {
			next = append(next, subtract(p, b)...)
		}
		pending = next
		if len(pending) == 0 {
			return nil
		}
	}
	return unionRanges(pending)
}

// intersectSet returns the parts of a covered by any range in bs.
func intersectSet(a Range, bs []Range) []Range {
	var out []Range
	for _, b := range bs {
		if ov := intersect(a, b); !ov.Empty() {
			out = append(out, ov)
		}
	}
	return unionRanges(out)
}

func rightsSet(in []Right) map[Right]bool {
	out := make(map[Right]bool, len(in))
	for _, r := range in {
		out[r] = true
	}
	return out
}

func cloneRights(in map[Right]bool) map[Right]bool {
	out := make(map[Right]bool, len(in))
	for r, ok := range in {
		if ok {
			out[r] = true
		}
	}
	return out
}

func intersectRights(a, b map[Right]bool) map[Right]bool {
	out := make(map[Right]bool)
	for r := range a {
		if b[r] {
			out[r] = true
		}
	}
	return out
}
