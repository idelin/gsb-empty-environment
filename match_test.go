package mapmatch

import (
	"math"
	"testing"
)

// xy converts local meter offsets (x east, y north of a reference) to a Point.
func xy(ref Point, x, y float64) Point {
	return fromLocalXY(x, y, ref)
}

var refPoint = Point{Lat: 31.2304, Lon: 121.4737} // downtown Shanghai

// buildCanyonNetwork builds a city-canyon network: a main road (y=0) and a
// parallel street (y=60m), each 1km long, connected by three cross streets.
func buildCanyonNetwork() *Graph {
	var nodes []Point
	for i := 0; i <= 10; i++ { // main road nodes 0..10
		nodes = append(nodes, xy(refPoint, float64(i*100), 0))
	}
	for i := 0; i <= 10; i++ { // parallel road nodes 11..21
		nodes = append(nodes, xy(refPoint, float64(i*100), 60))
	}
	g := NewGraph(nodes)
	for i := 0; i < 10; i++ {
		g.AddEdge("Main", i, i+1, []Point{nodes[i], nodes[i+1]})
	}
	for i := 11; i < 21; i++ {
		g.AddEdge("Parallel", i, i+1, []Point{nodes[i], nodes[i+1]})
	}
	for _, x := range []int{0, 5, 10} { // cross streets at x=0, 500, 1000
		g.AddEdge("Cross", x, x+11, []Point{nodes[x], nodes[x+11]})
	}
	return g
}

// noisyTrace simulates GPS fixes along the main road (y=0, 10 m/s) corrupted
// by deterministic noise plus severe multipath drift toward the parallel
// street on a stretch of fixes (the "city canyon" effect).
func noisyTrace() (obs []Point, ts []float64, truth []Point) {
	driftIdx := map[int]bool{6: true, 7: true, 8: true, 13: true}
	for i := 0; i < 19; i++ {
		x := 50 + float64(i)*50
		truth = append(truth, xy(refPoint, x, 0))
		ts = append(ts, float64(i)*5)
		// deterministic pseudo-noise, ~10m amplitude
		nx := 8 * math.Sin(float64(i)*2.3)
		ny := 9 * math.Cos(float64(i)*1.7)
		if driftIdx[i] {
			ny += 55 // multipath: fix jumps toward the parallel street
		}
		obs = append(obs, xy(refPoint, x+nx, ny))
	}
	return obs, ts, truth
}

func meanErr(results []MatchResult, truth []Point) float64 {
	var sum float64
	for _, r := range results {
		sum += haversine(r.Cand.Point, truth[r.Index])
	}
	return sum / float64(len(results))
}

func rawErr(obs, truth []Point) float64 {
	var sum float64
	for i := range obs {
		sum += haversine(obs[i], truth[i])
	}
	return sum / float64(len(obs))
}

// TestMatchCityCanyon is the acceptance test: heavily drifting GPS fixes must
// be snapped back onto the true road (the main road, not the parallel one).
func TestMatchCityCanyon(t *testing.T) {
	g := buildCanyonNetwork()
	obs, ts, truth := noisyTrace()

	m := NewMatcher(g)
	m.Sigma = 25 // city-canyon GPS error is large
	m.Beta = 20
	m.MaxSpeed = 30
	m.CandidateRadius = 80

	results := m.Match(obs, ts)
	if len(results) != len(obs) {
		t.Fatalf("expected %d results, got %d", len(obs), len(results))
	}

	for _, r := range results {
		if r.Cand.Edge.Name != "Main" {
			t.Errorf("obs %d matched to %q, want Main", r.Index, r.Cand.Edge.Name)
		}
		// matched point must lie (almost) on the centerline
		_, _, d := projectPoint(r.Cand.Point, r.Cand.Edge.Line)
		if d > 0.5 {
			t.Errorf("obs %d: matched point %.2fm off centerline", r.Index, d)
		}
	}

	mErr := meanErr(results, truth)
	rErr := rawErr(obs, truth)
	t.Logf("raw GPS mean error:      %.2f m", rErr)
	t.Logf("matched mean error:      %.2f m", mErr)
	if mErr >= rErr {
		t.Errorf("matching did not improve accuracy: matched %.2fm >= raw %.2fm", mErr, rErr)
	}
	if mErr > 15 {
		t.Errorf("matched mean error %.2fm exceeds 15m", mErr)
	}
}

// TestMatchOutlierSkipped verifies that fixes with no nearby road are dropped.
func TestMatchOutlierSkipped(t *testing.T) {
	g := buildCanyonNetwork()
	obs, ts, _ := noisyTrace()
	obs[9] = xy(refPoint, 500, 5000) // 5km away from any road
	m := NewMatcher(g)
	results := m.Match(obs, ts)
	if len(results) != len(obs)-1 {
		t.Fatalf("expected %d results, got %d", len(obs)-1, len(results))
	}
	for _, r := range results {
		if r.Index == 9 {
			t.Errorf("outlier fix 9 should have been skipped")
		}
	}
}

func TestProjectPoint(t *testing.T) {
	line := []Point{xy(refPoint, 0, 0), xy(refPoint, 100, 0)}
	proj, offset, dist := projectPoint(xy(refPoint, 30, 10), line)
	if math.Abs(offset-30) > 0.5 {
		t.Errorf("offset = %.2f, want ~30", offset)
	}
	if math.Abs(dist-10) > 0.5 {
		t.Errorf("dist = %.2f, want ~10", dist)
	}
	if d := haversine(proj, xy(refPoint, 30, 0)); d > 0.5 {
		t.Errorf("projection is %.2fm away from expected point", d)
	}
	// beyond the segment end: clamps to endpoint
	_, offset, _ = projectPoint(xy(refPoint, 150, 0), line)
	if math.Abs(offset-100) > 0.5 {
		t.Errorf("offset = %.2f, want ~100 (clamped)", offset)
	}
}

func TestDijkstra(t *testing.T) {
	g := buildCanyonNetwork()
	dist := g.dijkstra(0)
	if math.Abs(dist[10]-1000) > 1 {
		t.Errorf("dist(0->10) = %.1f, want ~1000", dist[10])
	}
	// 0 -> 11 is the 60m cross street; 0 -> 16 via main road + cross at x=500
	if math.Abs(dist[11]-60) > 1 {
		t.Errorf("dist(0->11) = %.1f, want ~60", dist[11])
	}
	if math.Abs(dist[16]-560) > 1 {
		t.Errorf("dist(0->16) = %.1f, want ~560", dist[16])
	}
}

func TestRouteDistance(t *testing.T) {
	g := buildCanyonNetwork()
	main := g.Edges[4] // Main edge x=400..500
	par := g.Edges[14] // Parallel edge x=400..500
	a := Candidate{Edge: main, Offset: 50}
	b := Candidate{Edge: par, Offset: 50}
	sp := map[int][]float64{
		main.From: g.dijkstra(main.From),
		main.To:   g.dijkstra(main.To),
	}
	// must go down a cross street and back up: 50+60+... shortest is
	// 50 (to x=400) ... actually via x=500 connector: 50+60+50 = 160
	if d := routeDistance(a, b, sp); math.Abs(d-160) > 1 {
		t.Errorf("routeDistance = %.1f, want ~160", d)
	}
	// same edge: direct offset difference
	c := Candidate{Edge: main, Offset: 80}
	if d := routeDistance(a, c, sp); math.Abs(d-30) > 1e-9 {
		t.Errorf("same-edge routeDistance = %.1f, want 30", d)
	}
}
