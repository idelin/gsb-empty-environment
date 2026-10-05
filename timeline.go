package main

import (
	"math/big"
	"sort"
)

// Object is a point moving with constant velocity: pos(t) = (X,Y) + t*(Vx,Vy).
// All coordinates and velocities are rational.
type Object struct {
	ID     int64
	X, Y   *big.Rat
	Vx, Vy *big.Rat
}

// distPoly returns the squared distance to the static query point (qx,qy)
// as a quadratic polynomial in t with rational coefficients.
func (o Object) distPoly(qx, qy *big.Rat) Poly {
	dx := new(big.Rat).Sub(o.X, qx)
	dy := new(big.Rat).Sub(o.Y, qy)
	a := new(big.Rat).Add(new(big.Rat).Mul(o.Vx, o.Vx), new(big.Rat).Mul(o.Vy, o.Vy))
	b := new(big.Rat).Add(new(big.Rat).Mul(dx, o.Vx), new(big.Rat).Mul(dy, o.Vy))
	b.Mul(b, big.NewRat(2, 1))
	c := new(big.Rat).Add(new(big.Rat).Mul(dx, dx), new(big.Rat).Mul(dy, dy))
	return mkPoly(a, b, c)
}

// Event is a single instant in [0,T]: either a set of pair roots clustered
// at the same algebraic time, or a boundary point (0 or T).
type Event struct {
	T        *AlgNum
	Pairs    [][2]int // object indices whose distance difference vanishes at T
	Boundary bool
	Lo, Hi   *big.Rat // rational isolating interval: Lo <= T <= Hi
	Ranking  []int64  // exact ranking (object IDs, closest first) at T
}

// Segment is an open interval (A,B) between consecutive events on which the
// full ranking is constant, certified by a rational Sample.
type Segment struct {
	A, B    *AlgNum
	Sample  *big.Rat
	Ranking []int64
}

// Entry is one timeline item: a point event or an open segment.
type Entry struct {
	Event   *Event
	Segment *Segment
}

// Computation holds the full result plus everything Verify needs.
type Computation struct {
	Objects  []Object
	QX, QY   *big.Rat
	T        *big.Rat
	K        int
	Dist     []Poly // squared-distance polynomials, one per object
	Events   []*Event
	Segments []*Segment
	Timeline []Entry
}

func rankIDs(objs []Object, vals []*AlgNum) []int64 {
	idx := make([]int, len(vals))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		i, j := idx[a], idx[b]
		if c := vals[i].Cmp(vals[j]); c != 0 {
			return c < 0
		}
		return objs[i].ID < objs[j].ID // distance ties broken by ID
	})
	ids := make([]int64, len(idx))
	for k, i := range idx {
		ids[k] = objs[i].ID
	}
	return ids
}

// topK returns the first min(K, len(ranking)) IDs; K <= 0 yields an empty list.
func topK(ranking []int64, K int) []int64 {
	k := K
	if k > len(ranking) {
		k = len(ranking)
	}
	if k < 0 {
		k = 0
	}
	out := make([]int64, k)
	copy(out, ranking[:k])
	return out
}

// rationalBetween returns a rational strictly between distinct a < b,
// refining rational bounds until a gap appears.
func rationalBetween(a, b *AlgNum) *big.Rat {
	for prec := uint(0); ; prec++ {
		_, ahi := a.interval(prec)
		blo, _ := b.interval(prec)
		if ahi.Cmp(blo) < 0 {
			s := new(big.Rat).Sub(blo, ahi)
			s.Mul(s, big.NewRat(1, 3))
			s.Add(s, ahi) // ahi + (blo-ahi)/3, strictly inside (ahi, blo)
			if a.Cmp(ratAlg(s)) < 0 && ratAlg(s).Cmp(b) < 0 {
				return s
			}
		}
		if prec > 1<<22 {
			panic("rationalBetween: no progress")
		}
	}
}

// Compute builds the complete, exact top-K ranking timeline over [0,T].
func Compute(objs []Object, qx, qy, T *big.Rat, K int) *Computation {
	c := &Computation{Objects: objs, QX: qx, QY: qy, T: T, K: K}
	n := len(objs)
	c.Dist = make([]Poly, n)
	for i, o := range objs {
		c.Dist[i] = o.distPoly(qx, qy)
	}
	zero := ratAlg(new(big.Rat))
	tEnd := ratAlg(T)

	// Enumerate all pairs; collect every root of every difference polynomial
	// that lies in [0,T]. Roots of distinct pairs may coincide (handled by
	// exact equality when clustering below).
	type pairRoot struct {
		t    *AlgNum
		i, j int
	}
	var roots []pairRoot
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			d := c.Dist[i].Sub(c.Dist[j])
			for _, r := range d.Roots() {
				if r.Cmp(zero) >= 0 && r.Cmp(tEnd) <= 0 {
					roots = append(roots, pairRoot{r, i, j})
				}
			}
		}
	}
	sort.Slice(roots, func(a, b int) bool {
		if cm := roots[a].t.Cmp(roots[b].t); cm != 0 {
			return cm < 0
		}
		if roots[a].i != roots[b].i {
			return roots[a].i < roots[b].i
		}
		return roots[a].j < roots[b].j
	})

	// Cluster simultaneous roots into single events (exact algebraic equality).
	for _, r := range roots {
		if len(c.Events) > 0 && c.Events[len(c.Events)-1].T.Equal(r.t) {
			e := c.Events[len(c.Events)-1]
			e.Pairs = append(e.Pairs, [2]int{r.i, r.j})
		} else {
			c.Events = append(c.Events, &Event{T: r.t, Pairs: [][2]int{{r.i, r.j}}})
		}
	}
	// Always expose the boundary instants 0 and T.
	if len(c.Events) == 0 || c.Events[0].T.Cmp(zero) != 0 {
		c.Events = append([]*Event{{T: zero, Boundary: true}}, c.Events...)
	} else {
		c.Events[0].Boundary = true
	}
	if last := c.Events[len(c.Events)-1]; last.T.Cmp(tEnd) != 0 {
		c.Events = append(c.Events, &Event{T: tEnd, Boundary: true})
	} else {
		last.Boundary = true
	}

	// Exact ranking at each event instant (arithmetic in Q(sqrt(m))).
	for _, e := range c.Events {
		vals := make([]*AlgNum, n)
		for i := range objs {
			vals[i] = c.Dist[i].EvalAlg(e.T)
		}
		e.Ranking = rankIDs(objs, vals)
	}

	// Open segments between consecutive distinct event times, each certified
	// by a rational sample point.
	for k := 0; k+1 < len(c.Events); k++ {
		a, b := c.Events[k].T, c.Events[k+1].T
		if a.Cmp(b) == 0 {
			continue
		}
		s := rationalBetween(a, b)
		vals := make([]*AlgNum, n)
		for i := range objs {
			vals[i] = ratAlg(c.Dist[i].EvalRat(s))
		}
		c.Segments = append(c.Segments, &Segment{A: a, B: b, Sample: s, Ranking: rankIDs(objs, vals)})
	}

	isolateEvents(c.Events)

	// Interleave events and segments into the final timeline.
	for k, e := range c.Events {
		c.Timeline = append(c.Timeline, Entry{Event: e})
		if k < len(c.Segments) {
			c.Timeline = append(c.Timeline, Entry{Segment: c.Segments[k]})
		}
	}
	return c
}

// isolateEvents assigns each event a rational isolating interval, pairwise
// disjoint and consistent with the event order.
func isolateEvents(events []*Event) {
	for prec := uint(2); ; prec++ {
		for _, e := range events {
			e.Lo, e.Hi = e.T.interval(prec)
		}
		ok := true
		for k := 0; k+1 < len(events); k++ {
			if events[k].Hi.Cmp(events[k+1].Lo) >= 0 {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		if prec > 1<<22 {
			panic("isolateEvents: no progress")
		}
	}
}
