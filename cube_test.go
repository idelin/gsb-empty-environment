package taxiheat

import (
	"math/rand"
	"testing"
	"time"
)

func testGrid(t *testing.T) Grid {
	g, err := NewGrid(39.0, 116.0, 0.01, 100, 100) // 1°x1°, 约1km网格
	if err != nil {
		t.Fatalf("NewGrid: %v", err)
	}
	return g
}

func TestGridCellAndCenter(t *testing.T) {
	g := testGrid(t)

	// 左下角边界点落在 0 号格
	idx, ok := g.Cell(39.0, 116.0)
	if !ok || idx != 0 {
		t.Fatalf("corner: idx=%d ok=%v", idx, ok)
	}
	// 中心点
	idx, ok = g.Cell(39.505, 116.505)
	if !ok {
		t.Fatal("center should be in grid")
	}
	lat, lng := g.Center(idx)
	if lat < 39.50 || lat >= 39.51 || lng < 116.50 || lng >= 116.51 {
		t.Fatalf("center mismatch: %v,%v", lat, lng)
	}
	// 越界点
	for _, p := range [][2]float64{{38.9, 116.5}, {40.1, 116.5}, {39.5, 115.9}, {39.5, 117.1}} {
		if _, ok := g.Cell(p[0], p[1]); ok {
			t.Fatalf("point %v should be out of grid", p)
		}
	}
}

func TestNewGridInvalid(t *testing.T) {
	if _, err := NewGrid(0, 0, 0, 10, 10); err == nil {
		t.Fatal("expect error for zero cellDeg")
	}
	if _, err := NewGrid(0, 0, 0.1, 0, 10); err == nil {
		t.Fatal("expect error for zero nx")
	}
}

func TestAccumulateAndWindow(t *testing.T) {
	c := NewCube(testGrid(t))
	base := time.Date(2026, 9, 25, 17, 0, 0, 0, time.Local) // 周五 17:00

	// 同一网格跨 3 个小时各加一点
	for h := 0; h < 3; h++ {
		if !c.Add(39.505, 116.505, base.Add(time.Duration(h)*time.Hour), 1.5) {
			t.Fatal("add failed")
		}
	}
	// 窗口外（昨天）的点不应被统计
	c.Add(39.505, 116.505, base.Add(-24*time.Hour), 100)

	hs := c.TopK(base, base.Add(3*time.Hour), 1)
	if len(hs) != 1 {
		t.Fatalf("expect 1 hotspot, got %d", len(hs))
	}
	if hs[0].Weight != 4.5 {
		t.Fatalf("expect weight 4.5, got %v", hs[0].Weight)
	}

	// 只查中间 1 小时
	hs = c.TopK(base.Add(time.Hour), base.Add(2*time.Hour), 1)
	if hs[0].Weight != 1.5 {
		t.Fatalf("expect weight 1.5, got %v", hs[0].Weight)
	}

	added, dropped, hours, _ := c.Stats()
	if added != 4 || dropped != 0 || hours != 4 {
		t.Fatalf("stats: added=%d dropped=%d hours=%d", added, dropped, hours)
	}
}

func TestOutOfBoundsDropped(t *testing.T) {
	c := NewCube(testGrid(t))
	now := time.Now()
	if c.Add(0, 0, now, 1) {
		t.Fatal("out-of-bounds point should be dropped")
	}
	_, dropped, _, _ := c.Stats()
	if dropped != 1 {
		t.Fatalf("expect 1 drop, got %d", dropped)
	}
}

func TestTopKOrdering(t *testing.T) {
	c := NewCube(testGrid(t))
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.Local)
	// 第 i 个网格放 i+1 的权重
	for i := 0; i < 20; i++ {
		lat, lng := c.Grid.Center(i * 7) // 分散的格子
		c.Add(lat, lng, now, float32(i+1))
	}
	hs := c.TopK(now, now.Add(time.Hour), 5)
	if len(hs) != 5 {
		t.Fatalf("expect 5, got %d", len(hs))
	}
	for i := 0; i+1 < len(hs); i++ {
		if hs[i].Weight < hs[i+1].Weight {
			t.Fatalf("not descending: %v then %v", hs[i].Weight, hs[i+1].Weight)
		}
	}
	if hs[0].Weight != 20 {
		t.Fatalf("top weight expect 20, got %v", hs[0].Weight)
	}
}

// 验收：百万级轨迹点流式导入，内存严格有界（与点数无关），查询秒级。
func TestMillionPointsMemoryBound(t *testing.T) {
	g, err := NewGrid(39.4, 115.9, 0.005, 120, 100) // 北京市区约 0.6°x0.5°
	if err != nil {
		t.Fatal(err)
	}
	c := NewCube(g)
	rng := rand.New(rand.NewSource(42))
	base := time.Date(2026, 9, 25, 0, 0, 0, 0, time.Local)

	const n = 1000000
	for i := 0; i < n; i++ {
		lat := 39.4 + rng.Float64()*0.5
		lng := 115.9 + rng.Float64()*0.6
		// 一周内随机小时
		ts := base.Add(time.Duration(rng.Intn(7*24)) * time.Hour)
		c.Add(lat, lng, ts, 1)
	}

	added, dropped, hours, mem := c.Stats()
	if added != n || dropped != 0 {
		t.Fatalf("added=%d dropped=%d", added, dropped)
	}
	// 内存上界：168 小时层 × 12000 格 × 4B ≈ 8.06 MB，与百万点无关
	wantMax := int64(7*24) * int64(g.Cells()) * 4
	if mem > wantMax {
		t.Fatalf("memory %d exceeds bound %d", mem, wantMax)
	}
	if hours != 7*24 {
		t.Fatalf("expect 168 hour layers, got %d", hours)
	}
	t.Logf("1M points: %d hour layers, cube memory %.2f MB", hours, float64(mem)/1e6)

	// 查询任意窗口 Top-10
	start := base.Add(24 * time.Hour)
	hs := c.TopK(start, start.Add(3*time.Hour), 10)
	if len(hs) != 10 {
		t.Fatalf("expect 10 hotspots, got %d", len(hs))
	}
	for i := 0; i+1 < len(hs); i++ {
		if hs[i].Weight < hs[i+1].Weight {
			t.Fatal("top-k not sorted descending")
		}
	}
}

func BenchmarkAdd(b *testing.B) {
	g, _ := NewGrid(39.4, 115.9, 0.005, 120, 100)
	c := NewCube(g)
	rng := rand.New(rand.NewSource(1))
	base := time.Date(2026, 9, 25, 0, 0, 0, 0, time.Local)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Add(39.4+rng.Float64()*0.5, 115.9+rng.Float64()*0.6,
			base.Add(time.Duration(rng.Intn(168))*time.Hour), 1)
	}
}

func BenchmarkTopK(b *testing.B) {
	g, _ := NewGrid(39.4, 115.9, 0.005, 120, 100)
	c := NewCube(g)
	rng := rand.New(rand.NewSource(1))
	base := time.Date(2026, 9, 25, 0, 0, 0, 0, time.Local)
	for i := 0; i < 1000000; i++ {
		c.Add(39.4+rng.Float64()*0.5, 115.9+rng.Float64()*0.6,
			base.Add(time.Duration(rng.Intn(168))*time.Hour), 1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.TopK(base.Add(24*time.Hour), base.Add(27*time.Hour), 10)
	}
}
