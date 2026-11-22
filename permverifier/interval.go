package permverifier

// Interval is a half-open physical or logical range [Lo, Lo+Len).
// All ranges are 64-bit; construction that would wrap past 2^64-1 is rejected.
type Interval struct {
	Lo  uint64
	Len uint64
}

// Empty reports whether the interval contains no byte.
func (i Interval) Empty() bool { return i.Len == 0 }

// End returns the exclusive upper bound and false if it would overflow.
func (i Interval) End() (uint64, bool) {
	if i.Len == 0 {
		return i.Lo, true
	}
	end, carry := add64(i.Lo, i.Len)
	return end, !carry
}

// Contains reports whether x lies inside [Lo, Lo+Len).
func (i Interval) Contains(x uint64) bool {
	end, ok := i.End()
	if !ok {
		return x >= i.Lo
	}
	return x >= i.Lo && x < end
}

// Overlaps reports whether two half-open intervals share any byte.
func (i Interval) Overlaps(o Interval) bool {
	iLast, iTop := i.lastByte()
	oLast, oTop := o.lastByte()
	if i.Lo > oLast && !(oTop && i.Lo == 0) {
		return false
	}
	if o.Lo > iLast && !(iTop && o.Lo == 0) {
		return false
	}
	return true
}

// lastByte returns the inclusive final byte and true when the interval ends
// exactly at 2^64 (so no ordinary end address exists).
func (i Interval) lastByte() (uint64, bool) {
	if i.Len == 0 {
		return i.Lo, false
	}
	endMinus1, carry := add64(i.Lo, i.Len-1)
	return endMinus1, carry
}

// Intersection returns the shared half-open range; it is empty when disjoint.
func (i Interval) Intersection(o Interval) Interval {
	if !i.Overlaps(o) {
		return Interval{}
	}
	lo := i.Lo
	if o.Lo > lo {
		lo = o.Lo
	}
	ie, iok := i.End()
	oe, ook := o.End()
	switch {
	case !iok && !ook:
		return Interval{Lo: lo, Len: mathMaxUint64 - lo + 1}
	case !iok:
		return Interval{Lo: lo, Len: oe - lo}
	case !ook:
		return Interval{Lo: lo, Len: ie - lo}
	default:
		hi := ie
		if oe < hi {
			hi = oe
		}
		return Interval{Lo: lo, Len: hi - lo}
	}
}

const mathMaxUint64 = ^uint64(0)

// add64 returns (a+b, overflow) where overflow indicates a 64-bit wrap.
func add64(a, b uint64) (uint64, bool) {
	s := a + b
	return s, s < a
}

// sub64 returns (a-b, underflow) where underflow indicates a negative result.
func sub64(a, b uint64) (uint64, bool) {
	if b > a {
		return 0, true
	}
	return a - b, false
}

// mul64 returns (a*b, overflow) where overflow indicates a 64-bit wrap.
func mul64(a, b uint64) (uint64, bool) {
	if a == 0 || b == 0 {
		return 0, false
	}
	p := a * b
	return p, p/a != b
}

// frag is a physical range attributed to one view while enumerating coverage.
type frag struct {
	phys Interval
	ref  ViewRef
}

type fragKey struct {
	id  uint64
	gen uint64
}

// mergeFrags unions fragments into disjoint physical intervals, annotating each
// with every contributing view in deterministic (view id, generation) order.
func mergeFrags(in []frag) []coveragePiece {
	type point struct {
		pos  uint64
		ends []frag
		add  []frag
	}
	points := map[uint64]*point{}
	get := func(p uint64) *point {
		q, ok := points[p]
		if !ok {
			q = &point{pos: p}
			points[p] = q
		}
		return q
	}
	for _, f := range in {
		end, ok := f.phys.End()
		if !ok {
			// Intervals wrapping past max uint64 end at the inclusive last byte.
			end = 0
		}
		get(f.phys.Lo).add = append(get(f.phys.Lo).add, f)
		if end != 0 {
			get(end).ends = append(get(end).ends, f)
		}
	}
	positions := make([]uint64, 0, len(points))
	for p := range points {
		positions = append(positions, p)
	}
	sortU64(positions)

	active := map[fragKey]frag{}
	var out []coveragePiece
	var prev uint64
	for idx, pos := range positions {
		if idx > 0 {
			if len(active) > 0 && pos > prev {
				out = append(out, buildPiece(prev, pos, active))
			}
		}
		for _, f := range points[pos].ends {
			delete(active, fragKey{f.ref.ID, f.ref.Gen})
		}
		for _, f := range points[pos].add {
			active[fragKey{f.ref.ID, f.ref.Gen}] = f
		}
		prev = pos
	}
	if len(active) > 0 {
		out = append(out, buildTailPiece(prev, active))
	}
	return out
}

func buildPiece(lo, hi uint64, active map[fragKey]frag) coveragePiece {
	p := coveragePiece{Phys: Interval{Lo: lo, Len: hi - lo}}
	fillPiece(&p, active)
	return p
}

func buildTailPiece(lo uint64, active map[fragKey]frag) coveragePiece {
	p := coveragePiece{Phys: Interval{Lo: lo, Len: mathMaxUint64 - lo + 1}}
	fillPiece(&p, active)
	return p
}

func fillPiece(p *coveragePiece, active map[fragKey]frag) {
	for _, f := range active {
		p.Refs = append(p.Refs, f.ref)
		if f.ref.Perm&PermWrite != 0 {
			p.W = true
		}
		if f.ref.Perm&PermExec != 0 {
			p.X = true
		}
	}
	sortRefs(p.Refs)
}

func sortU64(a []uint64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func sortRefs(a []ViewRef) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0; j-- {
			if a[j-1].ID < a[j].ID {
				break
			}
			if a[j-1].ID == a[j].ID && a[j-1].Gen <= a[j].Gen {
				break
			}
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
