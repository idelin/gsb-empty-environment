// Package arcview answers visibility queries on a circular camera
// field of view of [0, 360) degrees. Occluders are clockwise arcs
// given by a rational start angle and a rational clockwise span.
// Height layers map to priorities: for every angle the visible object
// is the covering occluder with the highest priority, ties broken by
// the smallest ID. All arithmetic is exact (rational).
package arcview

import (
	"math/big"
	"sort"
)

var fullCircle = big.NewRat(360, 1)

// Occluder blocks the clockwise arc [Start, Start+Span) of the view.
// The arc is left-closed right-open. A Span of 0 blocks nothing; a
// Span of 360 blocks the whole circle (never treated as empty).
type Occluder struct {
	ID       int
	Start    *big.Rat // degrees; normalized into [0, 360) on Upsert
	Span     *big.Rat // degrees; <= 0 means empty, >= 360 means full circle
	Priority int      // higher priority occludes lower ones
}

// Segment is one maximal arc of the view that sees the same object.
// It covers [Start, Start+Span) clockwise, left-closed right-open,
// and may cross the 0/360 boundary. ID is -1 where nothing occludes.
type Segment struct {
	Start *big.Rat // degrees in [0, 360)
	Span  *big.Rat // degrees in (0, 360]
	ID    int
}

// Scene is a mutable set of occluders supporting add/modify/delete.
type Scene struct {
	occs map[int]Occluder
}

// NewScene returns an empty scene.
func NewScene() *Scene { return &Scene{occs: make(map[int]Occluder)} }

// Upsert adds a new occluder or replaces the one with the same ID.
func (s *Scene) Upsert(o Occluder) {
	if o.Start == nil {
		o.Start = new(big.Rat)
	}
	if o.Span == nil {
		o.Span = new(big.Rat)
	}
	o.Start = normAngle(o.Start)
	o.Span = new(big.Rat).Set(o.Span)
	s.occs[o.ID] = o
}

// Delete removes the occluder with the given ID, if present.
func (s *Scene) Delete(id int) { delete(s.occs, id) }

// Query returns the ID of the object visible at angle, or -1 if none.
func (s *Scene) Query(angle *big.Rat) int {
	a := normAngle(angle)
	best, bestPrio := -1, 0
	for _, o := range s.occs {
		if !o.covers(a) {
			continue
		}
		if best == -1 || o.Priority > bestPrio || (o.Priority == bestPrio && o.ID < best) {
			best, bestPrio = o.ID, o.Priority
		}
	}
	return best
}

// covers reports whether the occluder blocks angle (normalized to
// [0, 360)), honoring left-closed right-open semantics.
func (o Occluder) covers(angle *big.Rat) bool {
	if o.Span.Sign() <= 0 {
		return false
	}
	if o.Span.Cmp(fullCircle) >= 0 {
		return true
	}
	off := normAngle(new(big.Rat).Sub(angle, o.Start))
	return off.Cmp(o.Span) < 0
}

// Segments sweeps the circle and returns the minimal arc segmentation
// of [0, 360): adjacent segments always have different IDs, and the
// segmentation is merged across the 0/360 boundary. A fully covered
// (or fully empty) circle yields one Segment with Span 360.
func (s *Scene) Segments() []Segment {
	type arc struct {
		lo, hi   *big.Rat
		id, prio int
	}
	var arcs []arc
	for _, o := range s.occs {
		if o.Span.Sign() <= 0 {
			continue
		}
		if o.Span.Cmp(fullCircle) >= 0 {
			arcs = append(arcs, arc{new(big.Rat), new(big.Rat).Set(fullCircle), o.ID, o.Priority})
			continue
		}
		lo := normAngle(o.Start)
		hi := new(big.Rat).Add(lo, o.Span)
		if hi.Cmp(fullCircle) <= 0 {
			arcs = append(arcs, arc{lo, hi, o.ID, o.Priority})
		} else {
			arcs = append(arcs, arc{lo, new(big.Rat).Set(fullCircle), o.ID, o.Priority})
			arcs = append(arcs, arc{new(big.Rat), new(big.Rat).Sub(hi, fullCircle), o.ID, o.Priority})
		}
	}

	pts := []*big.Rat{new(big.Rat), new(big.Rat).Set(fullCircle)}
	for _, a := range arcs {
		pts = append(pts, a.lo, a.hi)
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].Cmp(pts[j]) < 0 })
	uniq := pts[:1]
	for _, p := range pts[1:] {
		if p.Cmp(uniq[len(uniq)-1]) != 0 {
			uniq = append(uniq, p)
		}
	}
	pts = uniq

	starts := map[string][]int{}
	ends := map[string][]int{}
	for i, a := range arcs {
		starts[a.lo.RatString()] = append(starts[a.lo.RatString()], i)
		ends[a.hi.RatString()] = append(ends[a.hi.RatString()], i)
	}

	type labeled struct {
		lo, hi *big.Rat
		id     int
	}
	active := map[int]bool{}
	var merged []labeled
	push := func(seg labeled) {
		if n := len(merged); n > 0 && merged[n-1].id == seg.id {
			merged[n-1].hi = seg.hi
		} else {
			merged = append(merged, seg)
		}
	}
	for i := 0; i+1 < len(pts); i++ {
		key := pts[i].RatString()
		for _, idx := range ends[key] { // right-open: ending arcs leave first
			delete(active, idx)
		}
		for _, idx := range starts[key] { // left-closed: starting arcs join
			active[idx] = true
		}
		winner, wprio := -1, 0
		for idx := range active {
			a := arcs[idx]
			if winner == -1 || a.prio > wprio || (a.prio == wprio && a.id < winner) {
				winner, wprio = a.id, a.prio
			}
		}
		push(labeled{pts[i], pts[i+1], winner})
	}

	// Merge across the 0/360 boundary: the merged segment keeps the
	// last piece's start and wraps around to the first piece's end.
	if len(merged) > 1 && merged[0].id == merged[len(merged)-1].id {
		merged[0].lo = merged[len(merged)-1].lo
		merged = merged[:len(merged)-1]
	}

	segs := make([]Segment, len(merged))
	for i, m := range merged {
		span := new(big.Rat).Sub(m.hi, m.lo)
		if span.Sign() <= 0 {
			span.Add(span, fullCircle)
		}
		segs[i] = Segment{Start: m.lo, Span: span, ID: m.id}
	}
	return segs
}

// normAngle reduces a rational degree measure into [0, 360).
func normAngle(a *big.Rat) *big.Rat {
	turns := new(big.Rat).Quo(a, fullCircle)
	f := floorRat(turns)
	return new(big.Rat).Sub(a, new(big.Rat).SetInt(f.Mul(f, big.NewInt(360))))
}

// floorRat returns floor(x) as an integer.
func floorRat(x *big.Rat) *big.Int {
	q := new(big.Int).Quo(x.Num(), x.Denom()) // truncated toward zero
	if x.Sign() < 0 && new(big.Int).Rem(x.Num(), x.Denom()).Sign() != 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q
}
