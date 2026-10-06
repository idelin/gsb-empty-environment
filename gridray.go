// Package gridray enumerates the closed grid cells touched by a segment
// whose endpoints have rational coordinates, using exact arithmetic.
package gridray

import (
	"math/big"
	"sort"
)

// Point is a 2D point with exact rational coordinates.
type Point struct {
	X, Y *big.Rat
}

// Cell identifies the closed unit square [X,X+1] x [Y,Y+1].
type Cell struct {
	X, Y int64
}

// Visit is a touched cell together with the earliest segment parameter
// t in [0,1] at which the segment touches the closed cell.
type Visit struct {
	Cell Cell
	T    *big.Rat
}

// Rat builds a rational from a numerator and denominator.
func Rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

// Pt builds a Point from integer coordinates.
func Pt(x, y int64) Point { return Point{big.NewRat(x, 1), big.NewRat(y, 1)} }

// PtFrac builds a Point from fractional coordinates.
func PtFrac(xn, xd, yn, yd int64) Point {
	return Point{big.NewRat(xn, xd), big.NewRat(yn, yd)}
}

func addRat(a, b *big.Rat) *big.Rat { return new(big.Rat).Add(a, b) }
func subRat(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }
func mulRat(a, b *big.Rat) *big.Rat { return new(big.Rat).Mul(a, b) }

// floorBig returns floor(r) as a big.Int.
func floorBig(r *big.Rat) *big.Int {
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem)
	if rem.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q
}

// floorRat returns floor(r) as an int64.
func floorRat(r *big.Rat) int64 { return floorBig(r).Int64() }

// isInt reports whether r is an integer.
func isInt(r *big.Rat) bool { return r.Denom().Cmp(big.NewInt(1)) == 0 }

// cellsTouching1D returns the 1D cell indices whose closed interval
// contains v: two indices on a grid line, one otherwise.
func cellsTouching1D(v *big.Rat) []int64 {
	f := floorBig(v).Int64()
	if isInt(v) {
		return []int64{f - 1, f}
	}
	return []int64{f}
}

// axisEvents returns the parameters t in (0,1) at which p0+t*d crosses an
// integer grid line.
func axisEvents(p0, d *big.Rat) []*big.Rat {
	if d.Sign() == 0 {
		return nil
	}
	p1 := addRat(p0, d)
	lo, hi := p0, p1
	if lo.Cmp(hi) > 0 {
		lo, hi = hi, lo
	}
	// Integers k strictly between lo and hi; endpoints are covered by the
	// t=0 and t=1 point events.
	kLo := new(big.Int).Add(floorBig(lo), big.NewInt(1))
	kHi := floorBig(hi)
	if isInt(hi) {
		kHi.Sub(kHi, big.NewInt(1))
	}
	var out []*big.Rat
	one := big.NewRat(1, 1)
	for k := new(big.Int).Set(kLo); k.Cmp(kHi) <= 0; k.Add(k, big.NewInt(1)) {
		t := new(big.Rat).Quo(subRat(new(big.Rat).SetInt(k), p0), d)
		if t.Sign() > 0 && t.Cmp(one) < 0 {
			out = append(out, t)
		}
	}
	return out
}

// eval returns p0 + t*d.
func eval(p0, d, t *big.Rat) *big.Rat { return addRat(p0, mulRat(d, t)) }

// TraverseSegment lists every closed grid cell touched by the segment from
// p0 to p0+... to p1, i.e. P(t) = p0 + t*(p1-p0) for t in [0,1]. A cell is
// included when the segment merely touches its boundary; at a grid point
// all four adjacent cells are included. Results carry the first-contact
// parameter and are sorted by (T, Cell.X, Cell.Y) with no duplicates.
// A zero-length segment reports the cells containing the point.
func TraverseSegment(p0, p1 Point) []Visit {
	dx := subRat(p1.X, p0.X)
	dy := subRat(p1.Y, p0.Y)

	eventSet := map[string]*big.Rat{}
	addEvent := func(t *big.Rat) { eventSet[t.RatString()] = t }
	addEvent(big.NewRat(0, 1))
	addEvent(big.NewRat(1, 1))
	for _, t := range axisEvents(p0.X, dx) {
		addEvent(t)
	}
	for _, t := range axisEvents(p0.Y, dy) {
		addEvent(t)
	}
	events := make([]*big.Rat, 0, len(eventSet))
	for _, t := range eventSet {
		events = append(events, t)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Cmp(events[j]) < 0 })

	first := map[Cell]*big.Rat{}
	touch := func(c Cell, t *big.Rat) {
		if _, ok := first[c]; !ok {
			first[c] = t
		}
	}
	touchPoint := func(t *big.Rat) {
		x := eval(p0.X, dx, t)
		y := eval(p0.Y, dy, t)
		for _, cx := range cellsTouching1D(x) {
			for _, cy := range cellsTouching1D(y) {
				touch(Cell{cx, cy}, t)
			}
		}
	}

	for i, t := range events {
		touchPoint(t)
		if i+1 < len(events) {
			// Open interval between consecutive events: the segment lies
			// in the interior of a single cell, entered at t.
			tm := new(big.Rat).Quo(addRat(t, events[i+1]), big.NewRat(2, 1))
			c := Cell{floorRat(eval(p0.X, dx, tm)), floorRat(eval(p0.Y, dy, tm))}
			touch(c, t)
		}
	}

	visits := make([]Visit, 0, len(first))
	for c, t := range first {
		visits = append(visits, Visit{Cell: c, T: t})
	}
	SortVisits(visits)
	return visits
}

// SortVisits orders visits by first-contact parameter, then cell coordinates.
func SortVisits(vs []Visit) {
	sort.Slice(vs, func(i, j int) bool {
		if c := vs[i].T.Cmp(vs[j].T); c != 0 {
			return c < 0
		}
		if vs[i].Cell.X != vs[j].Cell.X {
			return vs[i].Cell.X < vs[j].Cell.X
		}
		return vs[i].Cell.Y < vs[j].Cell.Y
	})
}
