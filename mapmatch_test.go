package mapmatch

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

const (
	gridN        = 8
	gridSpacing  = 100.0 // 米
	metersPerDeg = 111320.0
)

func nodeID(i, j int) int64 { return int64(i*gridN + j) }

// gridGraph 构建 gridN x gridN 的方格路网，相邻节点间距 gridSpacing 米，
// 每条街道添加正反两条有向路段。返回图与 (from,to)->edgeID 映射。
func gridGraph() (*Graph, map[[2]int64]int64) {
	g := NewGraph()
	for i := 0; i < gridN; i++ {
		for j := 0; j < gridN; j++ {
			g.AddNode(nodeID(i, j), float64(i)*gridSpacing/metersPerDeg, float64(j)*gridSpacing/metersPerDeg)
		}
	}
	edgeIDs := make(map[[2]int64]int64)
	var nextID int64 = 1
	addPair := func(a, b int64) {
		na, nb := g.nodes[a], g.nodes[b]
		g.AddEdge(nextID, a, b, []LatLon{na.LatLon, nb.LatLon})
		edgeIDs[[2]int64{a, b}] = nextID
		nextID++
		g.AddEdge(nextID, b, a, []LatLon{nb.LatLon, na.LatLon})
		edgeIDs[[2]int64{b, a}] = nextID
		nextID++
	}
	for i := 0; i < gridN; i++ {
		for j := 0; j < gridN; j++ {
			if j+1 < gridN {
				addPair(nodeID(i, j), nodeID(i, j+1))
			}
			if i+1 < gridN {
				addPair(nodeID(i, j), nodeID(i+1, j))
			}
		}
	}
	return g, edgeIDs
}

// randomRoute 从 start 出发随机游走 steps 条路段，避免立即折返，
// 返回途经的有向路段 ID 序列。
func randomRoute(g *Graph, edgeIDs map[[2]int64]int64, start int64, steps int, rnd *rand.Rand) []int64 {
	route := make([]int64, 0, steps)
	cur := start
	var prevFrom, prevTo int64 = -1, -1
	for s := 0; s < steps; s++ {
		var options []int64
		for _, e := range g.adj[cur] {
			if e.From == prevTo && e.To == prevFrom && len(g.adj[cur]) > 1 {
				continue // 避免立即沿来路折返，保证真值无歧义
			}
			options = append(options, e.ID)
		}
		id := options[rnd.Intn(len(options))]
		e := g.Edge(id)
		route = append(route, id)
		prevFrom, prevTo = e.From, e.To
		cur = e.To
	}
	return route
}

// sampleRoute 沿路线等距采样（每个路段 5 个点，避开端点），
// 加高斯噪声后返回观测点序列与真值路段序列。
func sampleRoute(g *Graph, route []int64, noiseSigma float64, rnd *rand.Rand, start time.Time) ([]Point, []int64) {
	fractions := []float64{0.1, 0.3, 0.5, 0.7, 0.9}
	var pts []Point
	var truth []int64
	for _, id := range route {
		e := g.Edge(id)
		a, b := e.Points[0], e.Points[len(e.Points)-1]
		for _, f := range fractions {
			lat := a.Lat + f*(b.Lat-a.Lat)
			lon := a.Lon + f*(b.Lon-a.Lon)
			// 在局部平面坐标系中加噪（米）
			nx := rnd.NormFloat64() * noiseSigma
			ny := rnd.NormFloat64() * noiseSigma
			lat += ny / metersPerDeg
			lon += nx / (metersPerDeg * math.Cos(lat*math.Pi/180))
			pts = append(pts, Point{
				LatLon: LatLon{Lat: lat, Lon: lon},
				Time:   start.Add(time.Duration(len(pts)) * time.Second),
			})
			truth = append(truth, id)
		}
	}
	return pts, truth
}

func matchAll(m *Matcher, pts []Point) []MatchResult {
	var out []MatchResult
	for _, p := range pts {
		out = append(out, m.Add(p)...)
	}
	return append(out, m.Flush()...)
}

// TestMatchAccuracy 在已知路线上加噪生成轨迹，统计匹配到正确路段的比例。
func TestMatchAccuracy(t *testing.T) {
	g, edgeIDs := gridGraph()
	rnd := rand.New(rand.NewSource(42))
	route := randomRoute(g, edgeIDs, nodeID(3, 3), 60, rnd)
	pts, truth := sampleRoute(g, route, 8.0, rnd, time.Unix(1700000000, 0))

	m := NewMatcher(g, DefaultConfig())
	results := matchAll(m, pts)
	if len(results) != len(pts) {
		t.Fatalf("结果数 %d 与输入点数 %d 不一致", len(results), len(pts))
	}
	byTime := make(map[time.Time]int64, len(results))
	for _, r := range results {
		byTime[r.Point.Time] = r.EdgeID
	}
	var correct int
	for i, p := range pts {
		if byTime[p.Time] == truth[i] {
			correct++
		}
	}
	acc := float64(correct) / float64(len(pts))
	t.Logf("匹配正确率 %.2f%% (%d/%d)", acc*100, correct, len(pts))
	if acc < 0.9 {
		t.Fatalf("匹配正确率 %.2f%% 低于 90%%", acc*100)
	}
}

// TestUTurnRoute 车辆沿道路行驶到尽头掉头，匹配结果应切换到反向路段。
func TestUTurnRoute(t *testing.T) {
	g, edgeIDs := gridGraph()
	rnd := rand.New(rand.NewSource(7))
	route := []int64{
		edgeIDs[[2]int64{nodeID(2, 2), nodeID(2, 3)}],
		edgeIDs[[2]int64{nodeID(2, 3), nodeID(2, 4)}],
		edgeIDs[[2]int64{nodeID(2, 4), nodeID(2, 3)}], // 掉头
		edgeIDs[[2]int64{nodeID(2, 3), nodeID(2, 2)}],
	}
	pts, truth := sampleRoute(g, route, 3.0, rnd, time.Unix(1700000000, 0))

	m := NewMatcher(g, DefaultConfig())
	results := matchAll(m, pts)
	byTime := make(map[time.Time]int64, len(results))
	for _, r := range results {
		byTime[r.Point.Time] = r.EdgeID
	}
	var correct int
	for i, p := range pts {
		if byTime[p.Time] == truth[i] {
			correct++
		}
	}
	acc := float64(correct) / float64(len(pts))
	t.Logf("掉头路线匹配正确率 %.2f%% (%d/%d)", acc*100, correct, len(pts))
	if acc < 0.8 {
		t.Fatalf("掉头路线匹配正确率 %.2f%% 低于 80%%", acc*100)
	}
}

// TestRouteDistanceUTurn 直接验证同一路段上倒退的掉头判定逻辑。
func TestRouteDistanceUTurn(t *testing.T) {
	e := &Edge{ID: 1, Length: 100}
	const th = 20.0
	cases := []struct {
		name     string
		from, to float64
		want     float64
	}{
		{"前进", 30, 60, 30},
		{"轻微倒退视为抖动", 50, 40, 0},
		{"倒退超阈值视为掉头", 80, 30, 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from := &candidate{edge: e, pos: c.from}
			to := &candidate{edge: e, pos: c.to}
			if got := routeDistance(from, to, 0, th); got != c.want {
				t.Fatalf("routeDistance = %v, 期望 %v", got, c.want)
			}
		})
	}
}

// TestSegmentation 相邻点间隔超过时间阈值时应分段并冲刷上一段结果。
func TestSegmentation(t *testing.T) {
	g, edgeIDs := gridGraph()
	rnd := rand.New(rand.NewSource(1))
	route := randomRoute(g, edgeIDs, nodeID(1, 1), 10, rnd)
	pts, _ := sampleRoute(g, route, 5.0, rnd, time.Unix(1700000000, 0))
	if len(pts) < 20 {
		t.Fatalf("采样点不足: %d", len(pts))
	}
	first, second := pts[:10], pts[10:20]
	// 第二段整体后移 10 分钟，制造时间断层
	for i := range second {
		second[i].Time = second[i].Time.Add(10 * time.Minute)
	}

	cfg := DefaultConfig()
	cfg.WindowSize = 100 // 大于分段内点数，确保只由分段触发冲刷
	m := NewMatcher(g, cfg)
	var seg1, seg2 []MatchResult
	for i, p := range first {
		got := m.Add(p)
		if i < len(first)-1 && len(got) > 0 {
			t.Fatalf("分段前不应冲刷结果, 第 %d 点返回 %d 条", i, len(got))
		}
	}
	seg1 = m.Add(second[0]) // 触发分段，冲刷第一段
	if len(seg1) != len(first) {
		t.Fatalf("分段冲刷出 %d 条结果, 期望 %d", len(seg1), len(first))
	}
	for _, p := range second[1:] {
		seg2 = append(seg2, m.Add(p)...)
	}
	seg2 = append(seg2, m.Flush()...)
	if len(seg2) != len(second) {
		t.Fatalf("第二段得到 %d 条结果, 期望 %d", len(seg2), len(second))
	}
}

// TestStreamingWindow 窗口确定后 Add 应流式输出，且总量与输入一致。
func TestStreamingWindow(t *testing.T) {
	g, edgeIDs := gridGraph()
	rnd := rand.New(rand.NewSource(9))
	route := randomRoute(g, edgeIDs, nodeID(4, 4), 20, rnd)
	pts, _ := sampleRoute(g, route, 5.0, rnd, time.Unix(1700000000, 0))

	cfg := DefaultConfig()
	cfg.WindowSize = 5
	m := NewMatcher(g, cfg)
	var streamed int
	for _, p := range pts {
		streamed += len(m.Add(p))
	}
	tail := m.Flush()
	if streamed == 0 {
		t.Fatal("窗口未流式输出任何结果")
	}
	if streamed+len(tail) != len(pts) {
		t.Fatalf("输出总数 %d 与输入 %d 不一致", streamed+len(tail), len(pts))
	}
}
