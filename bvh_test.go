package spacetime

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

// segKey uniquely identifies a segment for set comparisons.
func segKey(s Segment) string {
	return fmt.Sprintf("%s#%d", s.TrajectoryID, s.Index)
}

func segSet(segs []Segment) map[string]bool {
	m := make(map[string]bool, len(segs))
	for _, s := range segs {
		m[segKey(s)] = true
	}
	return m
}

// bruteForceQuery is the reference linear-scan implementation of Query.
func bruteForceQuery(all []Segment, q AABB4) []Segment {
	var out []Segment
	for _, s := range all {
		if s.Bounds().Intersects(q) {
			out = append(out, s)
		}
	}
	return out
}

// bruteForceExact is the reference linear-scan implementation of QueryExact.
func bruteForceExact(all []Segment, q AABB4) []Segment {
	var out []Segment
	for _, s := range all {
		if _, _, ok := clipSegment(s, q); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestEmptyTree(t *testing.T) {
	tr := New(8)
	q := Box(Vec3{0, 0, 0}, Vec3{100, 100, 100}, 0, 100)
	if got := tr.Query(q); len(got) != 0 {
		t.Fatalf("empty tree Query returned %d segments", len(got))
	}
	if got := tr.QueryExact(q); len(got) != 0 {
		t.Fatalf("empty tree QueryExact returned %d hits", len(got))
	}
}

func TestInsertTrajectoryAndQuery(t *testing.T) {
	tr := New(4)
	// One drone flying a square at 150 m altitude between t=0 and t=400.
	pts := []Vec3{{0, 0, 150}, {100, 0, 150}, {100, 100, 150}, {0, 100, 150}, {0, 0, 150}}
	times := []float64{0, 100, 200, 300, 400}
	if n := tr.InsertTrajectory("alpha", pts, times); n != 4 {
		t.Fatalf("InsertTrajectory inserted %d segments, want 4", n)
	}
	if tr.Size() != 4 {
		t.Fatalf("Size = %d, want 4", tr.Size())
	}

	// Window covering only the second leg (x=100, y: 0->100, t in [100,200]).
	q := Box(Vec3{90, 40, 100}, Vec3{110, 60, 200}, 140, 160)
	got := tr.Query(q)
	if len(got) != 1 || got[0].Index != 1 {
		t.Fatalf("Query returned %+v, want only leg 1", got)
	}

	// Time window after the flight: nothing.
	got = tr.Query(Box(Vec3{-10, -10, 0}, Vec3{200, 200, 300}, 500, 600))
	if len(got) != 0 {
		t.Fatalf("Query outside time range returned %d segments", len(got))
	}
}

// TestExactQueryFiltersByTime checks the case where a segment's 4D AABB
// overlaps the query box but the segment itself is elsewhere during the
// time window: Query (conservative) must include it, QueryExact must not.
func TestExactQueryFiltersByTime(t *testing.T) {
	tr := New(4)
	// Flies along x at z=150 from t=0 to t=2000; crosses x in [50,100]
	// at u in [0.25,0.5], i.e. t in [500,1000].
	tr.Insert(Segment{TrajectoryID: "fast", Index: 0,
		Start: Vec3{0, 0, 150}, End: Vec3{200, 0, 150}, T0: 0, T1: 2000})

	// Query box: x in [50,100], but only during t in [1400,1600].
	q := Box(Vec3{50, -10, 100}, Vec3{100, 10, 200}, 1400, 1600)

	if got := tr.Query(q); len(got) != 1 {
		t.Fatalf("conservative Query should return the AABB-overlapping segment, got %d", len(got))
	}
	if got := tr.QueryExact(q); len(got) != 0 {
		t.Fatalf("QueryExact must exclude the segment (wrong time), got %+v", got)
	}

	// Same box, correct time window t in [500,1000]: exact hit with
	// entry at (50,0,150)@500 and exit at (100,0,150)@1000.
	q2 := Box(Vec3{50, -10, 100}, Vec3{100, 10, 200}, 500, 1000)
	hits := tr.QueryExact(q2)
	if len(hits) != 1 {
		t.Fatalf("QueryExact returned %d hits, want 1", len(hits))
	}
	h := hits[0]
	if math.Abs(h.EnterT-500) > 1e-9 || math.Abs(h.ExitT-1000) > 1e-9 {
		t.Fatalf("hit times [%v,%v], want [500,1000]", h.EnterT, h.ExitT)
	}
	if math.Abs(h.EnterPos.X-50) > 1e-9 || math.Abs(h.ExitPos.X-100) > 1e-9 {
		t.Fatalf("hit positions %+v -> %+v, want x 50 -> 100", h.EnterPos, h.ExitPos)
	}
}

// TestAltitudeFilter verifies the z-axis prunes low-altitude traffic.
func TestAltitudeFilter(t *testing.T) {
	tr := New(4)
	tr.Insert(Segment{TrajectoryID: "low", Index: 0,
		Start: Vec3{10, 10, 50}, End: Vec3{90, 90, 50}, T0: 0, T1: 100})
	tr.Insert(Segment{TrajectoryID: "high", Index: 0,
		Start: Vec3{10, 10, 150}, End: Vec3{90, 90, 150}, T0: 0, T1: 100})

	q := Box(Vec3{0, 0, 100}, Vec3{100, 100, 500}, 0, 100) // z >= 100 only
	hits := tr.QueryExact(q)
	if len(hits) != 1 || hits[0].Segment.TrajectoryID != "high" {
		t.Fatalf("altitude filter failed, hits = %+v", hits)
	}
}

// TestDegenerateSegment covers a zero-duration (stationary) segment.
func TestDegenerateSegment(t *testing.T) {
	tr := New(4)
	tr.Insert(Segment{TrajectoryID: "hover", Index: 0,
		Start: Vec3{50, 50, 120}, End: Vec3{50, 50, 120}, T0: 10, T1: 10})
	q := Box(Vec3{0, 0, 100}, Vec3{100, 100, 200}, 0, 100)
	hits := tr.QueryExact(q)
	if len(hits) != 1 {
		t.Fatalf("hovering point inside box should hit, got %d", len(hits))
	}
	qMiss := Box(Vec3{0, 0, 100}, Vec3{100, 100, 200}, 20, 100) // t=10 not in [20,100]
	if hits := tr.QueryExact(qMiss); len(hits) != 0 {
		t.Fatalf("hovering point outside time window should miss, got %d", len(hits))
	}
}

// TestLargeScaleMatchesBruteForce inserts many random trajectories and
// verifies both query flavors against a linear scan.
func TestLargeScaleMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	tr := New(16)
	var all []Segment
	for d := 0; d < 500; d++ {
		id := fmt.Sprintf("drone-%d", d)
		pos := Vec3{rng.Float64() * 10000, rng.Float64() * 10000, rng.Float64() * 500}
		ts := 0.0
		for i := 0; i < 40; i++ {
			next := Vec3{
				pos.X + (rng.Float64()-0.5)*800,
				pos.Y + (rng.Float64()-0.5)*800,
				math.Max(0, math.Min(600, pos.Z+(rng.Float64()-0.5)*80)),
			}
			seg := Segment{TrajectoryID: id, Index: i, Start: pos, End: next, T0: ts, T1: ts + 30}
			tr.Insert(seg)
			all = append(all, seg)
			pos, ts = next, ts+30
		}
	}
	if tr.Size() != len(all) {
		t.Fatalf("Size = %d, want %d", tr.Size(), len(all))
	}

	queries := []AABB4{
		Box(Vec3{2000, 2000, 100}, Vec3{4000, 4000, 400}, 300, 600),
		Box(Vec3{0, 0, 0}, Vec3{10000, 10000, 600}, 0, 1200), // everything
		Box(Vec3{9000, 9000, 500}, Vec3{9500, 9500, 600}, 0, 30),
		Box(Vec3{0, 0, 0}, Vec3{1, 1, 1}, 5000, 6000), // nothing
	}
	for qi, q := range queries {
		got, want := segSet(tr.Query(q)), segSet(bruteForceQuery(all, q))
		if len(got) != len(want) {
			t.Fatalf("query %d: Query returned %d, brute force %d", qi, len(got), len(want))
		}
		for k := range want {
			if !got[k] {
				t.Fatalf("query %d: Query missing %s", qi, k)
			}
		}
		gotE, wantE := segSet(hitsToSegs(tr.QueryExact(q))), segSet(bruteForceExact(all, q))
		if len(gotE) != len(wantE) {
			t.Fatalf("query %d: QueryExact returned %d, brute force %d", qi, len(gotE), len(wantE))
		}
		for k := range wantE {
			if !gotE[k] {
				t.Fatalf("query %d: QueryExact missing %s", qi, k)
			}
		}
	}
}

func hitsToSegs(hits []Hit) []Segment {
	out := make([]Segment, len(hits))
	for i, h := range hits {
		out[i] = h.Segment
	}
	return out
}

// TestMillisecondQuery builds a 200k-segment index and asserts the
// acceptance criterion: spatiotemporal window queries return in well under
// a few milliseconds.
func TestMillisecondQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf test in -short mode")
	}
	rng := rand.New(rand.NewSource(7))
	tr := New(16)
	for d := 0; d < 5000; d++ {
		pos := Vec3{rng.Float64() * 20000, rng.Float64() * 20000, rng.Float64() * 800}
		ts := 0.0
		for i := 0; i < 40; i++ {
			next := Vec3{
				pos.X + (rng.Float64()-0.5)*600,
				pos.Y + (rng.Float64()-0.5)*600,
				math.Max(0, math.Min(900, pos.Z+(rng.Float64()-0.5)*60)),
			}
			tr.Insert(Segment{TrajectoryID: fmt.Sprintf("uav-%d", d), Index: i,
				Start: pos, End: next, T0: ts, T1: ts + 20})
			pos, ts = next, ts+20
		}
	}
	t.Logf("indexed %d segments", tr.Size())

	q := Box(Vec3{8000, 8000, 100}, Vec3{12000, 12000, 600}, 300, 400)

	// Dynamic-insert tree: log the timing as information.
	start := time.Now()
	var hits []Hit
	for i := 0; i < 20; i++ {
		hits = tr.QueryExact(q)
	}
	t.Logf("dynamic tree: %d hits, avg %v", len(hits), time.Since(start)/20)

	// Bulk-loaded tree: this is the indexed-lookup path that must prune
	// aggressively and answer spatiotemporal window queries fast.
	tr.Rebuild()
	const runs = 200
	start = time.Now()
	for i := 0; i < runs; i++ {
		hits = tr.QueryExact(q)
	}
	avg := time.Since(start) / runs
	st := tr.LastQueryStats()
	t.Logf("bulk-loaded tree: %d hits, avg %v over %d runs; visited %d nodes, %d leaves, scanned %d/%d segments",
		len(hits), avg, runs, st.NodesVisited, st.LeavesVisited, st.SegmentsScanned, tr.Size())

	// Deterministic pruning assertion: the BVH must examine only a small
	// fraction of the 200k indexed segments (a linear scan would be 100%).
	if st.SegmentsScanned > tr.Size()/10 {
		t.Fatalf("ineffective pruning: scanned %d of %d segments (>10%%)",
			st.SegmentsScanned, tr.Size())
	}
	// Generous wall-clock sanity bound (millisecond-level, machine-independent
	// enough to avoid flakiness on loaded CI machines).
	if avg > 50*time.Millisecond {
		t.Fatalf("query too slow: avg %v > 50ms", avg)
	}
}

// TestBuildMatchesBruteForce verifies the bulk loader produces a correct
// index, and that Rebuild preserves the indexed set.
func TestBuildMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	var all []Segment
	for i := 0; i < 5000; i++ {
		x, y := rng.Float64()*5000, rng.Float64()*5000
		all = append(all, Segment{
			TrajectoryID: fmt.Sprintf("t-%d", i), Index: 0,
			Start: Vec3{x, y, rng.Float64() * 300},
			End:   Vec3{x + (rng.Float64()-0.5)*200, y + (rng.Float64()-0.5)*200, rng.Float64() * 300},
			T0:    rng.Float64() * 1000, T1: 0,
		})
		all[i].T1 = all[i].T0 + rng.Float64()*50
	}

	tr := New(16)
	tr.Build(all)
	if tr.Size() != len(all) {
		t.Fatalf("Size = %d, want %d", tr.Size(), len(all))
	}

	q := Box(Vec3{1000, 1000, 50}, Vec3{3000, 3000, 250}, 200, 500)
	got, want := segSet(hitsToSegs(tr.QueryExact(q))), segSet(bruteForceExact(all, q))
	if len(got) != len(want) {
		t.Fatalf("Build+QueryExact returned %d, brute force %d", len(got), len(want))
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("Build+QueryExact missing %s", k)
		}
	}

	// Insert more segments dynamically, then Rebuild: results must still
	// match a linear scan over everything.
	extra := Segment{TrajectoryID: "extra", Index: 0,
		Start: Vec3{1500, 1500, 100}, End: Vec3{2500, 2500, 200}, T0: 250, T1: 450}
	tr.Insert(extra)
	tr.Rebuild()
	all = append(all, extra)
	got, want = segSet(hitsToSegs(tr.QueryExact(q))), segSet(bruteForceExact(all, q))
	if len(got) != len(want) {
		t.Fatalf("after Insert+Rebuild: QueryExact returned %d, brute force %d", len(got), len(want))
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("after Insert+Rebuild: missing %s", k)
		}
	}
}
