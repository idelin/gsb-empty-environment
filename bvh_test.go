package stbvh

import (
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"
)

var cst = time.FixedZone("CST", 8*3600)

func ts(hour, min, sec int) float64 {
	return float64(time.Date(2026, 10, 2, hour, min, sec, 0, cst).Unix())
}

func pt(x, y, z, t float64) Point4 { return Point4{x, y, z, t} }

func TestBox4Intersect(t *testing.T) {
	a := NewBox(pt(0, 0, 0, 0), pt(10, 10, 10, 10))
	b := NewBox(pt(5, 5, 5, 5), pt(20, 20, 20, 20))
	if !a.Intersects(b) {
		t.Fatal("expected intersect")
	}
	// 仅时间维不相交
	c := NewBox(pt(5, 5, 5, 11), pt(20, 20, 20, 20))
	if a.Intersects(c) {
		t.Fatal("expected no intersect on time axis")
	}
	// 仅高度维不相交
	d := NewBox(pt(5, 5, 10.1, 5), pt(20, 20, 20, 20))
	if a.Intersects(d) {
		t.Fatal("expected no intersect on z axis")
	}
}

// 暴力对拍：随机轨迹 + 随机查询盒，BVH 结果必须与线性扫描一致。
func TestQueryMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	var trajs []Trajectory
	for id := 0; id < 50; id++ {
		n := 20 + rng.Intn(30)
		tr := Trajectory{ID: id}
		x, y, z := rng.Float64()*5000, rng.Float64()*5000, rng.Float64()*500
		tm := ts(12, 0, 0)
		for i := 0; i < n; i++ {
			tr.Points = append(tr.Points, pt(x, y, z, tm))
			x += (rng.Float64() - 0.5) * 400
			y += (rng.Float64() - 0.5) * 400
			z = math.Max(0, z+(rng.Float64()-0.5)*100)
			tm += 10 + rng.Float64()*50
		}
		trajs = append(trajs, tr)
	}
	bvh := BuildTrajectories(trajs...)

	var all []Segment
	for i := range trajs {
		all = append(all, trajs[i].Segments()...)
	}

	key := func(h Segment) [2]int { return [2]int{h.TrajID, h.Index} }
	for qi := 0; qi < 200; qi++ {
		cx, cy := rng.Float64()*5000, rng.Float64()*5000
		cz := rng.Float64() * 500
		ct := ts(12, 0, 0) + rng.Float64()*3600*4
		sx, sy := rng.Float64()*800, rng.Float64()*800
		sz, st := rng.Float64()*200, rng.Float64()*1800
		q := NewBox(pt(cx-sx, cy-sy, cz-sz, ct-st), pt(cx+sx, cy+sy, cz+sz, ct+st))

		var want []Segment
		for i := range all {
			if all[i].box.Intersects(q) {
				want = append(want, all[i])
			}
		}
		got := bvh.Query(q)
		if len(got) != len(want) {
			t.Fatalf("query %d: got %d hits, want %d", qi, len(got), len(want))
		}
		gotKeys := make([][2]int, len(got))
		for i, h := range got {
			gotKeys[i] = key(h.Segment)
		}
		wantKeys := make([][2]int, len(want))
		for i, s := range want {
			wantKeys[i] = key(s)
		}
		sort.Slice(gotKeys, func(i, j int) bool { return gotKeys[i][0]*1e6+gotKeys[i][1] < gotKeys[j][0]*1e6+gotKeys[j][1] })
		sort.Slice(wantKeys, func(i, j int) bool { return wantKeys[i][0]*1e6+wantKeys[i][1] < wantKeys[j][0]*1e6+wantKeys[j][1] })
		for i := range wantKeys {
			if gotKeys[i] != wantKeys[i] {
				t.Fatalf("query %d: mismatch at %d: got %v want %v", qi, i, gotKeys[i], wantKeys[i])
			}
		}
	}
}

// 验收场景：14:00–14:10、高度≥100m、穿越核心保护区的轨迹段。
func TestDroneScenario(t *testing.T) {
	t0 := ts(14, 0, 0)  // 14:00
	t1 := ts(14, 10, 0) // 14:10
	// 核心保护区: x,y ∈ [0,1000]，高度 ≥ 100
	query := Box4{
		Min: [NDim]float64{0, 0, 100, t0},
		Max: [NDim]float64{1000, 1000, math.Inf(1), t1},
	}

	trajs := []Trajectory{
		// A: 14:02–14:08 以 300m 高度横穿核心区 → 命中
		{ID: 1, Points: []Point4{
			pt(-500, 500, 300, ts(14, 2, 0)),
			pt(1500, 500, 300, ts(14, 8, 0)),
		}},
		// B: 同路径但高度 50m（低于 100）→ 不命中
		{ID: 2, Points: []Point4{
			pt(-500, 500, 50, ts(14, 2, 0)),
			pt(1500, 500, 50, ts(14, 8, 0)),
		}},
		// C: 高度 500m 但在核心区外（y=3000）→ 不命中
		{ID: 3, Points: []Point4{
			pt(-500, 3000, 500, ts(14, 2, 0)),
			pt(1500, 3000, 500, ts(14, 8, 0)),
		}},
		// D: 高度 200m 穿越核心区，但时间在 13:50–13:55 → 不命中
		{ID: 4, Points: []Point4{
			pt(-500, 500, 200, ts(13, 50, 0)),
			pt(1500, 500, 200, ts(13, 55, 0)),
		}},
		// E: 13:55 从区外起飞，14:05 到达核心区中心（爬升至 400m）→ 命中且被裁剪
		{ID: 5, Points: []Point4{
			pt(-2000, 500, 50, ts(13, 55, 0)),
			pt(500, 500, 400, ts(14, 5, 0)),
		}},
	}

	bvh := BuildTrajectories(trajs...)
	hits := bvh.Query(query)

	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d: %+v", len(hits), hits)
	}
	byID := map[int]Hit{}
	for _, h := range hits {
		byID[h.Segment.TrajID] = h
	}

	// A: 整段在窗口内，裁剪端点即原端点
	ha, ok := byID[1]
	if !ok {
		t.Fatal("missing hit for drone A")
	}
	if ha.Clip0[DimT] != ts(14, 2, 0) || ha.Clip1[DimT] != ts(14, 8, 0) {
		t.Fatalf("A clip wrong: %v -> %v", ha.Clip0, ha.Clip1)
	}

	// E: 段跨越 14:00，裁剪后起点必须恰为 14:00，且空间坐标为线性插值
	he, ok := byID[5]
	if !ok {
		t.Fatal("missing hit for drone E")
	}
	if he.Clip0[DimT] != t0 {
		t.Fatalf("E clip start = %v, want %v", he.Clip0[DimT], t0)
	}
	// 13:55→14:05 共 600s，14:00 为 u=0.5：x = -2000+0.5*2500 = -750
	if got := he.Clip0[DimX]; math.Abs(got-(-750)) > 1e-9 {
		t.Fatalf("E clip x = %v, want -750", got)
	}
	if he.Clip1[DimT] != ts(14, 5, 0) {
		t.Fatalf("E clip end wrong: %v", he.Clip1)
	}
}

// 性能：10 万级线段上查询应为毫秒级。
func TestQueryLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	rng := rand.New(rand.NewSource(7))
	var trajs []Trajectory
	segCount := 0
	for id := 0; id < 500; id++ {
		tr := Trajectory{ID: id}
		var x, y, z, tm float64
		if id < 50 {
			// 保证一批轨迹在 14:00–14:10 穿过查询区域
			x, y, z, tm = 5500+rng.Float64()*2000, 5500+rng.Float64()*2000, 200+rng.Float64()*300, ts(13, 55, 0)
		} else {
			x, y, z, tm = rng.Float64()*20000, rng.Float64()*20000, rng.Float64()*1000, ts(13, 0, 0)
		}
		for i := 0; i < 200; i++ {
			tr.Points = append(tr.Points, pt(x, y, z, tm))
			x += (rng.Float64() - 0.5) * 300
			y += (rng.Float64() - 0.5) * 300
			z = math.Max(0, z+(rng.Float64()-0.5)*50)
			tm += 5 + rng.Float64()*10
		}
		segCount += len(tr.Points) - 1
		trajs = append(trajs, tr)
	}
	bvh := BuildTrajectories(trajs...)
	t.Logf("indexed %d segments", bvh.Len())

	q := Box4{
		Min: [NDim]float64{5000, 5000, 100, ts(14, 0, 0)},
		Max: [NDim]float64{8000, 8000, math.Inf(1), ts(14, 10, 0)},
	}
	start := time.Now()
	const rounds = 100
	var hits []Hit
	for i := 0; i < rounds; i++ {
		hits = bvh.Query(q)
	}
	avg := time.Since(start) / rounds
	t.Logf("query: %d hits, avg %v over %d rounds", len(hits), avg, rounds)
	if len(hits) == 0 {
		t.Fatal("expected non-empty hits")
	}
	if avg > 10*time.Millisecond {
		t.Fatalf("query too slow: avg %v", avg)
	}
}

func BenchmarkQuery(b *testing.B) {
	rng := rand.New(rand.NewSource(7))
	var trajs []Trajectory
	for id := 0; id < 500; id++ {
		tr := Trajectory{ID: id}
		var x, y, z, tm float64
		if id < 50 {
			x, y, z, tm = 5500+rng.Float64()*2000, 5500+rng.Float64()*2000, 200+rng.Float64()*300, ts(13, 55, 0)
		} else {
			x, y, z, tm = rng.Float64()*20000, rng.Float64()*20000, rng.Float64()*1000, ts(13, 0, 0)
		}
		for i := 0; i < 200; i++ {
			tr.Points = append(tr.Points, pt(x, y, z, tm))
			x += (rng.Float64() - 0.5) * 300
			y += (rng.Float64() - 0.5) * 300
			z = math.Max(0, z+(rng.Float64()-0.5)*50)
			tm += 5 + rng.Float64()*10
		}
		trajs = append(trajs, tr)
	}
	bvh := BuildTrajectories(trajs...)
	q := Box4{
		Min: [NDim]float64{5000, 5000, 100, ts(14, 0, 0)},
		Max: [NDim]float64{8000, 8000, math.Inf(1), ts(14, 10, 0)},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSink = bvh.Query(q)
	}
}

var benchSink []Hit
