package floodsim

import (
	"math"
	"testing"
)

// 构造 V 形谷地 DEM：中间列低、两侧高，水应沿谷地向低处汇聚。
func valleyGrid(w, h int) *Grid {
	elev := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := math.Abs(float64(x) - float64(w-1)/2)
			elev[y*w+x] = d*2 + float64(h-1-y)*0.5 // 横向谷坡 + 北高南低
		}
	}
	return NewGrid(w, h, elev, nil)
}

func TestRainAccumulatesAndInfiltrates(t *testing.T) {
	// 全域均匀渗透：无水平水头差，可独立验证降雨+渗透
	g := NewGrid(4, 4, make([]float64, 16), nil)
	for i := range g.Infil {
		g.Infil[i] = 0.5
	}
	s := NewSimulator(g, []float64{1.0, 1.0})
	s.Run(2)
	d := s.DepthAt(2)
	// 每单元积水 = 2.0 降雨 - 2*0.5 渗透 = 1.0
	if math.Abs(d[5]-1.0) > 1e-9 {
		t.Fatalf("cell 5 depth = %v, want 1.0", d[5])
	}
	// 渗透不产生负水深：停雨后 3 tick 应归零而非变负
	s.Run(3)
	if d := s.DepthAt(s.Tick()); d[5] != 0 {
		t.Fatalf("cell 5 depth = %v, want 0 (no negative)", d[5])
	}
}

func TestWaterFlowsDownhillAndConserves(t *testing.T) {
	g := valleyGrid(9, 9)
	s := NewSimulator(g, []float64{0.5, 0.5, 0.5})
	s.Run(8) // 降雨 3 tick 后继续漫延 5 tick
	// 守恒：无渗透时 tick t 总水量 = 累计降雨 * 面积
	for tk := 0; tk <= s.Tick(); tk++ {
		rained := tk
		if rained > 3 {
			rained = 3
		}
		want := 0.5 * float64(rained) * 81
		if got := s.TotalWater(tk); math.Abs(got-want) > 1e-6 {
			t.Fatalf("tick %d total water = %v, want %v", tk, got, want)
		}
	}
	// 谷底线（中央列）水深应显著高于两侧坡面
	d := s.DepthAt(s.Tick())
	center := d[8*9+4]
	edge := d[8*9+0]
	if center <= edge {
		t.Fatalf("valley bottom depth %v should exceed slope depth %v", center, edge)
	}
}

func TestRegionsAndBoundaryPolygon(t *testing.T) {
	// 手工水深场：2x2 方块 + 远处孤点 => 两个连通区域
	g := NewGrid(5, 5, make([]float64, 25), nil)
	depth := make([]float64, 25)
	for _, c := range []int{6, 7, 11, 12} { // (1,1)(2,1)(1,2)(2,2)
		depth[c] = 1.0
	}
	depth[24] = 1.0 // (4,4)
	regions := ExtractRegions(g, depth, 0.05)
	if len(regions) != 2 {
		t.Fatalf("got %d regions, want 2", len(regions))
	}
	var big *Region
	for i := range regions {
		if len(regions[i].Cells) == 4 {
			big = &regions[i]
		}
	}
	if big == nil {
		t.Fatal("missing 4-cell region")
	}
	// 2x2 方块边界应为 8 顶点、面积 4 的逆时针环
	if len(big.Boundary) != 8 {
		t.Fatalf("boundary vertices = %d, want 8", len(big.Boundary))
	}
	if a := ringArea(big.Boundary); math.Abs(a-4.0) > 1e-9 {
		t.Fatalf("boundary area = %v, want 4.0", a)
	}
}

func TestBoundaryWithHole(t *testing.T) {
	// 环形区域：3x3 淹没、中心干 => 外边界 + 1 个孔洞
	g := NewGrid(5, 5, make([]float64, 25), nil)
	depth := make([]float64, 25)
	for y := 1; y <= 3; y++ {
		for x := 1; x <= 3; x++ {
			depth[y*5+x] = 1.0
		}
	}
	depth[2*5+2] = 0
	regions := ExtractRegions(g, depth, 0.05)
	if len(regions) != 1 {
		t.Fatalf("got %d regions, want 1", len(regions))
	}
	r := regions[0]
	if len(r.Holes) != 1 {
		t.Fatalf("got %d holes, want 1", len(r.Holes))
	}
	if a := ringArea(r.Boundary); math.Abs(a-9.0) > 1e-9 {
		t.Fatalf("outer area = %v, want 9.0", a)
	}
	if a := ringArea(r.Holes[0]); math.Abs(a+1.0) > 1e-9 { // 孔洞为顺时针
		t.Fatalf("hole area = %v, want -1.0", a)
	}
}

func TestHistorySnapshots(t *testing.T) {
	g := valleyGrid(7, 7)
	s := NewSimulator(g, []float64{0.3, 0.3})
	s.Run(4)
	if s.Tick() != 4 {
		t.Fatalf("tick = %d, want 4", s.Tick())
	}
	// 任意历史时刻可查询：淹没面积应随降雨单调不减（无渗透）
	prev := -1
	for tk := 0; tk <= 2; tk++ {
		n := 0
		for _, d := range s.DepthAt(tk) {
			if d > s.FloodThreshold {
				n++
			}
		}
		if n < prev {
			t.Fatalf("flooded cells shrank during rain: tick %d has %d < %d", tk, n, prev)
		}
		prev = n
	}
	if s.SnapshotAt(99) != nil {
		t.Fatal("out-of-range snapshot should be nil")
	}
}

func TestRoadBreakpoints(t *testing.T) {
	g := NewGrid(10, 3, make([]float64, 30), nil)
	depth := make([]float64, 30)
	for x := 4; x <= 6; x++ { // 中间一竖条淹没
		depth[1*10+x] = 1.0
	}
	s := NewSimulator(g, nil)
	s.history[0].Depth = depth
	net := &RoadNetwork{Roads: []Road{
		{Name: "cross", Points: []Point{{0.5, 1.5}, {9.5, 1.5}}}, // 横穿淹没带
		{Name: "safe", Points: []Point{{0.5, 0.2}, {9.5, 0.2}}},  // 上方安全
	}}
	bps := s.BreakpointsAt(0, net)
	if len(bps) != 1 {
		t.Fatalf("got %d breakpoints, want 1", len(bps))
	}
	bp := bps[0]
	if bp.Road != "cross" {
		t.Fatalf("breakpoint road = %q, want cross", bp.Road)
	}
	if bp.Enter.X < 4.0 || bp.Enter.X > 4.5 {
		t.Fatalf("enter x = %v, want in [4.0, 4.5]", bp.Enter.X)
	}
	if bp.Exit.X < 6.5 || bp.Exit.X > 7.0 {
		t.Fatalf("exit x = %v, want in [6.5, 7.0]", bp.Exit.X)
	}
}
