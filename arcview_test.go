package arcview

import (
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

func rat(n, d int64) *big.Rat { return big.NewRat(n, d) }

// --- naive reference model (independent of the sweep implementation) ---

func naiveContains(o Occluder, angle *big.Rat) bool {
	if o.Span.Sign() <= 0 {
		return false
	}
	if o.Span.Cmp(rat(360, 1)) >= 0 {
		return true
	}
	off := normAngle(new(big.Rat).Sub(angle, normAngle(o.Start)))
	return off.Sign() >= 0 && off.Cmp(o.Span) < 0
}

func naiveWinner(occs []Occluder, angle *big.Rat) int {
	best, bestPrio := -1, 0
	for _, o := range occs {
		if !naiveContains(o, angle) {
			continue
		}
		if best == -1 || o.Priority > bestPrio || (o.Priority == bestPrio && o.ID < best) {
			best, bestPrio = o.ID, o.Priority
		}
	}
	return best
}

// segmentContains reports whether angle lies in [Start, Start+Span),
// clockwise, possibly crossing 0.
func segmentContains(s Segment, angle *big.Rat) bool {
	off := normAngle(new(big.Rat).Sub(angle, s.Start))
	return off.Cmp(s.Span) < 0
}

func segmentLabelAt(t *testing.T, segs []Segment, angle *big.Rat) int {
	t.Helper()
	label, count := -2, 0
	for _, s := range segs {
		if segmentContains(s, angle) {
			label, count = s.ID, count+1
		}
	}
	if count != 1 {
		t.Fatalf("angle %v covered by %d segments", angle, count)
	}
	return label
}

// endpoints collects every angle where coverage may change, plus 0/360.
func endpoints(occs []Occluder) []*big.Rat {
	full := rat(360, 1)
	pts := []*big.Rat{rat(0, 1), rat(360, 1)}
	for _, o := range occs {
		if o.Span.Sign() <= 0 || o.Span.Cmp(full) >= 0 {
			continue
		}
		lo := normAngle(o.Start)
		hi := new(big.Rat).Add(lo, o.Span)
		if hi.Cmp(full) <= 0 {
			pts = append(pts, lo, hi)
		} else {
			pts = append(pts, lo, full, new(big.Rat).Sub(hi, full))
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].Cmp(pts[j]) < 0 })
	uniq := pts[:1]
	for _, p := range pts[1:] {
		if p.Cmp(uniq[len(uniq)-1]) != 0 {
			uniq = append(uniq, p)
		}
	}
	return uniq
}

// checkScene verifies a segmentation against the naive model by
// sampling the midpoint of every interval between consecutive
// endpoints, and checks partition + minimality properties.
func checkScene(t *testing.T, scene *Scene, occs []Occluder) {
	t.Helper()
	segs := scene.Segments()
	if len(segs) == 0 {
		t.Fatal("no segments returned")
	}

	// Partition: positive spans summing to exactly 360.
	total := new(big.Rat)
	for _, s := range segs {
		if s.Span.Sign() <= 0 {
			t.Fatalf("non-positive span %v", s.Span)
		}
		total.Add(total, s.Span)
	}
	if total.Cmp(rat(360, 1)) != 0 {
		t.Fatalf("spans sum to %v, want 360", total)
	}

	// Minimality: adjacent segments (incl. wrap-around) differ.
	if len(segs) > 1 {
		byStart := append([]Segment(nil), segs...)
		sort.Slice(byStart, func(i, j int) bool { return byStart[i].Start.Cmp(byStart[j].Start) < 0 })
		for i := range byStart {
			if byStart[i].ID == byStart[(i+1)%len(byStart)].ID {
				t.Fatalf("adjacent segments share ID %d: not minimal", byStart[i].ID)
			}
		}
	} else if segs[0].Span.Cmp(rat(360, 1)) != 0 {
		t.Fatalf("single segment has span %v, want 360", segs[0].Span)
	}

	// Sample midpoints between consecutive endpoints.
	pts := endpoints(occs)
	for i := 0; i+1 < len(pts); i++ {
		mid := new(big.Rat).Add(pts[i], pts[i+1])
		mid.Quo(mid, rat(2, 1))
		want := naiveWinner(occs, mid)
		if got := segmentLabelAt(t, segs, mid); got != want {
			t.Fatalf("midpoint %v: segments say %d, naive says %d", mid, got, want)
		}
		if got := scene.Query(mid); got != want {
			t.Fatalf("Query(%v) = %d, naive says %d", mid, got, want)
		}
	}
	// Also probe exact endpoints against the naive model.
	for _, p := range pts[:len(pts)-1] {
		if want := naiveWinner(occs, p); scene.Query(p) != want {
			t.Fatalf("Query(%v) = %d, naive says %d", p, scene.Query(p), want)
		}
	}
}

// --- targeted edge cases ---

func TestZeroSpanIsEmpty(t *testing.T) {
	s := NewScene()
	s.Upsert(Occluder{ID: 1, Start: rat(30, 1), Span: rat(0, 1), Priority: 5})
	segs := s.Segments()
	if len(segs) != 1 || segs[0].ID != -1 || segs[0].Span.Cmp(rat(360, 1)) != 0 {
		t.Fatalf("zero span should be empty, got %+v", segs)
	}
}

func TestFullCircleNotEmpty(t *testing.T) {
	s := NewScene()
	s.Upsert(Occluder{ID: 7, Start: rat(123, 1), Span: rat(360, 1), Priority: 1})
	segs := s.Segments()
	if len(segs) != 1 || segs[0].ID != 7 || segs[0].Span.Cmp(rat(360, 1)) != 0 {
		t.Fatalf("full circle must be one full segment, got %+v", segs)
	}
	for _, a := range []int64{0, 1, 90, 359, 3599} {
		if got := s.Query(rat(a*1234567, 10000)); got != 7 {
			t.Fatalf("Query = %d, want 7", got)
		}
	}
}

func TestFullCircleVsZeroSpan(t *testing.T) {
	s := NewScene()
	s.Upsert(Occluder{ID: 1, Start: rat(0, 1), Span: rat(360, 1), Priority: 1})
	s.Upsert(Occluder{ID: 2, Start: rat(0, 1), Span: rat(0, 1), Priority: 9})
	segs := s.Segments()
	if len(segs) != 1 || segs[0].ID != 1 {
		t.Fatalf("got %+v", segs)
	}
}

func TestWrapAroundInterval(t *testing.T) {
	s := NewScene()
	s.Upsert(Occluder{ID: 3, Start: rat(350, 1), Span: rat(20, 1), Priority: 0})
	segs := s.Segments()
	if len(segs) != 2 {
		t.Fatalf("want 2 segments, got %+v", segs)
	}
	var covered, empty Segment
	for _, seg := range segs {
		if seg.ID == 3 {
			covered = seg
		} else {
			empty = seg
		}
	}
	if covered.Start.Cmp(rat(350, 1)) != 0 || covered.Span.Cmp(rat(20, 1)) != 0 {
		t.Fatalf("covered segment = %+v, want start 350 span 20", covered)
	}
	if empty.Span.Cmp(rat(340, 1)) != 0 {
		t.Fatalf("empty segment = %+v, want span 340", empty)
	}
	if s.Query(rat(355, 1)) != 3 || s.Query(rat(5, 1)) != 3 {
		t.Fatal("wrapped arc should cover 355 and 5")
	}
	if s.Query(rat(10, 1)) != -1 || s.Query(rat(349, 1)) != -1 {
		t.Fatal("right-open at 10, not covering 349")
	}
}

func TestLeftClosedRightOpen(t *testing.T) {
	s := NewScene()
	s.Upsert(Occluder{ID: 1, Start: rat(90, 1), Span: rat(90, 1), Priority: 0})
	if s.Query(rat(90, 1)) != 1 {
		t.Fatal("left endpoint must be closed")
	}
	if s.Query(rat(180, 1)) != -1 {
		t.Fatal("right endpoint must be open")
	}
	if s.Query(rat(0, 1)) != -1 {
		t.Fatal("0 not covered")
	}
}

func TestPriorityAndTieBreak(t *testing.T) {
	s := NewScene()
	s.Upsert(Occluder{ID: 9, Start: rat(0, 1), Span: rat(100, 1), Priority: 1})
	s.Upsert(Occluder{ID: 5, Start: rat(50, 1), Span: rat(100, 1), Priority: 2})
	if got := s.Query(rat(75, 1)); got != 5 {
		t.Fatalf("higher priority should win, got %d", got)
	}
	// Same priority overlap: smaller ID wins.
	s.Upsert(Occluder{ID: 2, Start: rat(60, 1), Span: rat(20, 1), Priority: 2})
	if got := s.Query(rat(65, 1)); got != 2 {
		t.Fatalf("tie should go to min ID, got %d", got)
	}
}

func TestDeleteAndModify(t *testing.T) {
	s := NewScene()
	s.Upsert(Occluder{ID: 1, Start: rat(0, 1), Span: rat(180, 1), Priority: 0})
	s.Upsert(Occluder{ID: 2, Start: rat(90, 1), Span: rat(180, 1), Priority: 1})
	if got := s.Query(rat(100, 1)); got != 2 {
		t.Fatalf("got %d, want 2", got)
	}
	s.Delete(2)
	if got := s.Query(rat(100, 1)); got != 1 {
		t.Fatalf("after delete got %d, want 1", got)
	}
	if got := s.Query(rat(200, 1)); got != -1 {
		t.Fatalf("after delete got %d, want -1", got)
	}
	// Modify ID 1 in place: shrink to [0, 45).
	s.Upsert(Occluder{ID: 1, Start: rat(0, 1), Span: rat(45, 1), Priority: 0})
	if got := s.Query(rat(100, 1)); got != -1 {
		t.Fatalf("after modify got %d, want -1", got)
	}
	if got := s.Query(rat(44, 1)); got != 1 {
		t.Fatalf("after modify got %d, want 1", got)
	}
	s.Delete(999) // no-op
	checkScene(t, s, []Occluder{{ID: 1, Start: rat(0, 1), Span: rat(45, 1)}})
}

func TestRationalEndpoints(t *testing.T) {
	s := NewScene()
	s.Upsert(Occluder{ID: 1, Start: rat(1, 3), Span: rat(1, 3), Priority: 0})
	if got := s.Query(rat(1, 2)); got != 1 {
		t.Fatalf("1/2 in [1/3, 2/3), got %d", got)
	}
	if got := s.Query(rat(2, 3)); got != -1 {
		t.Fatalf("2/3 is right-open end, got %d", got)
	}
	if got := s.Query(rat(1, 3)); got != 1 {
		t.Fatalf("1/3 is left-closed end, got %d", got)
	}
}

// --- randomized cross-check against the naive sampling model ---

func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for trial := 0; trial < 500; trial++ {
		scene := NewScene()
		live := map[int]Occluder{}
		ops := 1 + rng.Intn(12)
		for op := 0; op < ops; op++ {
			switch rng.Intn(5) {
			case 0:
				if len(live) > 0 {
					ids := make([]int, 0, len(live))
					for id := range live {
						ids = append(ids, id)
					}
					victim := ids[rng.Intn(len(ids))]
					scene.Delete(victim)
					delete(live, victim)
					continue
				}
				fallthrough
			default:
				o := Occluder{
					ID:       rng.Intn(10),
					Start:    rat(int64(rng.Intn(1080)), int64(1+rng.Intn(6))),
					Span:     rat(int64(rng.Intn(361)), int64(1+rng.Intn(4))),
					Priority: rng.Intn(4),
				}
				if o.Span.Cmp(rat(360, 1)) > 0 {
					o.Span = rat(360, 1)
				}
				scene.Upsert(o)
				live[o.ID] = o
			}
		}
		occs := make([]Occluder, 0, len(live))
		for _, o := range live {
			occs = append(occs, o)
		}
		checkScene(t, scene, occs)
	}
}

func TestRandomFullCircleMix(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		scene := NewScene()
		live := map[int]Occluder{}
		for i := 0; i < 1+rng.Intn(6); i++ {
			span := rat(360, 1)
			if rng.Intn(2) == 0 {
				span = rat(int64(rng.Intn(360)), int64(1+rng.Intn(3)))
			}
			o := Occluder{
				ID:       i,
				Start:    rat(int64(rng.Intn(360)), int64(1+rng.Intn(5))),
				Span:     span,
				Priority: rng.Intn(3),
			}
			scene.Upsert(o)
			live[o.ID] = o
		}
		occs := make([]Occluder, 0, len(live))
		for _, o := range live {
			occs = append(occs, o)
		}
		checkScene(t, scene, occs)
	}
}
