package motion

import (
	"math"
	"testing"
)

// seg builds a segment; pv holds the position coordinates followed by the
// velocity coordinates (half and half).
func seg(start, end float64, pv ...float64) Segment {
	if len(pv)%2 != 0 {
		panic("seg: odd coordinate count")
	}
	n := len(pv) / 2
	p := make(Vec, n)
	copy(p, pv[:n])
	v := make(Vec, n)
	copy(v, pv[n:])
	return Segment{Start: start, End: end, Pos: p, Vel: v}
}

func traj(segs ...Segment) Trajectory { return Trajectory{Segments: segs} }

func checkIntervals(t *testing.T, name string, got []Interval, want []Interval) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d intervals %v, want %d %v", name, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: interval %d = %+v, want %+v", name, i, got[i], want[i])
		}
	}
}

func TestHandCrafted(t *testing.T) {
	still := traj(seg(0, 10, 0, 0, 0, 0)) // stationary at origin

	cases := []struct {
		name string
		a, b Trajectory
		thr  float64
		want []Interval
	}{
		{
			// Both endpoints outside, middle inside: proves endpoint
			// sampling alone is insufficient. (t-5)^2 <= 4 -> [3,7].
			"dip below threshold",
			still, traj(seg(0, 10, -5, 0, 1, 0)), 4,
			[]Interval{{3, 7, true}},
		},
		{
			"stationary within threshold",
			still, traj(seg(0, 10, 1, 1, 0, 0)), 4,
			[]Interval{{0, 10, false}},
		},
		{
			"stationary exactly at threshold",
			still, traj(seg(0, 10, 2, 0, 0, 0)), 4,
			[]Interval{{0, 10, false}},
		},
		{
			"stationary outside threshold",
			still, traj(seg(0, 10, 3, 0, 0, 0)), 4,
			nil,
		},
		{
			// (t-3)^2 + 4 <= 4 only at t = 3: grazing point contact.
			"grazing tangential contact",
			still, traj(seg(0, 10, -3, 2, 1, 0)), 4,
			[]Interval{{3, 3, true}},
		},
		{
			// Jump onto the threshold circle at the breakpoint: t=1 counts.
			"jump into range at breakpoint",
			still, traj(seg(0, 1, 10, 0, 0, 0), seg(1, 2, 1, 0, 0, 0)), 4,
			[]Interval{{1, 2, false}},
		},
		{
			// In range before the jump, far after: t=1 does NOT count.
			"jump out of range at breakpoint",
			still, traj(seg(0, 1, 1, 0, 0, 0), seg(1, 2, 10, 0, 0, 0)), 4,
			[]Interval{{0, 1, false}},
		},
		{
			// Continuous motion across a breakpoint: pieces merge.
			"continuous across breakpoint merges",
			still, traj(seg(0, 1, 0, 0, 1, 0), seg(1, 2, 1, 0, 1, 0)), 4,
			[]Interval{{0, 2, false}},
		},
		{
			// Distance exactly at threshold at the shared breakpoint;
			// the instant belongs to the union via the second piece.
			"threshold touched at shared breakpoint",
			still, traj(seg(0, 1, 0, 0, 1, 0), seg(1, 2, 1, 0, -1, 0)), 1,
			[]Interval{{0, 2, false}},
		},
		{
			// First piece inside (dist^2 == 1 constantly), second piece
			// tangent at its start: point [1,1] merges, closing the end.
			"grazing point at breakpoint closes previous piece",
			still, traj(seg(0, 1, 1, 0, 0, 0), seg(1, 2, 1, 0, 0, 1)), 1,
			[]Interval{{0, 1, true}},
		},
		{
			// Exit root lands exactly on the breakpoint, but the next
			// segment jumped far away: end stays open.
			"root at breakpoint does not close end",
			still, traj(seg(0, 1, -1, 0, 1, 0), seg(1, 2, 5, 0, 0, 0)), 1,
			[]Interval{{0, 1, false}},
		},
		{
			// Grazing minimum exactly at the breakpoint, reached only
			// from the left piece: the instant itself is far -> empty.
			"grazing at breakpoint from the left excluded",
			still, traj(seg(0, 1, -1, 1, 1, 0), seg(1, 2, 3, 0, 0, 0)), 1,
			nil,
		},
		{
			// Both trajectories jump simultaneously.
			"simultaneous jump",
			traj(seg(0, 1, 0, 0, 0, 0), seg(1, 2, 100, 0, 0, 0)),
			traj(seg(0, 2, 0, 0, 0, 0)), 4,
			[]Interval{{0, 1, false}},
		},
		{
			// Zero threshold: exact rendezvous at a single instant.
			"zero threshold point contact",
			traj(seg(0, 2, 0, 0, 1, 0)), traj(seg(0, 2, 0, 1, 1, -1)), 0,
			[]Interval{{1, 1, true}},
		},
		{
			// Coverage gap in a: no result over [1,2).
			"coverage gap",
			traj(seg(0, 1, 0, 0, 0, 0), seg(2, 3, 0, 0, 0, 0)),
			traj(seg(0, 3, 0.5, 0, 0, 0)), 1,
			[]Interval{{0, 1, false}, {2, 3, false}},
		},
		{
			// near -> far -> near: two separated intervals.
			"two separated intervals",
			still, traj(seg(0, 1, 0, 0, 0, 0), seg(1, 2, 10, 0, 0, 0), seg(2, 3, 0, 0, 0, 0)), 4,
			[]Interval{{0, 1, false}, {2, 3, false}},
		},
		{
			// 3D: (t-3)^2 + 4 <= 8 -> t in [1,5], clipped to [0,4).
			"3d enter inside exit beyond",
			traj(seg(0, 4, 0, 0, 0, 0, 0, 0)),
			traj(seg(0, 4, -3, 0, 2, 1, 0, 0)), 8,
			[]Interval{{1, 4, false}},
		},
		{
			"empty trajectory",
			Trajectory{}, still, 4, nil,
		},
	}
	for _, c := range cases {
		got, err := Intersect(c.a, c.b, c.thr)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		checkIntervals(t, c.name, got, c.want)
		checkSemantics(t, c.name, c.a, c.b, c.thr, got)
	}
}

func TestValidation(t *testing.T) {
	if _, err := Intersect(traj(seg(0, 0, 0, 0, 0, 0)), traj(seg(0, 1, 0, 0, 0, 0)), 1); err == nil {
		t.Error("zero-length segment accepted")
	}
	if _, err := Intersect(traj(seg(0, 2, 0, 0, 0, 0), seg(1, 3, 0, 0, 0, 0)), traj(seg(0, 1, 0, 0, 0, 0)), 1); err == nil {
		t.Error("overlapping segments accepted")
	}
	badDim := Trajectory{Segments: []Segment{{Start: 0, End: 1, Pos: Vec{0, 0}, Vel: Vec{0}}}}
	if _, err := Intersect(badDim, traj(seg(0, 1, 0, 0, 0, 0)), 1); err == nil {
		t.Error("pos/vel dimension mismatch accepted")
	}
	if _, err := Intersect(traj(seg(0, 1, 0, 0, 0, 0)), traj(seg(0, 1, 0, 0, 0, 0)), -1); err == nil {
		t.Error("negative threshold accepted")
	}
	if _, err := Intersect(traj(seg(0, 1, 0, 0, 0, 0)), traj(seg(0, 1, 0, 0, 0, 0)), math.NaN()); err == nil {
		t.Error("NaN threshold accepted")
	}
	traj3d := Trajectory{Segments: []Segment{{Start: 0, End: 1, Pos: Vec{0, 0, 0}, Vel: Vec{0, 0, 0}}}}
	if _, err := Intersect(traj(seg(0, 1, 0, 0, 0, 0)), traj3d, 1); err == nil {
		t.Error("trajectory dimension mismatch accepted")
	}
	if _, err := Intersect(traj(seg(math.NaN(), 1, 0, 0, 0, 0)), traj(seg(0, 1, 0, 0, 0, 0)), 1); err == nil {
		t.Error("NaN segment bound accepted")
	}
}

func TestAt(t *testing.T) {
	tr := traj(seg(0, 1, 0, 0, 2, 0), seg(2, 4, 10, 0, 0, 0))
	if _, ok := tr.At(-0.5); ok {
		t.Error("At before first segment")
	}
	p, ok := tr.At(0.5)
	if !ok || p[0] != 1 || p[1] != 0 {
		t.Errorf("At(0.5) = %v, %v", p, ok)
	}
	if _, ok := tr.At(1.5); ok {
		t.Error("At inside coverage gap")
	}
	p, ok = tr.At(2) // breakpoint belongs to the second segment
	if !ok || p[0] != 10 {
		t.Errorf("At(2) = %v, %v", p, ok)
	}
	if _, ok := tr.At(4); ok {
		t.Error("At after last segment")
	}
}
