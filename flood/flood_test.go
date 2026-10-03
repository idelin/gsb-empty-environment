package flood

import (
	"math"
	"testing"
)

func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestGridBasics(t *testing.T) {
	g := NewGrid(3, 2)
	g.Set(1, 1, 4.5)
	if !approxEq(g.At(1, 1), 4.5) {
		t.Fatalf("At(1,1) = %v, want 4.5", g.At(1, 1))
	}
	if g.InBounds(3, 0) || g.InBounds(0, -1) || !g.InBounds(2, 1) {
		t.Fatal("InBounds wrong")
	}
	c := g.Clone()
	c.Set(1, 1, 9)
	if approxEq(g.At(1, 1), 9) {
		t.Fatal("Clone shares storage")
	}
}

func TestRainAndInfiltration(t *testing.T) {
	dem := NewGrid(4, 3) // flat
	s := NewSimulator(dem, []float64{2.0, 1.0}, Config{
		Infiltration: 0.5,
		FlowPasses:   1,
	})
	s.Step()
	// 2.0 rain - 0.5 infiltration = 1.5 everywhere (flat: no net flow).
	d := s.Depth()
	for i, v := range d.Cells {
		if !approxEq(v, 1.5) {
			t.Fatalf("cell %d depth = %v, want 1.5", i, v)
		}
	}
	s.Step()
	if got := s.TotalWater(2); !approxEq(got, 12*2.0) {
		t.Fatalf("total water = %v, want 24", got)
	}
}

func TestWaterConservation(t *testing.T) {
	dem := NewGrid(6, 4)
	for x := 0; x < 6; x++ {
		for y := 0; y < 4; y++ {
			dem.Set(x, y, float64(6-x)) // slope west -> east
		}
	}
	s := NewSimulator(dem, nil, Config{FlowRate: 0.5, FlowPasses: 10})
	s.depth.Set(0, 0, 5)
	s.depth.Set(0, 3, 2)
	before := s.depth.Sum()
	s.Step() // no rain, no infiltration
	if got := s.depth.Sum(); !approxEq(got, before) {
		t.Fatalf("water not conserved: before %v after %v", before, got)
	}
}

func TestFlowDownhill(t *testing.T) {
	dem := NewGrid(5, 1)
	for x := 0; x < 5; x++ {
		dem.Set(x, 0, float64(5-x))
	}
	s := NewSimulator(dem, nil, Config{FlowRate: 0.5, FlowPasses: 40})
	s.depth.Set(0, 0, 4)
	s.Step()
	if got := s.depth.At(4, 0); got <= 0 {
		t.Fatalf("water did not reach downhill cell, depth = %v", got)
	}
	// Water must accumulate toward the lowest cell.
	if !(s.depth.At(4, 0) > s.depth.At(2, 0)) {
		t.Fatalf("expected downhill accumulation, depths: %v", s.depth.Cells)
	}
}

func TestPerCellInfiltrationMap(t *testing.T) {
	dem := NewGrid(2, 1)
	inf := NewGrid(2, 1)
	inf.Set(0, 0, 1.0) // permeable cell
	inf.Set(1, 0, 0.0) // impermeable cell
	s := NewSimulator(dem, []float64{3.0}, Config{FlowPasses: 1})
	s.InfiltrationMap = inf
	s.Step()
	// Cell 0: 3-1=2, cell 1: 3-0=3. One pass with FlowRate 0.5 moves
	// 50% of cell 1's water (its only lower neighbour gets it all).
	if got := s.depth.At(0, 0); !approxEq(got, 3.5) {
		t.Fatalf("cell0 depth = %v, want 3.5", got)
	}
	if got := s.depth.At(1, 0); !approxEq(got, 1.5) {
		t.Fatalf("cell1 depth = %v, want 1.5", got)
	}
}

func TestRegionConnectivity(t *testing.T) {
	depth := NewGrid(7, 3)
	// Two separate ponds.
	depth.Set(0, 0, 1)
	depth.Set(1, 0, 1)
	depth.Set(5, 2, 1)
	depth.Set(6, 2, 1)
	regions := ExtractRegions(depth, 0.5)
	if len(regions) != 2 {
		t.Fatalf("got %d regions, want 2", len(regions))
	}
	// Bridge them (8-connectivity via diagonal chain).
	depth.Set(2, 0, 1)
	depth.Set(3, 1, 1)
	depth.Set(4, 2, 1)
	regions = ExtractRegions(depth, 0.5)
	if len(regions) != 1 {
		t.Fatalf("got %d regions after merge, want 1", len(regions))
	}
	if regions[0].Area() != 7 {
		t.Fatalf("merged area = %d, want 7", regions[0].Area())
	}
}

func TestBoundaryPolygonSquare(t *testing.T) {
	depth := NewGrid(4, 4)
	for _, c := range []Cell{{1, 1}, {2, 1}, {1, 2}, {2, 2}} {
		depth.Set(c.X, c.Y, 1)
	}
	regions := ExtractRegions(depth, 0.5)
	if len(regions) != 1 {
		t.Fatalf("got %d regions, want 1", len(regions))
	}
	b := regions[0].Boundary
	if len(b) != 5 {
		t.Fatalf("boundary has %d vertices, want 5 (closed): %v", len(b), b)
	}
	if b[0] != b[len(b)-1] {
		t.Fatalf("boundary not closed: %v", b)
	}
	want := []Point{{1, 1}, {2, 1}, {2, 2}, {1, 2}, {1, 1}}
	for i, p := range want {
		if b[i] != p {
			t.Fatalf("vertex %d = %v, want %v (full: %v)", i, b[i], p, b)
		}
	}
}

func TestBoundaryPolygonSingleCell(t *testing.T) {
	depth := NewGrid(3, 3)
	depth.Set(1, 1, 1)
	regions := ExtractRegions(depth, 0.5)
	if len(regions) != 1 || len(regions[0].Boundary) != 1 {
		t.Fatalf("single-cell region boundary = %v", regions)
	}
	if regions[0].Boundary[0] != (Point{1, 1}) {
		t.Fatalf("boundary = %v", regions[0].Boundary)
	}
}

func TestBoundaryPolygonThinLineTerminates(t *testing.T) {
	depth := NewGrid(5, 1)
	for x := 0; x < 5; x++ {
		depth.Set(x, 0, 1)
	}
	regions := ExtractRegions(depth, 0.5)
	if len(regions) != 1 {
		t.Fatalf("got %d regions, want 1", len(regions))
	}
	if len(regions[0].Boundary) < 2 {
		t.Fatalf("thin-line boundary too short: %v", regions[0].Boundary)
	}
}

func TestRoadBlockage(t *testing.T) {
	depth := NewGrid(10, 3)
	for x := 4; x <= 6; x++ {
		depth.Set(x, 1, 1) // flooded band across the road
	}
	roads := []Road{
		{ID: "R1", Points: []Point{{0, 1}, {9, 1}}}, // crosses the flood
		{ID: "R2", Points: []Point{{0, 0}, {9, 0}}}, // stays dry
	}
	bps := FindBlockages(depth, 0.5, 3, roads)
	if len(bps) != 1 {
		t.Fatalf("got %d breakpoints, want 1: %+v", len(bps), bps)
	}
	bp := bps[0]
	if bp.RoadID != "R1" || bp.Tick != 3 {
		t.Fatalf("wrong breakpoint metadata: %+v", bp)
	}
	if !approxEq(bp.Enter.X, 4.0) {
		t.Fatalf("enter X = %v, want 4.0", bp.Enter.X)
	}
	if bp.Exit.X < 6.5 || bp.Exit.X >= 7.0 {
		t.Fatalf("exit X = %v, want in [6.5, 7.0)", bp.Exit.X)
	}
	if bp.Cells != 3 {
		t.Fatalf("blocked cells = %d, want 3", bp.Cells)
	}
}

func TestRoadBlockageTwoSegments(t *testing.T) {
	depth := NewGrid(10, 1)
	depth.Set(2, 0, 1)
	depth.Set(7, 0, 1)
	roads := []Road{{ID: "R", Points: []Point{{0, 0}, {9, 0}}}}
	bps := FindBlockages(depth, 0.5, 0, roads)
	if len(bps) != 2 {
		t.Fatalf("got %d breakpoints, want 2: %+v", len(bps), bps)
	}
}

func TestHistoricalSnapshots(t *testing.T) {
	dem := NewGrid(4, 4)
	s := NewSimulator(dem, []float64{1, 1, 1}, Config{FlowPasses: 1})
	s.Run(3)
	if s.Tick() != 3 {
		t.Fatalf("tick = %d, want 3", s.Tick())
	}
	if got := s.TotalWater(0); got != 0 {
		t.Fatalf("initial water = %v, want 0", got)
	}
	if got := s.TotalWater(1); !approxEq(got, 16) {
		t.Fatalf("water after tick 1 = %v, want 16", got)
	}
	if got := s.TotalWater(3); !approxEq(got, 48) {
		t.Fatalf("water after tick 3 = %v, want 48", got)
	}
	// Snapshots are immutable copies.
	d := s.DepthAt(1)
	d.Set(0, 0, 999)
	if !approxEq(s.TotalWater(1), 16) {
		t.Fatal("snapshot mutated through returned copy")
	}
	// Out-of-range queries clamp.
	if !approxEq(s.TotalWater(99), 48) {
		t.Fatal("clamping failed for future tick")
	}
}

func TestEndToEndFloodBlocksRoad(t *testing.T) {
	// DEM with a central depression; a road crosses it.
	dem := NewGrid(9, 5)
	for x := 0; x < 9; x++ {
		for y := 0; y < 5; y++ {
			dem.Set(x, y, 2.0)
		}
	}
	for x := 3; x <= 5; x++ {
		for y := 1; y <= 3; y++ {
			dem.Set(x, y, 0.0) // depression
		}
	}
	rain := []float64{0.05, 0.05, 0.05, 0.05, 0.05, 0.05}
	s := NewSimulator(dem, rain, Config{
		FlowRate:   0.5,
		FlowPasses: 20,
		BlockDepth: 0.3,
	})
	road := Road{ID: "MainSt", Points: []Point{{0, 2}, {8, 2}}}
	roads := []Road{road}

	s.Run(1)
	if bps := s.BlockagesAt(1, roads); len(bps) != 0 {
		t.Fatalf("road blocked too early at tick 1: %+v", bps)
	}
	s.Run(5)
	bps := s.BlockagesAt(6, roads)
	if len(bps) != 1 {
		t.Fatalf("got %d breakpoints at tick 6, want 1: %+v", len(bps), bps)
	}
	if bps[0].RoadID != "MainSt" {
		t.Fatalf("wrong road: %+v", bps[0])
	}
	// The depression must form exactly one connected region.
	if rs := s.RegionsAt(6); len(rs) != 1 {
		t.Fatalf("got %d regions at tick 6, want 1", len(rs))
	}
	// History: tick 1 must still be queryable and dry on the road.
	if bps := s.BlockagesAt(1, roads); len(bps) != 0 {
		t.Fatalf("historical tick 1 changed: %+v", bps)
	}
}
