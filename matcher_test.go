package mapmatch

import (
	"math"
	"math/rand"
	"testing"
)

const (
	baseLat = 31.2304 // Shanghai
	baseLon = 121.4737
)

// xyToPoint converts local meters (east, north) to WGS84 around baseLat/Lon.
func xyToPoint(x, y float64) Point {
	return Point{
		Lat: baseLat + y/metersPerDegLat,
		Lon: baseLon + x/(metersPerDegLat*math.Cos(baseLat*math.Pi/180)),
	}
}

// buildGrid builds a 4x4 street grid with 100 m blocks, all streets two-way.
func buildGrid() *Graph {
	const n = 4
	const cell = 100.0
	nodes := make([]Point, 0, n*n)
	for iy := 0; iy < n; iy++ {
		for ix := 0; ix < n; ix++ {
			nodes = append(nodes, xyToPoint(float64(ix)*cell, float64(iy)*cell))
		}
	}
	g := NewGraph(nodes)
	idx := func(ix, iy int) int { return iy*n + ix }
	for iy := 0; iy < n; iy++ {
		for ix := 0; ix < n; ix++ {
			if ix+1 < n {
				g.AddUndirected(idx(ix, iy), idx(ix+1, iy))
			}
			if iy+1 < n {
				g.AddUndirected(idx(ix, iy), idx(ix, iy+1))
			}
		}
	}
	return g
}

// samplePath samples positions every stepM meters along a polyline of
// waypoints (in local meters), returning local xy positions.
func samplePath(waypoints [][2]float64, stepM float64) [][2]float64 {
	var out [][2]float64
	for i := 0; i+1 < len(waypoints); i++ {
		ax, ay := waypoints[i][0], waypoints[i][1]
		bx, by := waypoints[i+1][0], waypoints[i+1][1]
		segLen := math.Hypot(bx-ax, by-ay)
		for d := 0.0; d < segLen; d += stepM {
			t := d / segLen
			out = append(out, [2]float64{ax + t*(bx-ax), ay + t*(by-ay)})
		}
	}
	out = append(out, waypoints[len(waypoints)-1])
	return out
}

// TestHaversine checks a known distance: 1 degree of latitude ~ 111.2 km.
func TestHaversine(t *testing.T) {
	d := Haversine(Point{Lat: 0, Lon: 0}, Point{Lat: 1, Lon: 0})
	if math.Abs(d-111195) > 500 {
		t.Fatalf("haversine 1deg lat = %.0f m, want ~111195 m", d)
	}
}

// TestProjectPoint checks perpendicular projection onto a segment.
func TestProjectPoint(t *testing.T) {
	a := xyToPoint(0, 0)
	b := xyToPoint(100, 0)
	p := xyToPoint(50, 30)
	matched, frac, dist := projectPoint(p, a, b)
	if math.Abs(frac-0.5) > 1e-9 {
		t.Fatalf("frac = %v, want 0.5", frac)
	}
	if math.Abs(dist-30) > 0.5 {
		t.Fatalf("dist = %v, want ~30", dist)
	}
	if d := Haversine(matched, xyToPoint(50, 0)); d > 0.5 {
		t.Fatalf("matched point off by %v m", d)
	}
}

// TestShortestPath checks Dijkstra on a small graph.
func TestShortestPath(t *testing.T) {
	nodes := []Point{xyToPoint(0, 0), xyToPoint(100, 0), xyToPoint(200, 0)}
	g := NewGraph(nodes)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	if d := g.ShortestPath(0, 2); math.Abs(d-200) > 1 {
		t.Fatalf("shortest path = %v, want ~200", d)
	}
	if d := g.ShortestPath(2, 0); !math.IsInf(d, 1) {
		t.Fatalf("reverse path should be unreachable, got %v", d)
	}
}

// TestRouteDistanceSameEdge checks forward travel along a single edge.
func TestRouteDistanceSameEdge(t *testing.T) {
	nodes := []Point{xyToPoint(0, 0), xyToPoint(100, 0)}
	g := NewGraph(nodes)
	id := g.AddEdge(0, 1)
	a := Candidate{EdgeID: id, Fraction: 0.2}
	b := Candidate{EdgeID: id, Fraction: 0.7}
	if d := g.RouteDistance(a, b); math.Abs(d-50) > 1 {
		t.Fatalf("route distance = %v, want ~50", d)
	}
}

// TestMatchCityCanyon simulates an urban-canyon trajectory: a vehicle drives
// east along the y=100 street, then north along the x=300 street. GPS noise
// (sigma=12 m) plus severe multipath outliers (60-100 m jumps) are added.
// The matcher must snap the trace back onto the true path.
func TestMatchCityCanyon(t *testing.T) {
	g := buildGrid()
	m := NewMatcher(g)

	// Ground truth: (0,100)->(300,100)->(300,300), 8 m/s, 2 s sampling.
	waypoints := [][2]float64{{0, 100}, {300, 100}, {300, 300}}
	truth := samplePath(waypoints, 16)

	rng := rand.New(rand.NewSource(7))
	var obs []GPSPoint
	outlierIdx := map[int]bool{}
	for i, pos := range truth {
		nx := rng.NormFloat64() * 12
		ny := rng.NormFloat64() * 12
		// Multipath: every 9th point jumps 50-80 m perpendicular to the
		// direction of travel, toward a parallel street. Points in the
		// last two samples are exempt: without future observations even
		// an exact decoder cannot tell a drifted point apart from a real
		// turn onto a cross street near the destination.
		if i%9 == 4 && i+3 < len(truth) {
			outlierIdx[i] = true
			var dirX, dirY float64
			if i+1 < len(truth) {
				dirX = truth[i+1][0] - pos[0]
				dirY = truth[i+1][1] - pos[1]
			} else {
				dirX = pos[0] - truth[i-1][0]
				dirY = pos[1] - truth[i-1][1]
			}
			norm := math.Hypot(dirX, dirY)
			dirX, dirY = dirX/norm, dirY/norm
			sign := 1.0
			if rng.Float64() < 0.5 {
				sign = -1
			}
			jump := 50 + rng.Float64()*30
			nx += sign * -dirY * jump
			ny += sign * dirX * jump
		}
		p := xyToPoint(pos[0]+nx, pos[1]+ny)
		obs = append(obs, GPSPoint{Lat: p.Lat, Lon: p.Lon, T: float64(i) * 2})
	}

	matches := m.Match(obs)
	if len(matches) != len(obs) {
		t.Fatalf("got %d matches, want %d", len(matches), len(obs))
	}

	var rawSum, matchedSum float64
	var matchedWorst float64
	within20 := 0
	for i, mt := range matches {
		if mt.EdgeID < 0 {
			t.Fatalf("observation %d unmatched", i)
		}
		tp := xyToPoint(truth[i][0], truth[i][1])
		rawErr := Haversine(Point{Lat: obs[i].Lat, Lon: obs[i].Lon}, tp)
		mErr := Haversine(mt.Point, tp)
		rawSum += rawErr
		matchedSum += mErr
		if mErr > matchedWorst {
			matchedWorst = mErr
		}
		if mErr <= 20 {
			within20++
		}
		if outlierIdx[i] && mErr > 30 {
			t.Fatalf("multipath outlier %d not corrected: matched error %.1f m (raw %.1f m)", i, mErr, rawErr)
		}
	}
	rawAvg := rawSum / float64(len(obs))
	matchedAvg := matchedSum / float64(len(obs))
	t.Logf("raw avg error %.1f m -> matched avg error %.1f m (worst %.1f m, %d/%d within 20 m)",
		rawAvg, matchedAvg, matchedWorst, within20, len(obs))

	// Snapping removes the perpendicular noise component but cannot recover
	// along-track jitter, so the theoretical floor is ~sigma*sqrt(2/pi) =
	// 9.6 m for sigma=12. Require a clear improvement plus an absolute bound.
	if matchedAvg > rawAvg*0.6 {
		t.Fatalf("matched avg error %.1f m not reduced enough (raw %.1f m)", matchedAvg, rawAvg)
	}
	if matchedAvg > 15 {
		t.Fatalf("matched avg error %.1f m above noise floor bound", matchedAvg)
	}
	if float64(within20)/float64(len(obs)) < 0.9 {
		t.Fatalf("only %d/%d matched points within 20 m of truth", within20, len(obs))
	}
}

// TestMatchParallelRoads is the classic city-canyon failure mode: two
// parallel one-way streets 60 m apart, connected only at their far ends.
// One GPS point drifts 55 m sideways, landing next to the wrong street.
// Global Viterbi decoding must keep the path on the correct street.
func TestMatchParallelRoads(t *testing.T) {
	nodes := []Point{
		xyToPoint(0, 0), xyToPoint(1000, 0), // road A (correct)
		xyToPoint(0, 60), xyToPoint(1000, 60), // road B (parallel)
	}
	g := NewGraph(nodes)
	edgeA := g.AddEdge(0, 1)
	g.AddEdge(2, 3) // road B
	g.AddEdge(0, 2) // connector at west end
	g.AddEdge(3, 1) // connector at east end (B -> A only)

	m := NewMatcher(g)

	var obs []GPSPoint
	for i := 0; i <= 20; i++ {
		x := 100.0 + float64(i)*40
		y := 0.0
		if i == 10 {
			y = 55 // severe multipath drift toward road B
		}
		p := xyToPoint(x, y)
		obs = append(obs, GPSPoint{Lat: p.Lat, Lon: p.Lon, T: float64(i) * 4})
	}

	matches := m.Match(obs)
	for i, mt := range matches {
		if mt.EdgeID != edgeA {
			t.Fatalf("obs %d matched to edge %d, want road A (edge %d)", i, mt.EdgeID, edgeA)
		}
		if d := Haversine(mt.Point, xyToPoint(100+float64(i)*40, 0)); d > 1 {
			t.Fatalf("obs %d snapped %.1f m off road A centerline", i, d)
		}
	}
}

// TestMatchUnmatchedObservation ensures observations with no nearby edge are
// reported as unmatched instead of crashing the decoder.
func TestMatchUnmatchedObservation(t *testing.T) {
	g := buildGrid()
	m := NewMatcher(g)
	far := xyToPoint(5000, 5000)
	near := xyToPoint(50, 100)
	obs := []GPSPoint{
		{Lat: near.Lat, Lon: near.Lon, T: 0},
		{Lat: far.Lat, Lon: far.Lon, T: 2},
		{Lat: near.Lat, Lon: near.Lon, T: 4},
	}
	matches := m.Match(obs)
	if matches[1].EdgeID != -1 {
		t.Fatalf("far-away observation should be unmatched, got edge %d", matches[1].EdgeID)
	}
	if matches[0].EdgeID < 0 || matches[2].EdgeID < 0 {
		t.Fatalf("near observations should be matched")
	}
}
