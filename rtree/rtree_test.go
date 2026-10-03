package rtree

import (
	"math/rand"
	"sort"
	"testing"
)

// ---- 几何基础 ----

func TestRectBasics(t *testing.T) {
	a := NewRect(Point{3, 4}, Point{1, 2})
	if a.Min != (Point{1, 2}) || a.Max != (Point{3, 4}) {
		t.Fatalf("NewRect 未规范化: %+v", a)
	}
	if got := a.Area(); got != 4 {
		t.Fatalf("Area = %v, want 4", got)
	}
	b := Rect{Min: Point{2, 1}, Max: Point{5, 3}}
	if !a.Intersects(b) {
		t.Fatal("应相交")
	}
	u := a.Union(b)
	if u.Min != (Point{1, 1}) || u.Max != (Point{5, 4}) {
		t.Fatalf("Union 错误: %+v", u)
	}
	if !u.Contains(a) || !u.Contains(b) || a.Contains(b) {
		t.Fatal("Contains 错误")
	}
	c := Rect{Min: Point{10, 10}, Max: Point{11, 11}}
	if a.Intersects(c) || c.Intersects(a) {
		t.Fatal("不应相交")
	}
	if got := a.overlapArea(b); got != 1 {
		t.Fatalf("overlapArea = %v, want 1", got)
	}
	if got := a.overlapArea(c); got != 0 {
		t.Fatalf("overlapArea = %v, want 0", got)
	}
}

// ---- 辅助 ----

// bruteForce 暴力扫描基准答案。
func bruteForce(rects []Rect, window Rect) []int {
	var out []int
	for i, r := range rects {
		if r.Intersects(window) {
			out = append(out, i)
		}
	}
	sort.Ints(out)
	return out
}

func sortedIDs(ids []int) []int {
	s := append([]int(nil), ids...)
	sort.Ints(s)
	return s
}

func equalIDs(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// genShops 生成上海市区范围内的随机商铺经纬度。
func genShops(rng *rand.Rand, n int) []Rect {
	rects := make([]Rect, n)
	for i := range rects {
		lon := 121.35 + rng.Float64()*0.30 // 经度 121.35~121.65
		lat := 31.15 + rng.Float64()*0.25  // 纬度 31.15~31.40
		rects[i] = PointRect(Point{lon, lat})
	}
	return rects
}

// ---- 正确性：插入 + 范围查询 vs 暴力扫描 ----

func TestInsertAndSearchMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const n = 5000
	rects := genShops(rng, n)

	tr := New(8, 3)
	for i, r := range rects {
		tr.Insert(i, r)
	}
	if tr.Size() != n {
		t.Fatalf("Size = %d, want %d", tr.Size(), n)
	}

	// 200 个随机窗口 + 若干极端窗口。
	windows := make([]Rect, 0, 220)
	for i := 0; i < 200; i++ {
		x := 121.30 + rng.Float64()*0.40
		y := 31.10 + rng.Float64()*0.35
		w := rng.Float64() * 0.08
		h := rng.Float64() * 0.08
		windows = append(windows, NewRect(Point{x, y}, Point{x + w, y + h}))
	}
	windows = append(windows,
		NewRect(Point{120, 30}, Point{123, 32}),             // 全覆盖
		NewRect(Point{0, 0}, Point{1, 1}),                   // 完全不相交
		PointRect(Point{121.5, 31.25}),                      // 零面积窗口
		NewRect(Point{121.35, 31.15}, Point{121.35, 31.40}), // 竖线
	)

	for qi, w := range windows {
		got := sortedIDs(tr.Search(w))
		want := bruteForce(rects, w)
		if !equalIDs(got, want) {
			t.Fatalf("窗口 %d %+v: 命中 %d 条, 期望 %d 条", qi, w, len(got), len(want))
		}
	}
}

// 非点状矩形（多边形外包矩形）的插入与查询。
func TestPolygonBoundingBoxes(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const n = 2000
	rects := make([]Rect, n)
	tr := New(10, 4)
	for i := range rects {
		x := rng.Float64() * 100
		y := rng.Float64() * 100
		w := rng.Float64() * 3
		h := rng.Float64() * 3
		rects[i] = NewRect(Point{x, y}, Point{x + w, y + h})
		tr.Insert(i, rects[i])
	}
	for i := 0; i < 100; i++ {
		x := rng.Float64() * 100
		y := rng.Float64() * 100
		win := NewRect(Point{x, y}, Point{x + rng.Float64()*20, y + rng.Float64()*20})
		if got, want := sortedIDs(tr.Search(win)), bruteForce(rects, win); !equalIDs(got, want) {
			t.Fatalf("窗口 %d: 命中 %d, 期望 %d", i, len(got), len(want))
		}
	}
}

// ---- 结构不变量：树不退化 ----

func TestTreeInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const n = 20000
	rects := genShops(rng, n)
	tr := New(8, 3)
	for i, r := range rects {
		tr.Insert(i, r)
	}

	// 1) 所有条目恰好可达一次。
	seen := make(map[int]int)
	tr.VisitEntries(func(id int, r Rect) { seen[id]++ })
	if len(seen) != n {
		t.Fatalf("可达条目 %d, 期望 %d", len(seen), n)
	}
	for id, c := range seen {
		if c != 1 {
			t.Fatalf("条目 %d 出现 %d 次", id, c)
		}
	}

	// 2) 逐节点校验：填充率、父子 bbox 一致、叶子同层。
	var check func(nd *node, parent *node, depth int)
	leafDepth := -1
	nodes := 0
	check = func(nd *node, parent *node, depth int) {
		nodes++
		if nd != tr.root {
			if len(nd.entries) < tr.minEntries {
				t.Fatalf("节点条目数 %d 低于最小填充 %d", len(nd.entries), tr.minEntries)
			}
			if len(nd.entries) > tr.maxEntries {
				t.Fatalf("节点条目数 %d 超过容量 %d", len(nd.entries), tr.maxEntries)
			}
			if nd.parent != parent {
				t.Fatal("parent 指针错误")
			}
		}
		if nd.leaf {
			if leafDepth == -1 {
				leafDepth = depth
			} else if leafDepth != depth {
				t.Fatalf("叶子深度不一致: %d vs %d", depth, leafDepth)
			}
			return
		}
		for _, e := range nd.entries {
			// 父条目 bbox 必须与子节点实际 bbox 完全一致。
			cb := e.child.bbox()
			if e.bbox != cb {
				t.Fatalf("父条目 bbox %+v 与子节点实际 bbox %+v 不一致", e.bbox, cb)
			}
			check(e.child, nd, depth+1)
		}
	}
	check(tr.root, nil, 1)

	// 3) 树高应是对数级：log_4(20000) ≈ 7.2，留足余量。
	if tr.Height() > 12 {
		t.Fatalf("树高 %d 异常，疑似退化", tr.Height())
	}
	t.Logf("n=%d 树高=%d 节点数=%d 平均填充=%.1f/%d",
		n, tr.Height(), nodes, float64(n)/float64(nodes), tr.maxEntries)
}

// ---- 重插入机制有效性：与禁用重插入的对照 ----

// siblingOverlap 计算整棵树兄弟节点外包矩形两两重叠面积之和。
func siblingOverlap(tr *RTree) float64 {
	total := 0.0
	var walk func(nd *node)
	walk = func(nd *node) {
		if nd.leaf {
			return
		}
		for i := 0; i < len(nd.entries); i++ {
			for j := i + 1; j < len(nd.entries); j++ {
				total += nd.entries[i].bbox.overlapArea(nd.entries[j].bbox)
			}
			walk(nd.entries[i].child)
		}
	}
	walk(tr.root)
	return total
}

func countNodes(tr *RTree) int {
	count := 0
	var walk func(nd *node)
	walk = func(nd *node) {
		count++
		if !nd.leaf {
			for _, e := range nd.entries {
				walk(e.child)
			}
		}
	}
	walk(tr.root)
	return count
}

func TestReinsertReducesOverlap(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	const n = 20000
	// 聚簇分布：更容易诱发退化，放大重插入的收益。
	rects := make([]Rect, n)
	for i := range rects {
		cx := float64(rng.Intn(5)) * 0.05
		cy := float64(rng.Intn(5)) * 0.05
		lon := 121.35 + cx + rng.Float64()*0.06
		lat := 31.15 + cy + rng.Float64()*0.06
		rects[i] = PointRect(Point{lon, lat})
	}

	with := New(8, 3)
	without := New(8, 3)
	without.disableReinsert = true
	for i, r := range rects {
		with.Insert(i, r)
		without.Insert(i, r)
	}

	ovWith := siblingOverlap(with)
	ovWithout := siblingOverlap(without)
	ndWith := countNodes(with)
	ndWithout := countNodes(without)
	t.Logf("重插入:   重叠=%.6f 节点数=%d 树高=%d", ovWith, ndWith, with.Height())
	t.Logf("禁用重插入: 重叠=%.6f 节点数=%d 树高=%d", ovWithout, ndWithout, without.Height())
	if ovWith >= ovWithout {
		t.Fatalf("重插入未降低重叠: %.6f >= %.6f", ovWith, ovWithout)
	}
	if ndWith > ndWithout {
		t.Fatalf("重插入节点数更多: %d > %d", ndWith, ndWithout)
	}
}

// ---- 边界情形 ----

func TestEdgeCases(t *testing.T) {
	tr := New(4, 2)
	if got := tr.Search(NewRect(Point{0, 0}, Point{1, 1})); len(got) != 0 {
		t.Fatal("空树查询应无结果")
	}
	// 重复坐标。
	for i := 0; i < 100; i++ {
		tr.Insert(i, PointRect(Point{1, 1}))
	}
	if got := tr.Search(PointRect(Point{1, 1})); len(got) != 100 {
		t.Fatalf("重复点查询命中 %d, 期望 100", len(got))
	}
	if got := tr.Search(NewRect(Point{2, 2}, Point{3, 3})); len(got) != 0 {
		t.Fatal("不相交窗口应无结果")
	}
	// 单点。
	single := New(4, 2)
	single.Insert(7, PointRect(Point{5, 5}))
	if got := single.Search(NewRect(Point{4, 4}, Point{6, 6})); len(got) != 1 || got[0] != 7 {
		t.Fatalf("单点查询错误: %v", got)
	}
}

// ---- 基准 ----

func BenchmarkInsert10K(b *testing.B) {
	rng := rand.New(rand.NewSource(3))
	rects := genShops(rng, 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr := New(8, 3)
		for id, r := range rects {
			tr.Insert(id, r)
		}
	}
}

func BenchmarkRangeQuery(b *testing.B) {
	rng := rand.New(rand.NewSource(3))
	rects := genShops(rng, 100000)
	tr := New(8, 3)
	for i, r := range rects {
		tr.Insert(i, r)
	}
	win := NewRect(Point{121.45, 31.25}, Point{121.50, 31.30})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Search(win)
	}
}
