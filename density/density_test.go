package density

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func shanghaiGrid() *Grid {
	// ~250m tiles over Shanghai.
	return NewGrid(31.0, 121.3, 250)
}

func TestGridLocateCenterRoundTrip(t *testing.T) {
	g := shanghaiGrid()
	for _, p := range [][2]float64{{31.23, 121.47}, {31.05, 121.32}, {31.40, 121.65}} {
		x, y := g.Locate(p[0], p[1])
		lat, lng := g.Center(x, y)
		// Center must lie within half a tile of the original point.
		if math.Abs(lat-p[0])*metersPerDegLat > g.TileSize/2 {
			t.Fatalf("lat roundtrip off: got %v want %v", lat, p[0])
		}
		if math.Abs(lng-p[1])*metersPerDegLng*g.cosLat > g.TileSize/2 {
			t.Fatalf("lng roundtrip off: got %v want %v", lng, p[1])
		}
	}
}

func TestGridDistinctTiles(t *testing.T) {
	g := shanghaiGrid()
	// Two points 1km apart in latitude must land in different tiles.
	_, y1 := g.Locate(31.20, 121.47)
	_, y2 := g.Locate(31.21, 121.47)
	if y1 == y2 {
		t.Fatalf("expected different tiles, both got y=%d", y1)
	}
}

func TestCubeAddAndWeight(t *testing.T) {
	c := NewCube(shanghaiGrid())
	ts := time.Date(2026, 9, 25, 18, 30, 0, 0, time.Local) // a Friday evening
	if !c.Add(31.23, 121.47, ts, 1.5) {
		t.Fatal("Add rejected in-range point")
	}
	c.Add(31.23, 121.47, ts, 2.5)
	x, y := c.grid.Locate(31.23, 121.47)
	if got := c.Weight(ts, x, y); got != 4.0 {
		t.Fatalf("Weight = %v, want 4.0", got)
	}
	// Same tile, next hour: separate cell.
	if got := c.Weight(ts.Add(time.Hour), x, y); got != 0 {
		t.Fatalf("cross-hour leak: %v", got)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}
}

func TestCubeRejectsOutOfRange(t *testing.T) {
	// 1m tiles: the representable range is ±524288 tiles ≈ ±524km, so a
	// point thousands of km away must be dropped.
	c := NewCube(NewGrid(31.0, 121.3, 1))
	if c.Add(-33.8, 151.2, time.Now(), 1) { // Sydney
		t.Fatal("expected out-of-range point to be dropped")
	}
	if c.Len() != 0 {
		t.Fatalf("Len = %d, want 0", c.Len())
	}
}

func TestTopKWindow(t *testing.T) {
	c := NewCube(shanghaiGrid())
	fri := time.Date(2026, 9, 25, 18, 0, 0, 0, time.Local) // Friday 18:00
	sat := fri.AddDate(0, 0, 1)

	// Hot tile A: 100 in Friday rush hour, spread over 2 hourly buckets.
	ax, ay := c.grid.Locate(31.2304, 121.4737) // People's Square
	key, _ := pack(hourOf(fri), ax, ay)
	c.cells[key] = 60
	key, _ = pack(hourOf(fri.Add(time.Hour)), ax, ay)
	c.cells[key] = 40

	// Tile B: 50 on Friday, 80 on Saturday (outside the window).
	bx, by := c.grid.Locate(31.2397, 121.4998) // Lujiazui
	key, _ = pack(hourOf(fri), bx, by)
	c.cells[key] = 50
	key, _ = pack(hourOf(sat), bx, by)
	c.cells[key] = 80

	// Tile C: 30 on Friday.
	cx, cy := c.grid.Locate(31.1943, 121.4351) // Xujiahui
	key, _ = pack(hourOf(fri), cx, cy)
	c.cells[key] = 30

	to := fri.Add(2 * time.Hour)
	top := c.TopK(fri, to, 2)
	if len(top) != 2 {
		t.Fatalf("got %d hotspots, want 2", len(top))
	}
	if top[0].CellX != ax || top[0].CellY != ay || top[0].Weight != 100 {
		t.Fatalf("rank1 = %+v, want tile A with 100", top[0])
	}
	if top[1].CellX != bx || top[1].CellY != by || top[1].Weight != 50 {
		t.Fatalf("rank2 = %+v, want tile B with 50 (Saturday excluded)", top[1])
	}
	// Center coordinates must be populated.
	if top[0].Lat == 0 || top[0].Lng == 0 {
		t.Fatal("missing center coordinates")
	}

	// k larger than the number of tiles in the window returns all of them.
	if got := len(c.TopK(fri, to, 10)); got != 3 {
		t.Fatalf("TopK(10) returned %d, want 3", got)
	}
	// Empty window.
	if got := c.TopK(to, to, 5); got != nil {
		t.Fatalf("empty window returned %v", got)
	}
}

func TestCubePrune(t *testing.T) {
	c := NewCube(shanghaiGrid())
	base := time.Date(2026, 9, 25, 0, 0, 0, 0, time.Local)
	for h := 0; h < 48; h++ {
		c.Add(31.23, 121.47, base.Add(time.Duration(h)*time.Hour), 1)
	}
	if c.Len() != 48 {
		t.Fatalf("Len = %d, want 48", c.Len())
	}
	removed := c.Prune(base.Add(24 * time.Hour))
	if removed != 24 || c.Len() != 24 {
		t.Fatalf("removed %d, Len %d; want 24/24", removed, c.Len())
	}
}

// TestStreamingMillionPoints ingests 1,000,000 trajectory points, verifies
// memory stays proportional to distinct cells, and times a Top-10 query.
func TestStreamingMillionPoints(t *testing.T) {
	g := shanghaiGrid()
	c := NewCube(g)
	rng := rand.New(rand.NewSource(42))

	// Friday 2026-09-25 evening peak, 17:00–20:00 local.
	base := time.Date(2026, 9, 25, 17, 0, 0, 0, time.Local)
	hotspots := [][2]float64{
		{31.2304, 121.4737}, // People's Square
		{31.2397, 121.4998}, // Lujiazui
		{31.1943, 121.4351}, // Xujiahui
	}

	const n = 1000000
	start := time.Now()
	for i := 0; i < n; i++ {
		var lat, lng float64
		if rng.Float64() < 0.6 {
			// 60% of points cluster around the hotspots.
			hs := hotspots[rng.Intn(len(hotspots))]
			lat = hs[0] + rng.NormFloat64()*0.004
			lng = hs[1] + rng.NormFloat64()*0.004
		} else {
			lat = 31.05 + rng.Float64()*0.35
			lng = 121.32 + rng.Float64()*0.35
		}
		ts := base.Add(time.Duration(rng.Intn(3*3600)) * time.Second)
		c.Add(lat, lng, ts, 1)
	}
	ingest := time.Since(start)

	// Memory bound: cells <= distinct tiles x 3 hourly buckets, and far below n.
	if c.Len() >= n {
		t.Fatalf("cells %d not bounded below input %d", c.Len(), n)
	}
	t.Logf("ingested %d points into %d cells in %v (%.0f pts/s)",
		n, c.Len(), ingest, float64(n)/ingest.Seconds())

	start = time.Now()
	top := c.TopK(base, base.Add(3*time.Hour), 10)
	query := time.Since(start)
	t.Logf("Top-10 query over %d cells took %v", c.Len(), query)
	if query > time.Second {
		t.Fatalf("query too slow: %v", query)
	}
	if len(top) != 10 {
		t.Fatalf("got %d hotspots, want 10", len(top))
	}
	for i := 1; i < len(top); i++ {
		if top[i].Weight > top[i-1].Weight {
			t.Fatalf("results not sorted descending at %d", i)
		}
	}
	// The heaviest tile must be near one of the planted hotspots.
	near := false
	for _, hs := range hotspots {
		if math.Abs(top[0].Lat-hs[0]) < 0.01 && math.Abs(top[0].Lng-hs[1]) < 0.01 {
			near = true
		}
	}
	if !near {
		t.Fatalf("top hotspot (%.4f, %.4f) not near any planted hotspot",
			top[0].Lat, top[0].Lng)
	}
	t.Logf("top hotspot: (%.5f, %.5f) weight=%.0f", top[0].Lat, top[0].Lng, top[0].Weight)
}
