// Package occlusion implements an angular-domain query library for a camera
// with a circular field of view [0, 360).
//
// Occluders are described by a rational start angle and a clockwise rational
// span in [0, 360]. A span of 0 is empty; a span of 360 is the full circle
// (never confused with empty, even though its endpoints coincide). Intervals
// are left-closed, right-open: [start, start+span). Intervals may cross the
// 0-degree meridian. Occluders carry a priority (height layer); for every
// visible angle the library reports the highest-priority occluder, breaking
// ties by the smallest object ID. Angles covered by no occluder report
// NoObject.
//
// All arithmetic on angles is exact rational arithmetic (math/big.Rat), so
// endpoint comparisons never suffer from floating-point error.
package occlusion

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// NoObject is the winner reported for angles covered by no occluder.
const NoObject = -1

var (
	rat360 = big.NewRat(360, 1)
	rat0   = big.NewRat(0, 1)
	rat2   = big.NewRat(2, 1)
)

// Rat builds a rational angle from a numerator and denominator.
func Rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

func cloneRat(x *big.Rat) *big.Rat { return new(big.Rat).Set(x) }

// ModAngle reduces an angle into the canonical range [0, 360).
func ModAngle(a *big.Rat) *big.Rat {
	q := new(big.Rat).Quo(a, rat360)
	num, den := q.Num(), q.Denom()
	floor := new(big.Int).Quo(num, den) // truncated toward zero
	if new(big.Int).Rem(num, den).Sign() != 0 && q.Sign() < 0 {
		floor.Sub(floor, big.NewInt(1))
	}
	r := new(big.Rat).Sub(a, new(big.Rat).Mul(new(big.Rat).SetInt(floor), rat360))
	if r.Cmp(rat360) >= 0 || r.Sign() < 0 { // defensive; should not happen
		return cloneRat(rat0)
	}
	return r
}

// Occluder is a single occluding object: a clockwise arc
// [Start, Start+Span) on the circle, at a given priority layer.
type Occluder struct {
	ID       int
	Priority int      // higher priority occludes lower priority
	Start    *big.Rat // rational degrees, any value (normalized internally)
	Span     *big.Rat // rational degrees in [0, 360]
}

// contains reports whether canonical angle a (already in [0,360)) lies in
// the occluder's left-closed, right-open arc.
func (o Occluder) contains(a *big.Rat) bool {
	if o.Span.Sign() == 0 {
		return false
	}
	if o.Span.Cmp(rat360) == 0 {
		return true // full circle, not empty
	}
	s := ModAngle(o.Start)
	e := ModAngle(new(big.Rat).Add(o.Start, o.Span))
	if s.Cmp(e) < 0 {
		return a.Cmp(s) >= 0 && a.Cmp(e) < 0
	}
	// crosses the 0 meridian
	return a.Cmp(s) >= 0 || a.Cmp(e) < 0
}

// Segment is one maximal clockwise arc [Start, Start+Span) on which a single
// winner is reported. Start lies in [0, 360); Span lies in (0, 360]. A
// segment with Span == 360 covers the whole circle. Winner is an occluder ID
// or NoObject.
type Segment struct {
	Start  *big.Rat
	Span   *big.Rat
	Winner int
}

// End returns the (normalized) right-open end angle of the segment.
func (s Segment) End() *big.Rat {
	return ModAngle(new(big.Rat).Add(s.Start, s.Span))
}

// Full reports whether the segment covers the entire circle.
func (s Segment) Full() bool { return s.Span.Cmp(rat360) == 0 }

func (s Segment) contains(a *big.Rat) bool {
	if s.Full() {
		return true
	}
	e := s.End()
	if s.Start.Cmp(e) < 0 {
		return a.Cmp(s.Start) >= 0 && a.Cmp(e) < 0
	}
	return a.Cmp(s.Start) >= 0 || a.Cmp(e) < 0
}

func (s Segment) String() string {
	return fmt.Sprintf("[%s, +%s) -> %d", s.Start.RatString(), s.Span.RatString(), s.Winner)
}

// Map is a mutable set of occluders supporting angular winner queries.
type Map struct {
	occluders map[int]Occluder
}

// New returns an empty occlusion map.
func New() *Map { return &Map{occluders: make(map[int]Occluder)} }

// Upsert inserts a new occluder or replaces the one with the same ID
// (modify). The occluder is copied; the caller may reuse the struct.
func (m *Map) Upsert(o Occluder) error {
	if o.Start == nil || o.Span == nil {
		return errors.New("occlusion: nil Start or Span")
	}
	if o.Span.Sign() < 0 || o.Span.Cmp(rat360) > 0 {
		return fmt.Errorf("occlusion: span %s outside [0, 360]", o.Span.RatString())
	}
	o.Start = cloneRat(o.Start)
	o.Span = cloneRat(o.Span)
	m.occluders[o.ID] = o
	return nil
}

// Remove deletes the occluder with the given ID, reporting whether it existed.
func (m *Map) Remove(id int) bool {
	_, ok := m.occluders[id]
	delete(m.occluders, id)
	return ok
}

// Len returns the number of occluders.
func (m *Map) Len() int { return len(m.occluders) }

// WinnerAt returns the ID of the highest-priority occluder covering the
// angle (ties broken by smallest ID), or NoObject. The angle may be any
// rational; it is normalized into [0, 360).
func (m *Map) WinnerAt(angle *big.Rat) int {
	a := ModAngle(angle)
	best := NoObject
	bestPrio := 0
	for _, o := range m.occluders {
		if !o.contains(a) {
			continue
		}
		if best == NoObject || o.Priority > bestPrio || (o.Priority == bestPrio && o.ID < best) {
			best, bestPrio = o.ID, o.Priority
		}
	}
	return best
}

// breakpoints collects the canonical start/end angles of every non-empty,
// non-full occluder.
func (m *Map) breakpoints() []*big.Rat {
	set := map[string]*big.Rat{}
	for _, o := range m.occluders {
		if o.Span.Sign() == 0 || o.Span.Cmp(rat360) == 0 {
			continue
		}
		s := ModAngle(o.Start)
		e := ModAngle(new(big.Rat).Add(o.Start, o.Span))
		set[s.RatString()] = s
		set[e.RatString()] = e
	}
	pts := make([]*big.Rat, 0, len(set))
	for _, p := range set {
		pts = append(pts, p)
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].Cmp(pts[j]) < 0 })
	return pts
}

// Segments returns the minimal clockwise segmentation of the full circle
// [0, 360) into arcs with a constant winner. Adjacent arcs (including across
// the 0 meridian) always have different winners; the segments together cover
// the whole circle exactly once.
func (m *Map) Segments() []Segment {
	pts := m.breakpoints()
	if len(pts) == 0 {
		// No boundary anywhere: the winner is constant on the whole circle.
		return []Segment{{Start: cloneRat(rat0), Span: cloneRat(rat360), Winner: m.WinnerAt(rat0)}}
	}

	type arc struct {
		start, span *big.Rat
		winner      int
	}
	n := len(pts)
	arcs := make([]arc, 0, n)
	for i, s := range pts {
		var span *big.Rat
		if i+1 < n {
			span = new(big.Rat).Sub(pts[i+1], s)
		} else {
			// wrap-around arc from the last breakpoint to the first
			span = new(big.Rat).Add(new(big.Rat).Sub(rat360, s), pts[0])
		}
		mid := ModAngle(new(big.Rat).Add(s, new(big.Rat).Quo(span, rat2)))
		arcs = append(arcs, arc{s, span, m.WinnerAt(mid)})
	}

	// Cyclic merge: if every arc has the same winner, the circle is one segment.
	allSame := true
	for i := 1; i < n; i++ {
		if arcs[i].winner != arcs[0].winner {
			allSame = false
			break
		}
	}
	if allSame {
		return []Segment{{Start: cloneRat(rat0), Span: cloneRat(rat360), Winner: arcs[0].winner}}
	}

	// Rotate so the first arc differs from its cyclic predecessor, then merge
	// linearly; this also merges across the 0 meridian.
	k := 0
	for i := 0; i < n; i++ {
		if arcs[i].winner != arcs[(i-1+n)%n].winner {
			k = i
			break
		}
	}
	rot := make([]arc, 0, n)
	rot = append(rot, arcs[k:]...)
	rot = append(rot, arcs[:k]...)

	var segs []Segment
	cur := rot[0]
	for _, a := range rot[1:] {
		if a.winner == cur.winner {
			cur.span.Add(cur.span, a.span)
			continue
		}
		segs = append(segs, Segment{Start: cur.start, Span: cur.span, Winner: cur.winner})
		cur = a
	}
	segs = append(segs, Segment{Start: cur.start, Span: cur.span, Winner: cur.winner})
	return segs
}

// CheckConsistency verifies Segments() against a naive model that samples a
// rational point strictly between every pair of consecutive angle endpoints
// and evaluates the winner by brute force. It also checks exact coverage of
// the circle, left-closed endpoint ownership, and segmentation minimality.
// It returns nil when the segment model and the naive model agree.
func (m *Map) CheckConsistency() error {
	segs := m.Segments()

	// Exact coverage of the circle, positive spans, naive agreement at each
	// segment's midpoint and at its left-closed start point.
	total := cloneRat(rat0)
	for _, s := range segs {
		if s.Span.Sign() <= 0 || s.Span.Cmp(rat360) > 0 {
			return fmt.Errorf("segment %v has span outside (0, 360]", s)
		}
		total.Add(total, s.Span)
		mid := ModAngle(new(big.Rat).Add(s.Start, new(big.Rat).Quo(s.Span, rat2)))
		if w := m.WinnerAt(mid); w != s.Winner {
			return fmt.Errorf("segment %v: naive winner at midpoint %s is %d", s, mid.RatString(), w)
		}
		if w := m.WinnerAt(s.Start); w != s.Winner {
			return fmt.Errorf("segment %v: naive winner at left-closed start is %d", s, w)
		}
	}
	if total.Cmp(rat360) != 0 {
		return fmt.Errorf("segments cover %s degrees, not 360", total.RatString())
	}

	// Minimality: cyclically adjacent segments must differ.
	if n := len(segs); n > 1 {
		for i := 0; i < n; i++ {
			if segs[i].Winner == segs[(i+1)%n].Winner {
				return fmt.Errorf("non-minimal segmentation at %v and %v", segs[i], segs[(i+1)%n])
			}
		}
	}

	// Naive cross-check: sample between every pair of consecutive occluder
	// endpoints and locate the containing segment.
	pts := m.breakpoints()
	if len(pts) == 0 {
		pts = []*big.Rat{cloneRat(rat0)}
	}
	n := len(pts)
	for i, s := range pts {
		var span *big.Rat
		if i+1 < n {
			span = new(big.Rat).Sub(pts[i+1], s)
		} else {
			span = new(big.Rat).Add(new(big.Rat).Sub(rat360, s), pts[0])
		}
		mid := ModAngle(new(big.Rat).Add(s, new(big.Rat).Quo(span, rat2)))
		want := m.WinnerAt(mid)
		found := false
		for _, seg := range segs {
			if seg.contains(mid) {
				found = true
				if seg.Winner != want {
					return fmt.Errorf("at sample %s: segment winner %d, naive winner %d", mid.RatString(), seg.Winner, want)
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("no segment contains sample %s", mid.RatString())
		}
	}
	return nil
}
