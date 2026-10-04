package main

import (
	"fmt"
	"math/big"
	"sort"
)

// Object is a point moving with constant velocity: pos(t) = (X,Y) + t*(VX,VY).
type Object struct {
	ID           string
	X, Y, VX, VY *big.Rat
}

// Input is the full problem instance.
type Input struct {
	QueryX, QueryY *big.Rat
	T              *big.Rat
	K              int
	Objects        []Object
}

// DistQuad returns the squared distance to the query point as a quadratic
// polynomial in t with rational coefficients.
func (o Object) DistQuad(qx, qy *big.Rat) Quad {
	dx := new(big.Rat).Sub(o.X, qx)
	dy := new(big.Rat).Sub(o.Y, qy)
	a := new(big.Rat).Add(new(big.Rat).Mul(o.VX, o.VX), new(big.Rat).Mul(o.VY, o.VY))
	b := new(big.Rat).Add(new(big.Rat).Mul(o.VX, dx), new(big.Rat).Mul(o.VY, dy))
	b.Mul(b, ratInt(2))
	c := new(big.Rat).Add(new(big.Rat).Mul(dx, dx), new(big.Rat).Mul(dy, dy))
	return Quad{A: a, B: b, C: c}
}

type pair struct{ i, j int }

// Segment is an open time interval (Lo, Hi) on which the ranking is fixed.
type Segment struct {
	Lo, Hi  *Root
	Sample  *big.Rat // rational certificate point inside the segment
	Ranking []int    // full order, nearest first
}

// Event is an instant at which at least one pair is exactly tied.
type Event struct {
	Time    *Root
	Pairs   map[pair]bool // pairs whose distance difference vanishes at Time
	Ranking []int         // full order at Time, ties broken by ID
}

// Timeline is the complete answer: events at t0=0 < t1 < ... < tm=T and the
// open segments between consecutive events.
type Timeline struct {
	Events   []*Event
	Segments []*Segment // Segments[w] spans (Events[w].Time, Events[w+1].Time)
}

// Engine precomputes pairwise difference polynomials.
type Engine struct {
	in        Input
	n         int
	ids       []string
	polys     []Quad
	diff      [][]Quad // diff[i][j] = d_i - d_j for i < j
	identical [][]bool // diff[i][j] is the zero polynomial
}

// NewEngine builds the pairwise data for n objects. Enumerating all pairs
// is O(n^2), which is explicitly allowed.
func NewEngine(in Input) *Engine {
	e := &Engine{in: in, n: len(in.Objects)}
	e.polys = make([]Quad, e.n)
	e.ids = make([]string, e.n)
	for i, o := range in.Objects {
		e.polys[i] = o.DistQuad(in.QueryX, in.QueryY)
		e.ids[i] = o.ID
	}
	e.diff = make([][]Quad, e.n)
	e.identical = make([][]bool, e.n)
	for i := 0; i < e.n; i++ {
		e.diff[i] = make([]Quad, e.n)
		e.identical[i] = make([]bool, e.n)
		for j := i + 1; j < e.n; j++ {
			d := e.polys[i].Sub(e.polys[j])
			e.diff[i][j] = d
			e.identical[i][j] = d.IsZero()
		}
	}
	return e
}

// rankingAtRational sorts objects by exact squared distance at a rational
// time, ties broken by ID. Inside an open segment no pair can be tied
// unless their distance functions are identical.
func (e *Engine) rankingAtRational(s *big.Rat) []int {
	vals := make([]*big.Rat, e.n)
	for i := range vals {
		vals[i] = e.polys[i].Eval(s)
	}
	idx := make([]int, e.n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		if c := vals[idx[a]].Cmp(vals[idx[b]]); c != 0 {
			return c < 0
		}
		return e.ids[idx[a]] < e.ids[idx[b]]
	})
	return idx
}

// ComputeTimeline enumerates every pairwise crossing in [0, T] and builds
// the full ranking timeline.
func (e *Engine) ComputeTimeline() *Timeline {
	type entry struct {
		t    *Root
		p    pair
		hasP bool
	}
	entries := []entry{{t: RationalRoot(ratInt(0))}, {t: RationalRoot(e.in.T)}}
	for i := 0; i < e.n; i++ {
		for j := i + 1; j < e.n; j++ {
			if e.identical[i][j] {
				continue // never crosses; permanently tied, broken by ID
			}
			for _, r := range rootsOf(e.diff[i][j], e.in.T) {
				entries = append(entries, entry{t: r, p: pair{i, j}, hasP: true})
			}
		}
	}
	sort.Slice(entries, func(a, b int) bool {
		return CompareRoots(entries[a].t, entries[b].t) < 0
	})
	// Group entries that happen at the same exact time (multiple events
	// can coincide, e.g. several pairs crossing simultaneously).
	var times []*Root
	var pairSets []map[pair]bool
	for _, en := range entries {
		if len(times) > 0 && CompareRoots(times[len(times)-1], en.t) == 0 {
			if en.hasP {
				pairSets[len(pairSets)-1][en.p] = true
			}
			continue
		}
		times = append(times, en.t)
		ps := map[pair]bool{}
		if en.hasP {
			ps[en.p] = true
		}
		pairSets = append(pairSets, ps)
	}

	tl := &Timeline{}
	for w := 0; w+1 < len(times); w++ {
		lo, hi := times[w], times[w+1]
		CompareRoots(lo, hi) // force disjoint isolating intervals
		_, loHi := lo.bounds()
		hiLo, _ := hi.bounds()
		sample := new(big.Rat).Add(loHi, hiLo)
		sample.Quo(sample, ratInt(2)) // rational, strictly inside
		tl.Segments = append(tl.Segments, &Segment{
			Lo: lo, Hi: hi, Sample: sample,
			Ranking: e.rankingAtRational(sample),
		})
	}
	for w, t := range times {
		tl.Events = append(tl.Events, &Event{
			Time:    t,
			Pairs:   pairSets[w],
			Ranking: e.eventRanking(t, pairSets[w], tl.Segments, w),
		})
	}
	return tl
}

// eventRanking computes the exact order at an event time. A pair is tied
// iff its difference polynomial vanishes there (it is in the event's pair
// set, or the polynomials are identical); ties are broken by ID. Any other
// pair has a definite sign at the event time, read off exactly at a
// rational time or inherited from an adjacent open segment.
func (e *Engine) eventRanking(t *Root, tied map[pair]bool, segs []*Segment, w int) []int {
	var rightSample, leftSample *big.Rat
	if w < len(segs) {
		rightSample = segs[w].Sample
	}
	if w > 0 {
		leftSample = segs[w-1].Sample
	}
	idx := make([]int, e.n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		return e.compareAtEvent(idx[a], idx[b], t, tied, rightSample, leftSample) < 0
	})
	return idx
}

func (e *Engine) compareAtEvent(a, b int, t *Root, tied map[pair]bool, rs, ls *big.Rat) int {
	i, j := a, b
	flip := false
	if i > j {
		i, j = j, i
		flip = true
	}
	res := 0
	switch {
	case e.identical[i][j], tied[pair{i, j}]:
		res = 0
	default:
		var s int
		if t.rat != nil {
			s = e.diff[i][j].Eval(t.rat).Sign() // exact; nonzero by construction
		} else {
			sample := rs
			if sample == nil {
				sample = ls
			}
			// No root of diff[i][j] lies between t and the sample, so the
			// sign at the sample equals the sign at t.
			s = e.diff[i][j].Eval(sample).Sign()
		}
		res = s
	}
	if res == 0 { // exact tie: break by ID
		switch {
		case e.ids[a] < e.ids[b]:
			return -1
		case e.ids[a] > e.ids[b]:
			return 1
		}
		return 0
	}
	if flip {
		res = -res
	}
	return res
}

// mix returns (wa*a + wb*b) / (wa + wb), a rational strictly between a < b.
func mix(a, b *big.Rat, wa, wb int64) *big.Rat {
	r := new(big.Rat).Mul(a, ratInt(wa))
	r.Add(r, new(big.Rat).Mul(b, ratInt(wb)))
	return r.Quo(r, ratInt(wa+wb))
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Verify independently certifies a timeline:
//  1. events are strictly increasing and pinned to 0 and T;
//  2. every open segment contains no root of any pairwise difference
//     polynomial (no unhandled crossing), checked by recomputing all roots
//     and comparing them against the segment bounds;
//  3. the reported segment ranking is reproduced at three independent
//     rational sample points inside the segment;
//  4. every pair declared tied at an event really has a root there, and the
//     event ranking agrees with each adjacent segment on every untied pair.
func (e *Engine) Verify(tl *Timeline) error {
	if len(tl.Events) == 0 {
		return fmt.Errorf("no events")
	}
	if CompareRoots(tl.Events[0].Time, RationalRoot(ratInt(0))) != 0 {
		return fmt.Errorf("first event is not t=0")
	}
	if CompareRoots(tl.Events[len(tl.Events)-1].Time, RationalRoot(e.in.T)) != 0 {
		return fmt.Errorf("last event is not t=T")
	}
	if len(tl.Segments) != len(tl.Events)-1 {
		return fmt.Errorf("segment/event count mismatch")
	}
	for w := 1; w < len(tl.Events); w++ {
		if CompareRoots(tl.Events[w-1].Time, tl.Events[w].Time) >= 0 {
			return fmt.Errorf("events not strictly increasing at index %d", w)
		}
	}
	for si, seg := range tl.Segments {
		if CompareRoots(seg.Lo, RationalRoot(seg.Sample)) >= 0 ||
			CompareRoots(RationalRoot(seg.Sample), seg.Hi) >= 0 {
			return fmt.Errorf("segment %d: sample point not inside", si)
		}
		for i := 0; i < e.n; i++ {
			for j := i + 1; j < e.n; j++ {
				if e.identical[i][j] {
					continue
				}
				for _, r := range rootsOf(e.diff[i][j], e.in.T) {
					if CompareRoots(r, seg.Lo) > 0 && CompareRoots(r, seg.Hi) < 0 {
						return fmt.Errorf("segment %d: unhandled crossing of objects %s and %s",
							si, e.ids[i], e.ids[j])
					}
				}
			}
		}
		// Rational sampling certificate: three distinct rational points
		// must all reproduce the reported ranking.
		_, loHi := seg.Lo.bounds()
		hiLo, _ := seg.Hi.bounds()
		s1 := mix(loHi, hiLo, 3, 1)
		s2 := mix(loHi, hiLo, 1, 3)
		for _, s := range []*big.Rat{seg.Sample, s1, s2} {
			if got := e.rankingAtRational(s); !equalInts(got, seg.Ranking) {
				return fmt.Errorf("segment %d: ranking not reproduced at rational sample %s",
					si, ratStr(s))
			}
		}
	}
	for w, ev := range tl.Events {
		for p := range ev.Pairs {
			found := false
			for _, r := range rootsOf(e.diff[p.i][p.j], e.in.T) {
				if CompareRoots(r, ev.Time) == 0 {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("event %d: pair %s,%s declared tied but has no root there",
					w, e.ids[p.i], e.ids[p.j])
			}
		}
		pos := make([]int, e.n)
		for r, obj := range ev.Ranking {
			pos[obj] = r
		}
		checkSide := func(sample *big.Rat, side string) error {
			for i := 0; i < e.n; i++ {
				for j := i + 1; j < e.n; j++ {
					if e.identical[i][j] || ev.Pairs[pair{i, j}] {
						continue
					}
					s := e.diff[i][j].Eval(sample).Sign()
					if s == 0 {
						return fmt.Errorf("event %d: pair %s,%s tied at %s sample but not declared",
							w, e.ids[i], e.ids[j], side)
					}
					if (pos[i] < pos[j]) != (s < 0) {
						return fmt.Errorf("event %d: order of %s,%s disagrees with %s segment",
							w, e.ids[i], e.ids[j], side)
					}
				}
			}
			return nil
		}
		if w > 0 {
			if err := checkSide(tl.Segments[w-1].Sample, "left"); err != nil {
				return err
			}
		}
		if w < len(tl.Segments) {
			if err := checkSide(tl.Segments[w].Sample, "right"); err != nil {
				return err
			}
		}
	}
	return nil
}
