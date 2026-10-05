package segint

import (
	"math/big"
	"sort"
)

// accumulator gathers pairwise intersections and merges them into the
// final grouped result.
type accumulator struct {
	points   map[string]Point            // point key -> point
	pointIDs map[string]map[int]struct{} // point key -> participating IDs
	lines    map[string]*lineAgg         // line key -> overlap intervals
}

func newAccumulator() *accumulator {
	return &accumulator{
		points:   make(map[string]Point),
		pointIDs: make(map[string]map[int]struct{}),
		lines:    make(map[string]*lineAgg),
	}
}

func (ac *accumulator) addPair(a, b Segment) {
	pi := pairIntersect(a, b)
	switch pi.kind {
	case pairPoint:
		k := pointKey(pi.point)
		ac.points[k] = pi.point
		if ac.pointIDs[k] == nil {
			ac.pointIDs[k] = make(map[int]struct{})
		}
		ac.pointIDs[k][a.ID] = struct{}{}
		ac.pointIDs[k][b.ID] = struct{}{}
	case pairOverlap:
		key, la := lineAggFor(pi.lo, pi.hi)
		if ac.lines[key] == nil {
			ac.lines[key] = la
		} else {
			la = ac.lines[key]
		}
		la.intervals = append(la.intervals, lineInterval{
			lo:  la.param(pi.lo),
			hi:  la.param(pi.hi),
			ids: map[int]struct{}{a.ID: {}, b.ID: {}},
		})
	}
}

func (ac *accumulator) result() Result {
	var r Result
	for k, p := range ac.points {
		r.Points = append(r.Points, PointIntersection{Point: p, SegmentIDs: sortedIDs(ac.pointIDs[k])})
	}
	for _, la := range ac.lines {
		r.Overlaps = append(r.Overlaps, la.build()...)
	}
	sortPoints(r.Points)
	sortOverlaps(r.Overlaps)
	return r
}

// lineAgg accumulates 1D overlap intervals on one supporting line.
type lineAgg struct {
	a, b, c   *big.Rat // canonical line equation a*x + b*y = c
	vertical  bool
	intervals []lineInterval
}

type lineInterval struct {
	lo, hi *big.Rat // parameter along the line, lo < hi
	ids    map[int]struct{}
}

// param returns the 1D coordinate of p along the line.
func (la *lineAgg) param(p Point) *big.Rat {
	if la.vertical {
		return p.Y
	}
	return p.X
}

// point reconstructs the 2D point on the line for a parameter value.
func (la *lineAgg) point(t *big.Rat) Point {
	if la.vertical {
		// b == 0, so x = c / a.
		return Point{X: new(big.Rat).Quo(la.c, la.a), Y: t}
	}
	// y = (c - a*x) / b
	y := new(big.Rat).Mul(la.a, t)
	y = new(big.Rat).Sub(la.c, y)
	y = y.Quo(y, la.b)
	return Point{X: t, Y: y}
}

// lineAggFor returns the canonical supporting line through p and q
// (p != q), keyed by the normalized integer equation a*x + b*y = c.
func lineAggFor(p, q Point) (string, *lineAgg) {
	a := ratSub(p.Y, q.Y)
	b := ratSub(q.X, p.X)
	c := new(big.Rat).Add(new(big.Rat).Mul(a, p.X), new(big.Rat).Mul(b, p.Y))
	ai, bi, ci := normalizeLine(a, b, c)
	key := ai.String() + ";" + bi.String() + ";" + ci.String()
	la := &lineAgg{
		a:        new(big.Rat).SetInt(ai),
		b:        new(big.Rat).SetInt(bi),
		c:        new(big.Rat).SetInt(ci),
		vertical: bi.Sign() == 0,
	}
	return key, la
}

// normalizeLine scales rational coefficients to integers and reduces them
// so that (a, b, c) is canonical: gcd(|a|,|b|,|c|) == 1 and the first
// non-zero coefficient is positive.
func normalizeLine(a, b, c *big.Rat) (ai, bi, ci *big.Int) {
	d := lcm3(a.Denom(), b.Denom(), c.Denom())
	scale := func(r *big.Rat) *big.Int {
		return new(big.Int).Quo(new(big.Int).Mul(r.Num(), d), r.Denom())
	}
	ai, bi, ci = scale(a), scale(b), scale(c)
	g := gcdAbs(gcdAbs(ai, bi), ci)
	if g.Sign() > 0 {
		ai.Quo(ai, g)
		bi.Quo(bi, g)
		ci.Quo(ci, g)
	}
	if ai.Sign() < 0 || (ai.Sign() == 0 && bi.Sign() < 0) ||
		(ai.Sign() == 0 && bi.Sign() == 0 && ci.Sign() < 0) {
		ai.Neg(ai)
		bi.Neg(bi)
		ci.Neg(ci)
	}
	return ai, bi, ci
}

// gcdAbs returns gcd(|a|, |b|), with gcd(0, 0) == 0. (big.Int.GCD with a
// zero argument is unreliable in older toolchains, so roll our own.)
func gcdAbs(a, b *big.Int) *big.Int {
	a = new(big.Int).Abs(a)
	b = new(big.Int).Abs(b)
	for b.Sign() != 0 {
		a, b = b, new(big.Int).Mod(a, b)
	}
	return a
}

func lcm3(x, y, z *big.Int) *big.Int {
	lcm := func(a, b *big.Int) *big.Int {
		g := gcdAbs(a, b)
		return new(big.Int).Mul(new(big.Int).Quo(a, g), b)
	}
	return lcm(lcm(x, y), z)
}

// build merges the accumulated intervals into maximal atomic overlap
// pieces, each carrying the exact set of covering segment IDs.
func (la *lineAgg) build() []OverlapSegment {
	uniq := make(map[string]*big.Rat)
	for _, iv := range la.intervals {
		uniq[iv.lo.String()] = iv.lo
		uniq[iv.hi.String()] = iv.hi
	}
	ends := make([]*big.Rat, 0, len(uniq))
	for _, v := range uniq {
		ends = append(ends, v)
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i].Cmp(ends[j]) < 0 })

	var out []OverlapSegment
	for k := 0; k+1 < len(ends); k++ {
		lo, hi := ends[k], ends[k+1]
		ids := make(map[int]struct{})
		for _, iv := range la.intervals {
			if iv.lo.Cmp(lo) <= 0 && iv.hi.Cmp(hi) >= 0 {
				for id := range iv.ids {
					ids[id] = struct{}{}
				}
			}
		}
		if len(ids) < 2 {
			continue
		}
		// Merge with the previous piece when the covering set is identical.
		if n := len(out); n > 0 && sameIDs(out[n-1].SegmentIDs, ids) && la.param(out[n-1].End).Cmp(lo) == 0 {
			out[n-1].End = la.point(hi)
			continue
		}
		out = append(out, OverlapSegment{
			Start:      la.point(lo),
			End:        la.point(hi),
			SegmentIDs: sortedIDs(ids),
		})
	}
	return out
}

func sameIDs(sorted []int, set map[int]struct{}) bool {
	if len(sorted) != len(set) {
		return false
	}
	for _, id := range sorted {
		if _, ok := set[id]; !ok {
			return false
		}
	}
	return true
}
