package spectest

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	. "prescreen.local/ps-c7499fbc67"
)

func TestTimeSpanNormalizesAndMergesUnion(t *testing.T) {
	s, err := NewTimeSpan(
		Interval{Start: tt(0), End: tt(2)},
		Interval{Start: tt(1), End: tt(3)},
		Interval{Start: tt(3), End: tt(4)}, // adjacent to previous
		Interval{Start: tt(7), End: tt(8)},
		Interval{Start: tt(8), End: tt(9)}, // adjacent
	)
	if err != nil {
		t.Fatal(err)
	}
	want := MustNewTimeSpan(
		Interval{Start: tt(0), End: tt(4)},
		Interval{Start: tt(7), End: tt(9)},
	)
	if !s.Equal(want) {
		t.Fatalf("normalized span = %s, want %s", s, want)
	}
}

func TestTimeSpanIntersectionOfUnions(t *testing.T) {
	a := MustNewTimeSpan(
		Interval{Start: tt(0), End: tt(2)},
		Interval{Start: tt(4), End: tt(8)},
	)
	b := MustNewTimeSpan(
		Interval{Start: tt(1), End: tt(5)},
		Interval{Start: tt(7), End: tt(9)},
	)
	c := MustNewTimeSpan(
		Interval{Start: tt(1), End: tt(2)},
		Interval{Start: tt(7), End: tt(8)},
	)
	got := IntersectAll(a, b, c)
	want := MustNewTimeSpan(
		Interval{Start: tt(1), End: tt(2)},
		Interval{Start: tt(7), End: tt(8)},
	)
	if !got.Equal(want) {
		t.Fatalf("intersection = %s, want %s", got, want)
	}
}

func TestPairwiseIntersectingButNoCommonInstant(t *testing.T) {
	x := Interval{Start: tt(0), End: tt(1)}
	y := Interval{Start: tt(2), End: tt(3)}
	z := Interval{Start: tt(4), End: tt(5)}
	a := MustNewTimeSpan(x, y)
	b := MustNewTimeSpan(y, z)
	c := MustNewTimeSpan(z, x)

	if a.Intersect(b).Empty() || b.Intersect(c).Empty() || c.Intersect(a).Empty() {
		t.Fatalf("expected all pairs to intersect")
	}
	all := IntersectAll(a, b, c)
	if !all.Empty() {
		t.Fatalf("global intersection = %s, want empty", all)
	}
}

// TestTimeSpanUnionExhaustive checks the interval implementation against a
// pointwise oracle over every subset of unit intervals on a small grid.
func TestTimeSpanUnionExhaustive(t *testing.T) {
	const n = 6
	spans := make([]TimeSpan, 0, 1<<n)
	sets := make([][]Interval, 0, 1<<n)
	for mask := 0; mask < 1<<n; mask++ {
		var parts []Interval
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				parts = append(parts, Interval{Start: tt(i), End: tt(i + 1)})
			}
		}
		s, err := NewTimeSpan(parts...)
		if err != nil {
			t.Fatalf("mask %d: %v", mask, err)
		}
		spans = append(spans, s)
		sets = append(sets, parts)
	}
	contains := func(parts []Interval, t int) bool {
		for _, iv := range parts {
			if iv.Contains(tt(t)) {
				return true
			}
		}
		return false
	}
	for i := range spans {
		for j := range spans {
			u := spans[i].Union(spans[j])
			x := spans[i].Intersect(spans[j])
			for k := 0; k < n; k++ {
				wantU := contains(sets[i], k) || contains(sets[j], k)
				wantX := contains(sets[i], k) && contains(sets[j], k)
				if u.Contains(tt(k)) != wantU {
					t.Fatalf("union mismatch i=%d j=%d k=%d: %s", i, j, k, u)
				}
				if x.Contains(tt(k)) != wantX {
					t.Fatalf("intersection mismatch i=%d j=%d k=%d: %s", i, j, k, x)
				}
			}
		}
	}
}

func TestTimeSpanRandomizedAgainstPointOracle(t *testing.T) {
	r := rand.New(rand.NewSource(20261109))
	const n = 12
	for trial := 0; trial < 500; trial++ {
		randomSpan := func() (TimeSpan, [n]bool) {
			var parts []Interval
			var points [n]bool
			for i := 0; i < n; i++ {
				if r.Intn(3) == 0 {
					continue
				}
				j := i + 1 + r.Intn(3)
				if j > n {
					j = n
				}
				parts = append(parts, Interval{Start: tt(i), End: tt(j)})
				for k := i; k < j; k++ {
					points[k] = true
				}
			}
			s, err := NewTimeSpan(parts...)
			if err != nil {
				t.Fatal(err)
			}
			return s, points
		}
		a, pa := randomSpan()
		b, pb := randomSpan()
		for k := 0; k < n; k++ {
			if got, want := a.Intersect(b).Contains(tt(k)), pa[k] && pb[k]; got != want {
				t.Fatalf("trial %d: intersection point %d = %v, want %v", trial, k, got, want)
			}
			if got, want := a.Union(b).Contains(tt(k)), pa[k] || pb[k]; got != want {
				t.Fatalf("trial %d: union point %d = %v, want %v", trial, k, got, want)
			}
		}
	}
}

func TestClampSupportsOpenEndpoints(t *testing.T) {
	s := MustNewTimeSpan(
		Interval{Start: tt(0), End: tt(2)},
		Interval{Start: tt(4), End: tt(9)},
	)
	if got := s.Clamp(tt(1), time.Time{}); !got.Equal(MustNewTimeSpan(Interval{Start: tt(1), End: tt(2)}, Interval{Start: tt(4), End: tt(9)})) {
		t.Fatalf("clamp from = %s", got)
	}
	if got := s.Clamp(time.Time{}, tt(5)); !got.Equal(MustNewTimeSpan(Interval{Start: tt(0), End: tt(2)}, Interval{Start: tt(4), End: tt(5)})) {
		t.Fatalf("clamp until = %s", got)
	}
}

func TestCommonInstantDeterministic(t *testing.T) {
	a := MustNewTimeSpan(Interval{Start: tt(0), End: tt(5)})
	b := MustNewTimeSpan(Interval{Start: tt(2), End: tt(7)})
	got, ok := CommonInstant(a, b)
	if !ok || !got.Equal(tt(2)) {
		t.Fatalf("CommonInstant = %v, %v", got, ok)
	}
}

func TestTimeSpanString(t *testing.T) {
	s := MustNewTimeSpan(Interval{Start: tt(0), End: tt(1)})
	if got := fmt.Sprint(s); got == "" {
		t.Fatal("empty String()")
	}
}
