package motion

import (
	"math"
	"math/big"
	"math/rand"
	"testing"
)

// This file cross-checks Coverage against an independent reference that
// solves the per-pair quadratic inequality with exact rational arithmetic
// (math/big). Random inputs are drawn from a coarse dyadic grid so every
// float64 computation in both implementations is exact, which makes the
// comparison of open/closed classifications meaningful. Endpoint values of
// genuine quadratic roots are compared numerically against a 256-bit
// big.Float evaluation.

func rat(x float64) *big.Rat { return new(big.Rat).SetFloat64(x) }

func ratPos(x *big.Rat) bool { return x.Sign() > 0 }
func ratNeg(x *big.Rat) bool { return x.Sign() < 0 }

// refSpan mirrors Span but endpoints are symbolic: breakpoint endpoints
// carry their exact rational value, root endpoints refer to a root of the
// pair quadratic.
type refEnd struct {
	closed  bool
	isRoot  bool
	rootIdx int      // 1 = smaller root, 2 = larger root (quadratic), 0 = linear root
	val     *big.Rat // local time u when !isRoot (0 or dur)
}
type refSpan struct{ start, end refEnd }

// refSolve exactly solves au*u^2 + bu*u + c0 <= 0 on u in [0, dur), au >= 0.
func refSolve(au, bu, c0, dur *big.Rat) []refSpan {
	full := []refSpan{{
		start: refEnd{closed: true, val: rat(0)},
		end:   refEnd{closed: false, val: new(big.Rat).Set(dur)},
	}}
	zero := rat(0)

	if au.Sign() == 0 {
		// au == 0 implies bu == 0 (zero relative velocity).
		if c0.Sign() <= 0 {
			return full
		}
		return nil
	}

	// au > 0
	d := new(big.Rat).Sub(new(big.Rat).Mul(bu, bu),
		new(big.Rat).Mul(rat(4), new(big.Rat).Mul(au, c0)))
	switch d.Sign() {
	case -1:
		return nil
	case 0:
		u := new(big.Rat).Quo(new(big.Rat).Neg(bu), new(big.Rat).Mul(rat(2), au))
		if u.Cmp(zero) < 0 || u.Cmp(dur) >= 0 {
			return nil
		}
		return []refSpan{{
			start: refEnd{closed: true, isRoot: true, rootIdx: 1},
			end:   refEnd{closed: true, isRoot: true, rootIdx: 2},
		}}
	}

	// Two distinct roots. Exact sign facts for an upward parabola:
	//   u1 <= 0   <=>   c0 <= 0 or bu >= 0
	//   u2 >= dur <=>   f(dur) <= 0 or f'(dur) <= 0
	cDur := new(big.Rat).Add(new(big.Rat).Mul(au, new(big.Rat).Mul(dur, dur)),
		new(big.Rat).Add(new(big.Rat).Mul(bu, dur), c0))
	slopeDur := new(big.Rat).Add(new(big.Rat).Mul(rat(2), new(big.Rat).Mul(au, dur)), bu)

	// Empty cases: both roots < 0, or both roots >= dur.
	if ratPos(c0) && ratPos(bu) {
		return nil // u1 < u2 < 0
	}
	if cDur.Sign() >= 0 && slopeDur.Sign() <= 0 {
		return nil // dur <= u1
	}
	var sp refSpan
	if ratPos(c0) && ratNeg(bu) { // u1 > 0
		sp.start = refEnd{closed: true, isRoot: true, rootIdx: 1}
	} else {
		sp.start = refEnd{closed: true, val: rat(0)}
	}
	if ratPos(cDur) && ratPos(slopeDur) { // u2 < dur
		sp.end = refEnd{closed: true, isRoot: true, rootIdx: 2}
	} else {
		sp.end = refEnd{closed: false, val: new(big.Rat).Set(dur)}
	}
	return []refSpan{sp}
}

// rootFloat evaluates root idx of au*u^2+bu*u+c0 (or the linear root when
// au == 0) with 256-bit big.Float arithmetic and returns it as a float64.
func rootFloat(au, bu, c0 *big.Rat, idx int) float64 {
	const prec = 256
	bf := func(r *big.Rat) *big.Float { return new(big.Float).SetPrec(prec).SetRat(r) }
	a, b, c := bf(au), bf(bu), bf(c0)
	disc := new(big.Float).SetPrec(prec).Sub(
		new(big.Float).SetPrec(prec).Mul(b, b),
		new(big.Float).SetPrec(prec).Mul(big.NewFloat(4),
			new(big.Float).SetPrec(prec).Mul(a, c)))
	s := new(big.Float).SetPrec(prec).Sqrt(disc)
	den := new(big.Float).SetPrec(prec).Mul(big.NewFloat(2), a)
	nb := new(big.Float).SetPrec(prec).Neg(b)
	var u *big.Float
	if idx == 1 {
		u = new(big.Float).SetPrec(prec).Quo(new(big.Float).SetPrec(prec).Sub(nb, s), den)
	} else {
		u = new(big.Float).SetPrec(prec).Quo(new(big.Float).SetPrec(prec).Add(nb, s), den)
	}
	f, _ := u.Float64()
	return f
}

// refCoverage is the independent reference implementation of Coverage.
func refCoverage(a, b Trajectory, thr float64) []Span {
	var spans []Span
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		sa, sb := a[i], b[j]
		lo := math.Max(sa.T0, sb.T0)
		hi := math.Min(sa.T1, sb.T1)
		if lo < hi {
			au, bu, c0 := pairCoeffs(sa, sb, lo, thr)
			dur := hi - lo
			for _, rs := range refSolve(rat(au), rat(bu), rat(c0), rat(dur)) {
				spans = appendSpan(spans, materialize(rs, au, bu, c0, lo))
			}
		}
		switch {
		case sa.T1 < sb.T1:
			i++
		case sb.T1 < sa.T1:
			j++
		default:
			i++
			j++
		}
	}
	return spans
}

// materialize converts a symbolic reference span into a Span with float64
// endpoints, keeping root markers for comparison.
func materialize(rs refSpan, au, bu, c0, lo float64) Span {
	ra, rb, rc := rat(au), rat(bu), rat(c0)
	var s Span
	if rs.start.isRoot {
		s.Start = lo + rootFloat(ra, rb, rc, rs.start.rootIdx)
		s.StartRoot = &QuadRoot{} // presence marker only
	} else {
		f, _ := rs.start.val.Float64()
		s.Start = lo + f
	}
	if rs.end.isRoot {
		s.End = lo + rootFloat(ra, rb, rc, rs.end.rootIdx)
		s.EndRoot = &QuadRoot{}
	} else {
		f, _ := rs.end.val.Float64()
		s.End = lo + f
	}
	s.StartClosed = rs.start.closed
	s.EndClosed = rs.end.closed
	return s
}

func findSeg(tr Trajectory, t float64) (Segment, bool) {
	for _, s := range tr {
		if t >= s.T0 && t < s.T1 {
			return s, true
		}
	}
	return Segment{}, false
}

// directMember evaluates the definition directly at time t.
func directMember(a, b Trajectory, thr, t float64) bool {
	sa, oka := findSeg(a, t)
	sb, okb := findSeg(b, t)
	if !oka || !okb {
		return false
	}
	pa, pb := sa.At(t), sb.At(t)
	var d2 float64
	for k := range pa {
		d := pa[k] - pb[k]
		d2 += d * d
	}
	return d2 <= thr
}

func gridHalf(r *rand.Rand, n int) float64 { return float64(r.Intn(2*n+1)-n) / 2 }

func randTrajectory(r *rand.Rand, dim int) Trajectory {
	n := 1 + r.Intn(4)
	tr := make(Trajectory, 0, n)
	t := float64(r.Intn(2))
	for k := 0; k < n; k++ {
		dur := float64(1 + r.Intn(3))
		p := make(Vec, dim)
		w := make(Vec, dim)
		for d := 0; d < dim; d++ {
			p[d] = gridHalf(r, 4)
			w[d] = gridHalf(r, 2)
		}
		tr = append(tr, seg(t, t+dur, p, w))
		t += dur
		if r.Intn(4) == 0 {
			t += float64(1 + r.Intn(2)) // gap
		}
	}
	return tr
}

func compareWithReference(t *testing.T, a, b Trajectory, thr float64, got, want []Span) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("span count %d != reference %d\nthr=%v\na=%v\nb=%v\ngot=%v\nwant=%v",
			len(got), len(want), thr, a, b, got, want)
	}
	for k := range got {
		g, w := got[k], want[k]
		if g.StartClosed != w.StartClosed || g.EndClosed != w.EndClosed ||
			(g.StartRoot != nil) != (w.StartRoot != nil) ||
			(g.EndRoot != nil) != (w.EndRoot != nil) ||
			!almostEq(g.Start, w.Start) || !almostEq(g.End, w.End) {
			t.Fatalf("span %d mismatch\nthr=%v\na=%v\nb=%v\ngot =[%v,%v](%v,%v) roots(%v,%v)\nwant=[%v,%v](%v,%v) roots(%v,%v)",
				k, thr, a, b,
				g.Start, g.End, g.StartClosed, g.EndClosed, g.StartRoot != nil, g.EndRoot != nil,
				w.Start, w.End, w.StartClosed, w.EndClosed, w.StartRoot != nil, w.EndRoot != nil)
		}
	}
}

func checkInvariants(t *testing.T, spans []Span) {
	t.Helper()
	for k, s := range spans {
		if s.Start > s.End {
			t.Fatalf("span %d has Start > End: %v", k, s)
		}
		if s.Start == s.End && !(s.StartClosed && s.EndClosed) {
			t.Fatalf("span %d is an empty interval: %v", k, s)
		}
		if k > 0 {
			prev := spans[k-1]
			if prev.End > s.Start {
				t.Fatalf("spans %d and %d overlap", k-1, k)
			}
			if prev.End == s.Start && (prev.EndClosed || s.StartClosed) {
				t.Fatalf("spans %d and %d should have been merged", k-1, k)
			}
		}
	}
}

func TestRandomAgainstReference(t *testing.T) {
	r := rand.New(rand.NewSource(20261005))
	for iter := 0; iter < 3000; iter++ {
		dim := 1 + r.Intn(3)
		a := randTrajectory(r, dim)
		b := randTrajectory(r, dim)
		thr := float64(r.Intn(201)) / 4 // multiples of 0.25 in [0,50]

		got, err := Coverage(a, b, thr)
		if err != nil {
			t.Fatal(err)
		}
		want := refCoverage(a, b, thr)
		compareWithReference(t, a, b, thr, got, want)
		checkInvariants(t, got)

		// Independent point sampling: every breakpoint instant plus random
		// dyadic grid times, evaluated straight from the definition.
		var samples []float64
		maxT := 0.0
		for _, tr := range []Trajectory{a, b} {
			for _, s := range tr {
				samples = append(samples, s.T0, s.T1)
				if s.T1 > maxT {
					maxT = s.T1
				}
			}
		}
		for k := 0; k < 40; k++ {
			samples = append(samples, float64(r.Intn(int(maxT*4)+1))/4)
		}
		for _, ts := range samples {
			if Contains(got, ts) != directMember(a, b, thr, ts) {
				t.Fatalf("sample t=%v mismatch: spans say %v, direct says %v\nthr=%v\na=%v\nb=%v\nspans=%v",
					ts, Contains(got, ts), directMember(a, b, thr, ts), thr, a, b, got)
			}
		}
	}
}

// Randomly constructed tangencies: relative motion with a perpendicular
// offset whose squared length equals the threshold.
func TestRandomTangencies(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for iter := 0; iter < 500; iter++ {
		uv := float64(1 + r.Intn(4)) // tangency time
		off := gridHalf(r, 3)        // perpendicular offset
		thr := off * off
		dur := uv + float64(1+r.Intn(3))
		a := Trajectory{seg(0, dur, v(0, 0), v(0, 0))}
		b := Trajectory{seg(0, dur, v(uv, off), v(-1, 0))} // d^2 = (t-uv)^2 + off^2
		got, err := Coverage(a, b, thr)
		if err != nil {
			t.Fatal(err)
		}
		want := refCoverage(a, b, thr)
		compareWithReference(t, a, b, thr, got, want)
		if thr > 0 {
			if len(got) != 1 || got[0].Start != uv || got[0].End != uv ||
				!got[0].StartClosed || !got[0].EndClosed {
				t.Fatalf("expected grazing point at %v, got %v", uv, got)
			}
		} else {
			// zero threshold, trajectories coincide at t=uv
			if len(got) != 1 || got[0].Start != uv || got[0].End != uv {
				t.Fatalf("expected contact point at %v, got %v", uv, got)
			}
		}
	}
}

// Random stationary pairs exercise the au == 0, bu == 0 branch exactly.
func TestRandomStationary(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for iter := 0; iter < 500; iter++ {
		dim := 1 + r.Intn(3)
		a := randTrajectory(r, dim)
		b := randTrajectory(r, dim)
		for k := range a {
			a[k].V = make(Vec, dim)
		}
		for k := range b {
			b[k].V = make(Vec, dim)
		}
		thr := float64(r.Intn(65)) / 4
		got, err := Coverage(a, b, thr)
		if err != nil {
			t.Fatal(err)
		}
		compareWithReference(t, a, b, thr, got, refCoverage(a, b, thr))
	}
}
