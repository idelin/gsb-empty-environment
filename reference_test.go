package motion

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

// referenceIntersect is an independent, deliberately unoptimized reference:
// it iterates over ALL segment pairs, solves each overlap's quadratic
// inequality by sign-sampling between critical points (a different method
// from the library's direct root classification), then sorts and merges.
func referenceIntersect(a, b Trajectory, thr float64) []Interval {
	var ivs []Interval
	for _, sa := range a.Segments {
		for _, sb := range b.Segments {
			lo := math.Max(sa.Start, sb.Start)
			hi := math.Min(sa.End, sb.End)
			if lo >= hi {
				continue
			}
			ivs = append(ivs, refPiece(sa, sb, lo, hi, thr)...)
		}
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].Start < ivs[j].Start })
	var out []Interval
	for _, iv := range ivs {
		if n := len(out); n > 0 && iv.Start <= out[n-1].End {
			last := &out[n-1]
			if iv.End > last.End || (iv.End == last.End && iv.EndClosed) {
				last.End, last.EndClosed = iv.End, iv.EndClosed
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// refPiece solves the quadratic inequality on one overlap by evaluating the
// sign of f on every cell (critical points and the open intervals between
// them) and merging adjacent covered cells.
func refPiece(sa, sb Segment, lo, hi, thr float64) []Interval {
	dim := len(sa.Pos)
	var a2, b1, c0 float64
	for d := 0; d < dim; d++ {
		r0 := sa.Pos[d] + sa.Vel[d]*(lo-sa.Start) - (sb.Pos[d] + sb.Vel[d]*(lo-sb.Start))
		rv := sa.Vel[d] - sb.Vel[d]
		a2 += rv * rv
		b1 += 2 * r0 * rv
		c0 += r0 * r0
	}
	c0 -= thr
	L := hi - lo
	f := func(s float64) float64 { return (a2*s+b1)*s + c0 }
	tol := 1e-9 * (1 + math.Abs(c0) + math.Abs(b1)*L + a2*L*L)

	crit := []float64{0, L}
	if a2 != 0 {
		if disc := b1*b1 - 4*a2*c0; disc >= 0 {
			sq := math.Sqrt(disc)
			for _, r := range [2]float64{(-b1 - sq) / (2 * a2), (-b1 + sq) / (2 * a2)} {
				if r > 0 && r < L {
					crit = append(crit, r)
				}
			}
		}
	}
	sort.Float64s(crit)
	uniq := crit[:1]
	for _, c := range crit[1:] {
		if c != uniq[len(uniq)-1] {
			uniq = append(uniq, c)
		}
	}
	crit = uniq

	// cells alternate: point c0, gap (c0,c1), point c1, ..., gap (c_{n-1}, L).
	// Point cells exist only for c < L (the domain is half-open).
	type run struct {
		start, end float64
		endClosed  bool
	}
	var runs []run
	open := false
	var cur run
	startRun := func(s float64) { cur = run{start: s}; open = true }
	endRun := func(end float64, closed bool) {
		if open {
			cur.end, cur.endClosed = end, closed
			runs = append(runs, cur)
			open = false
		}
	}
	for k, c := range crit {
		pointIn := c < L && f(c) <= tol
		if pointIn {
			if !open {
				startRun(c)
			}
			cur.end, cur.endClosed = c, true
		} else {
			endRun(c, false)
		}
		if k+1 < len(crit) {
			mid := c/2 + crit[k+1]/2
			if f(mid) <= tol {
				if !open {
					startRun(c)
				}
				cur.end, cur.endClosed = crit[k+1], false
			} else {
				// The run, if any, ends at c; it is closed there exactly
				// when the point cell at c itself is covered.
				endRun(c, pointIn)
			}
		}
	}
	endRun(L, false)

	var out []Interval
	for _, r := range runs {
		out = append(out, Interval{Start: lo + r.start, End: lo + r.end, EndClosed: r.endClosed})
	}
	return out
}

// checkSemantics verifies intervals against direct trajectory evaluation:
// membership at critical instants and midpoints must match the squared
// distance comparison, and endpoint openness must be consistent with the
// actual distance at the endpoint (this is what pins down breakpoint
// open/closed behavior).
func checkSemantics(t *testing.T, name string, a, b Trajectory, thr float64, ivs []Interval) {
	t.Helper()
	for i, iv := range ivs {
		if iv.Start > iv.End {
			t.Errorf("%s: interval %d has Start > End: %+v", name, i, iv)
		}
		if iv.Start == iv.End && !iv.EndClosed {
			t.Errorf("%s: interval %d is an empty half-open point: %+v", name, i, iv)
		}
		if i > 0 && iv.Start <= ivs[i-1].End {
			t.Errorf("%s: intervals %d and %d not disjoint/merged: %+v %+v", name, i-1, i, ivs[i-1], iv)
		}
	}

	var crit []float64
	for _, tr := range []Trajectory{a, b} {
		for _, s := range tr.Segments {
			crit = append(crit, s.Start, s.End)
		}
	}
	for _, iv := range ivs {
		crit = append(crit, iv.Start, iv.End)
	}
	sort.Float64s(crit)
	uniq := crit[:0]
	for _, c := range crit {
		if len(uniq) == 0 || c != uniq[len(uniq)-1] {
			uniq = append(uniq, c)
		}
	}
	crit = uniq

	var samples []float64
	samples = append(samples, crit...)
	for i := 0; i+1 < len(crit); i++ {
		if m := crit[i]/2 + crit[i+1]/2; m > crit[i] && m < crit[i+1] {
			samples = append(samples, m)
		}
	}

	distSqAt := func(x float64) (float64, bool) {
		pa, oka := a.At(x)
		pb, okb := b.At(x)
		if !oka || !okb {
			return 0, false
		}
		var d2 float64
		for d := range pa {
			diff := pa[d] - pb[d]
			d2 += diff * diff
		}
		return d2, true
	}
	member := func(x float64) bool {
		for _, iv := range ivs {
			if iv.Contains(x) {
				return true
			}
		}
		return false
	}

	for _, x := range samples {
		in := member(x)
		d2, ok := distSqAt(x)
		if !ok {
			if in {
				t.Errorf("%s: t=%v reported inside but trajectory undefined there", name, x)
			}
			continue
		}
		scale := math.Max(1, math.Max(d2, thr))
		if math.Abs(d2-thr) <= 1e-7*scale {
			continue // too close to the boundary to compare floats
		}
		if want := d2 <= thr; want != in {
			t.Errorf("%s: t=%v distSq=%v thr=%v: membership=%v, want %v", name, x, d2, thr, in, want)
		}
	}

	for _, iv := range ivs {
		if d2, ok := distSqAt(iv.Start); !ok {
			t.Errorf("%s: interval start %v lies outside trajectory coverage", name, iv.Start)
		} else if d2 > thr+1e-7*math.Max(1, thr) {
			t.Errorf("%s: closed start %v has distSq=%v > thr=%v", name, iv.Start, d2, thr)
		}
		if d2, ok := distSqAt(iv.End); ok {
			scale := math.Max(1, math.Max(d2, thr))
			if math.Abs(d2-thr) > 1e-7*scale {
				if iv.EndClosed && d2 > thr {
					t.Errorf("%s: closed end %v has distSq=%v > thr=%v", name, iv.End, d2, thr)
				}
				if !iv.EndClosed && d2 <= thr {
					t.Errorf("%s: open end %v has distSq=%v <= thr=%v (should have been merged or closed)", name, iv.End, d2, thr)
				}
			}
		}
	}
}

func compareIntervals(tol float64, got, want []Interval) (string, bool) {
	if len(got) != len(want) {
		return "interval count differs", false
	}
	close := func(x, y float64) bool {
		return math.Abs(x-y) <= tol*(1+math.Max(math.Abs(x), math.Abs(y)))
	}
	for i := range want {
		if !close(got[i].Start, want[i].Start) || !close(got[i].End, want[i].End) ||
			got[i].EndClosed != want[i].EndClosed {
			return "interval mismatch", false
		}
	}
	return "", true
}

func randTraj(rng *rand.Rand, dim int, pool []float64) Trajectory {
	var segs []Segment
	i := 0
	for i < len(pool)-1 {
		if rng.Intn(4) == 0 { // coverage gap
			i++
			continue
		}
		span := 1 + rng.Intn(2)
		j := i + span
		if j > len(pool)-1 {
			j = len(pool) - 1
		}
		pos := make(Vec, dim)
		vel := make(Vec, dim)
		zeroVel := rng.Intn(3) == 0
		for d := 0; d < dim; d++ {
			pos[d] = float64(rng.Intn(13) - 6)
			if !zeroVel {
				vel[d] = float64(rng.Intn(9) - 4)
			}
		}
		segs = append(segs, Segment{Start: pool[i], End: pool[j], Pos: pos, Vel: vel})
		i = j
	}
	return Trajectory{Segments: segs}
}

func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	thresholds := []float64{0, 0.5, 1, 2, 4, 9, 16, 25, 36, 50}
	for iter := 0; iter < 5000; iter++ {
		dim := 1 + rng.Intn(3)
		poolSet := map[float64]bool{0: true}
		for k := 0; k < 6; k++ {
			poolSet[float64(1+rng.Intn(12))] = true
		}
		var pool []float64
		for v := range poolSet {
			pool = append(pool, v)
		}
		sort.Float64s(pool)

		a := randTraj(rng, dim, pool)
		b := randTraj(rng, dim, pool)
		thr := thresholds[rng.Intn(len(thresholds))]

		got, err := Intersect(a, b, thr)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		want := referenceIntersect(a, b, thr)
		if msg, ok := compareIntervals(1e-9, got, want); !ok {
			t.Fatalf("iter %d thr=%v: %s\ngot  %v\nwant %v\na=%+v\nb=%+v", iter, thr, msg, got, want, a, b)
		}
		checkSemantics(t, "random", a, b, thr, got)
		if t.Failed() {
			t.Fatalf("iter %d thr=%v a=%+v b=%+v got=%v", iter, thr, a, b, got)
		}
	}
}
