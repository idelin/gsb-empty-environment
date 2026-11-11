package netsec

import (
	"math/rand"
	"sort"
	"testing"
)

func TestEndpointOpenness(t *testing.T) {
	v := MustParse("1.2.3")
	below := MustParse("1.2.3-z.9") // greatest tested version below 1.2.3
	above := MustParse("1.2.4-0")   // smallest version above 1.2.3

	exact := Exact(v)
	if !exact.Contains(v) || exact.Contains(below) || exact.Contains(above) {
		t.Errorf("Exact(%s) membership wrong", v)
	}

	open := Open(MustParse("1.0.0"), MustParse("1.0.2"))
	for _, s := range []string{"1.0.0", "1.0.2"} {
		if open.Contains(MustParse(s)) {
			t.Errorf("open interval (1.0.0, 1.0.2) contains endpoint %s", s)
		}
	}
	for _, s := range []string{"1.0.1", "1.0.2-0", "1.0.1-5"} {
		if !open.Contains(MustParse(s)) {
			t.Errorf("open interval (1.0.0, 1.0.2) misses %s", s)
		}
	}

	co := ClosedOpen(MustParse("1.0.0"), MustParse("1.0.2"))
	if !co.Contains(MustParse("1.0.0")) || co.Contains(MustParse("1.0.2")) {
		t.Errorf("[1.0.0, 1.0.2) endpoint openness wrong")
	}
	oc := OpenClosed(MustParse("1.0.0"), MustParse("1.0.2"))
	if oc.Contains(MustParse("1.0.0")) || !oc.Contains(MustParse("1.0.2")) {
		t.Errorf("(1.0.0, 1.0.2] endpoint openness wrong")
	}
}

func TestEmptyIntervals(t *testing.T) {
	cases := []struct {
		name string
		set  Set
		want bool
	}{
		{"adjacent across patch", Open(MustParse("1.0.0"), MustParse("1.0.1-0")), true},
		{"adjacent pre-release", Open(MustParse("1.0.0-a"), MustParse("1.0.0-a.0")), true},
		{"equal exclusive", OpenClosed(MustParse("1.0.0"), MustParse("1.0.0")), true},
		{"inverted", Closed(MustParse("1.0.1"), MustParse("1.0.0")), true},
		{"point", Exact(MustParse("1.0.0")), false},
		{"half-open adjacent", ClosedOpen(MustParse("1.0.0"), MustParse("1.0.1-0")), false},
		{"open with gap", Open(MustParse("1.0.0"), MustParse("1.0.1")), false},
	}
	for _, c := range cases {
		if got := c.set.IsEmpty(); got != c.want {
			t.Errorf("%s: IsEmpty() = %v, want %v (set %s)", c.name, got, c.want, c.set)
		}
	}
}

func TestNormalization(t *testing.T) {
	// Overlapping intervals merge.
	s := Closed(MustParse("1.0.0"), MustParse("1.5.0")).
		Union(Closed(MustParse("1.2.0"), MustParse("2.0.0")))
	if got, want := s.String(), "[1.0.0, 2.0.0]"; got != want {
		t.Errorf("overlap merge = %s, want %s", got, want)
	}

	// Shared endpoint with at least one inclusive side merges.
	s = ClosedOpen(MustParse("1.0.0"), MustParse("1.5.0")).
		Union(Closed(MustParse("1.5.0"), MustParse("2.0.0")))
	if got, want := s.String(), "[1.0.0, 2.0.0]"; got != want {
		t.Errorf("touching merge = %s, want %s", got, want)
	}

	// Adjacent versions with both endpoints inclusive merge: no version
	// exists between 1.0.5 and 1.0.6-0.
	s = Closed(MustParse("1.0.0"), MustParse("1.0.5")).
		Union(Closed(MustParse("1.0.6-0"), MustParse("2.0.0")))
	if got, want := s.String(), "[1.0.0, 2.0.0]"; got != want {
		t.Errorf("adjacent merge = %s, want %s", got, want)
	}

	// Same for adjacent pre-release identifiers.
	s = Exact(MustParse("1.0.0-a")).Union(Closed(MustParse("1.0.0-a.0"), MustParse("1.0.0-b")))
	if got, want := s.String(), "[1.0.0-a, 1.0.0-b]"; got != want {
		t.Errorf("adjacent pre-release merge = %s, want %s", got, want)
	}

	// A gap (1.0.6-0 is excluded) keeps intervals disjoint.
	s = Closed(MustParse("1.0.0"), MustParse("1.0.5")).
		Union(Closed(MustParse("1.0.6"), MustParse("2.0.0")))
	if got, want := len(s.Intervals()), 2; got != want {
		t.Errorf("gapped union has %d intervals, want %d (%s)", got, want, s)
	}

	// Both sides exclusive at a shared endpoint keeps the point out.
	s = ClosedOpen(MustParse("1.0.0"), MustParse("1.5.0")).
		Union(OpenClosed(MustParse("1.5.0"), MustParse("2.0.0")))
	if s.Contains(MustParse("1.5.0")) {
		t.Errorf("point 1.5.0 excluded on both sides must stay excluded in %s", s)
	}
}

func TestSetOps(t *testing.T) {
	a := Closed(MustParse("1.0.0"), MustParse("2.0.0"))
	b := Closed(MustParse("1.5.0"), MustParse("3.0.0"))

	if got, want := a.Union(b).String(), "[1.0.0, 3.0.0]"; got != want {
		t.Errorf("union = %s, want %s", got, want)
	}
	if got, want := a.Intersect(b).String(), "[1.5.0, 2.0.0]"; got != want {
		t.Errorf("intersect = %s, want %s", got, want)
	}
	if got, want := a.Difference(b).String(), "[1.0.0, 1.5.0)"; got != want {
		t.Errorf("difference = %s, want %s", got, want)
	}
	if got, want := b.Difference(a).String(), "(2.0.0, 3.0.0]"; got != want {
		t.Errorf("difference = %s, want %s", got, want)
	}

	// Difference with an interior point splits into two intervals.
	holed := a.Difference(Exact(MustParse("1.5.0")))
	if got, want := holed.String(), "[1.0.0, 1.5.0) ∪ (1.5.0, 2.0.0]"; got != want {
		t.Errorf("holed difference = %s, want %s", got, want)
	}
	if holed.Contains(MustParse("1.5.0")) {
		t.Errorf("exempted point 1.5.0 still in %s", holed)
	}

	// Exempt removes exact backport versions only.
	s := ClosedOpen(MustParse("1.0.0"), MustParse("1.4.0")).Exempt(MustParse("1.2.3"))
	if s.Contains(MustParse("1.2.3")) || !s.Contains(MustParse("1.2.4")) {
		t.Errorf("exemption wrong in %s", s)
	}
}

func TestMatchEvidence(t *testing.T) {
	s := ClosedOpen(MustParse("1.0.0"), MustParse("1.4.0")).
		Union(Closed(MustParse("2.0.0-a"), MustParse("2.0.0")))

	m := s.Match(MustParse("1.2.3"))
	if !m.Hit || m.Interval.String() != "[1.0.0, 1.4.0)" {
		t.Errorf("Match(1.2.3) = %+v, want hit on [1.0.0, 1.4.0)", m)
	}
	m = s.Match(MustParse("1.4.0"))
	if m.Hit {
		t.Errorf("Match(1.4.0) = %+v, want miss", m)
	}
	m = s.Match(MustParse("2.0.0-b"))
	if !m.Hit || m.Interval.String() != "[2.0.0-a, 2.0.0]" {
		t.Errorf("Match(2.0.0-b) = %+v, want hit on [2.0.0-a, 2.0.0]", m)
	}
}

// --- Randomized cross-check against an explicit small version universe ---

func buildUniverse() []Version {
	pres := [][]string{
		nil,
		{"0"}, {"1"}, {"2"}, {"9"}, {"10"},
		{"a"}, {"b"}, {"alpha"}, {"z"},
		{"0", "0"}, {"0", "1"}, {"a", "0"}, {"a", "1"}, {"1", "a"},
	}
	var vs []Version
	for major := uint64(0); major <= 2; major++ {
		for minor := uint64(0); minor <= 2; minor++ {
			for patch := uint64(0); patch <= 2; patch++ {
				for _, pre := range pres {
					vs = append(vs, Version{Major: major, Minor: minor, Patch: patch, Pre: pre})
				}
			}
		}
	}
	sort.Slice(vs, func(i, j int) bool { return Compare(vs[i], vs[j]) < 0 })
	for i := 1; i < len(vs); i++ {
		if Compare(vs[i-1], vs[i]) >= 0 {
			panic("universe not strictly sorted")
		}
	}
	return vs
}

// ivDef is an independently written naive interval description used as the
// reference model for the randomized cross-check.
type ivDef struct {
	lo, hi       Version
	loInc, hiInc bool
	loInf, hiInf bool
}

func (d ivDef) contains(v Version) bool {
	if !d.loInf {
		c := Compare(v, d.lo)
		if c < 0 || (c == 0 && !d.loInc) {
			return false
		}
	}
	if !d.hiInf {
		c := Compare(v, d.hi)
		if c > 0 || (c == 0 && !d.hiInc) {
			return false
		}
	}
	return true
}

func (d ivDef) interval() Interval {
	lo := Bound{V: d.lo, Inclusive: d.loInc, Infinite: d.loInf}
	hi := Bound{V: d.hi, Inclusive: d.hiInc, Infinite: d.hiInf}
	return Interval{Lo: lo, Hi: hi}
}

func randDefs(r *rand.Rand, uni []Version) []ivDef {
	n := 1 + r.Intn(4)
	defs := make([]ivDef, 0, n)
	for k := 0; k < n; k++ {
		d := ivDef{loInc: r.Intn(2) == 0, hiInc: r.Intn(2) == 0}
		if r.Intn(8) == 0 {
			d.loInf = true
		}
		if r.Intn(8) == 0 {
			d.hiInf = true
		}
		i, j := r.Intn(len(uni)), r.Intn(len(uni))
		if i > j {
			i, j = j, i
		}
		d.lo, d.hi = uni[i], uni[j]
		defs = append(defs, d)
	}
	return defs
}

func defsContain(defs []ivDef, v Version) bool {
	for _, d := range defs {
		if d.contains(v) {
			return true
		}
	}
	return false
}

func defsSet(defs []ivDef) Set {
	ivs := make([]Interval, 0, len(defs))
	for _, d := range defs {
		ivs = append(ivs, d.interval())
	}
	return NewSet(ivs...)
}

func checkNormalized(t *testing.T, name string, s Set) {
	t.Helper()
	ivs := s.Intervals()
	for i, iv := range ivs {
		if iv.isEmpty() {
			t.Fatalf("%s: normalized set %s contains empty interval %s", name, s, iv)
		}
		if i > 0 {
			prev := ivs[i-1]
			if !loLess(prev.Lo, iv.Lo) {
				t.Fatalf("%s: normalized set %s not sorted", name, s)
			}
			if touches(prev.Hi, iv.Lo) {
				t.Fatalf("%s: normalized set %s has mergeable neighbours %s and %s", name, s, prev, iv)
			}
		}
	}
}

func TestRandomizedSetOps(t *testing.T) {
	uni := buildUniverse()
	r := rand.New(rand.NewSource(1285))
	for trial := 0; trial < 3000; trial++ {
		defsA := randDefs(r, uni)
		defsB := randDefs(r, uni)
		A, B := defsSet(defsA), defsSet(defsB)
		checkNormalized(t, "A", A)
		checkNormalized(t, "B", B)

		var exV []Version
		for k := 0; k < r.Intn(4); k++ {
			exV = append(exV, uni[r.Intn(len(uni))])
		}

		union := A.Union(B)
		inter := A.Intersect(B)
		diff := A.Difference(B)
		exempt := A.Exempt(exV...)
		checkNormalized(t, "union", union)
		checkNormalized(t, "intersect", inter)
		checkNormalized(t, "difference", diff)
		checkNormalized(t, "exempt", exempt)

		for _, v := range uni {
			inA := defsContain(defsA, v)
			inB := defsContain(defsB, v)
			inEx := false
			for _, e := range exV {
				if e.Equal(v) {
					inEx = true
					break
				}
			}
			checks := []struct {
				name string
				set  Set
				want bool
			}{
				{"A", A, inA},
				{"B", B, inB},
				{"union", union, inA || inB},
				{"intersect", inter, inA && inB},
				{"difference", diff, inA && !inB},
				{"exempt", exempt, inA && !inEx},
			}
			for _, c := range checks {
				if got := c.set.Contains(v); got != c.want {
					t.Fatalf("trial %d: %s.Contains(%s) = %v, want %v (set %s)",
						trial, c.name, v, got, c.want, c.set)
				}
				if m := c.set.Match(v); m.Hit != c.want {
					t.Fatalf("trial %d: %s.Match(%s).Hit = %v, want %v",
						trial, c.name, v, m.Hit, c.want)
				} else if m.Hit && !m.Interval.Contains(v) {
					t.Fatalf("trial %d: %s.Match(%s) interval %s does not contain the version",
						trial, c.name, v, m.Interval)
				}
			}
		}
	}
}
