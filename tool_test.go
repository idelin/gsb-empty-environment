package pathcount

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

// bruteForce 全路径枚举：按邻居编号升序 DFS，到达 t 即停，
// 仅保留覆盖了全部必经点的路径。返回顺序即字典序。
func bruteForce(g *Graph, s, t int, req uint32) [][]int {
	var paths [][]int
	var dfs func(v int, mask uint32, path []int)
	dfs = func(v int, mask uint32, path []int) {
		if v == t {
			if mask&req == req {
				cp := append([]int(nil), path...)
				paths = append(paths, cp)
			}
			return
		}
		for _, w := range g.Neighbors(v) {
			if mask&(1<<uint(w)) != 0 {
				continue
			}
			dfs(w, mask|1<<uint(w), append(path, w))
		}
	}
	dfs(s, 1<<uint(s), []int{s})
	return paths
}

func reqMask(required []int) uint32 {
	var m uint32
	for _, v := range required {
		m |= 1 << uint(v)
	}
	return m
}

func pathsEqual(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

func hasPrefix(path, prefix []int) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

// TestCrossCheckRandom 在大量随机小图上用全枚举核对计数与分页。
func TestCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 400; iter++ {
		n := 2 + rng.Intn(7) // 2..8 个顶点
		g := NewGraph(n)
		for u := 0; u < n; u++ {
			for v := 0; v < n; v++ {
				if u != v && rng.Float64() < 0.35 {
					g.AddEdge(u, v)
				}
			}
		}
		s, tv := rng.Intn(n), rng.Intn(n)
		var required []int
		for v := 0; v < n; v++ {
			if rng.Float64() < 0.3 {
				required = append(required, v)
			}
		}
		want := bruteForce(g, s, tv, reqMask(required))

		tool := NewTool(g, s, tv, required, 0)
		res := tool.Count()
		if !res.Known {
			t.Fatalf("iter %d: count unknown on tiny graph", iter)
		}
		if res.Value.Cmp(big.NewInt(int64(len(want)))) != 0 {
			t.Fatalf("iter %d: count=%s, want %d (n=%d s=%d t=%d req=%v)",
				iter, res.Value, len(want), n, s, tv, required)
		}

		// 全量枚举顺序必须与暴力字典序一致
		page := tool.Enumerate([]int{s}, 0, -1)
		if !page.Known || !pathsEqual(page.Paths, want) {
			t.Fatalf("iter %d: enumerate mismatch (got %d paths, want %d)",
				iter, len(page.Paths), len(want))
		}
		if page.Total.Cmp(res.Value) != 0 || page.Version != res.Version {
			t.Fatalf("iter %d: page total/version inconsistent with count", iter)
		}

		// 多种页大小翻页拼接必须等于全量
		for _, ps := range []int{1, 2, 3, 5} {
			var got [][]int
			for off := 0; ; off += ps {
				p := tool.Enumerate([]int{s}, off, ps)
				if !p.Known {
					t.Fatalf("iter %d: page unknown", iter)
				}
				got = append(got, p.Paths...)
				if len(p.Paths) < ps {
					break
				}
			}
			if !pathsEqual(got, want) {
				t.Fatalf("iter %d: pagination size %d mismatch", iter, ps)
			}
		}

		// 随机前缀条件：枚举结果必须等于暴力过滤结果
		for k := 0; k < 3 && len(want) > 0; k++ {
			full := want[rng.Intn(len(want))]
			pl := 1 + rng.Intn(len(full))
			prefix := full[:pl]
			var filtered [][]int
			for _, p := range want {
				if hasPrefix(p, prefix) {
					filtered = append(filtered, p)
				}
			}
			p := tool.Enumerate(prefix, 0, -1)
			if !p.Known || !pathsEqual(p.Paths, filtered) {
				t.Fatalf("iter %d: prefix %v mismatch", iter, prefix)
			}
		}
	}
}

// TestEarlyArrivalAtT 提前到达 t 的路径不计入：必须经过必经点。
func TestEarlyArrivalAtT(t *testing.T) {
	g := NewGraph(3)
	g.AddEdge(0, 1) // 直达 t，但未经过 2
	g.AddEdge(0, 2)
	g.AddEdge(2, 1)
	tool := NewTool(g, 0, 1, []int{2}, 0)
	res := tool.Count()
	if !res.Known || res.Value.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("got %v (known=%v), want 1", res.Value, res.Known)
	}
	page := tool.Enumerate([]int{0}, 0, -1)
	if !pathsEqual(page.Paths, [][]int{{0, 2, 1}}) {
		t.Fatalf("got %v, want [[0 2 1]]", page.Paths)
	}
}

// TestIsolatedRequired 必经点为孤点（不可达）时计数为 0，且是确定的 0。
func TestIsolatedRequired(t *testing.T) {
	g := NewGraph(4)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	tool := NewTool(g, 0, 2, []int{3}, 0) // 顶点 3 是孤点
	res := tool.Count()
	if !res.Known || res.Value.Sign() != 0 {
		t.Fatalf("got %v (known=%v), want known 0", res.Value, res.Known)
	}
	page := tool.Enumerate([]int{0}, 0, -1)
	if !page.Known || page.Total.Sign() != 0 || len(page.Paths) != 0 {
		t.Fatalf("page = %+v, want empty known page", page)
	}
}

// TestRequiredConflict 必经点分布在互斥分支上，无法同时覆盖，计数为 0。
func TestRequiredConflict(t *testing.T) {
	g := NewGraph(4)
	g.AddEdge(0, 2)
	g.AddEdge(2, 1)
	g.AddEdge(0, 3)
	g.AddEdge(3, 1)
	tool := NewTool(g, 0, 1, []int{2, 3}, 0)
	res := tool.Count()
	if !res.Known || res.Value.Sign() != 0 {
		t.Fatalf("got %v (known=%v), want known 0", res.Value, res.Known)
	}
}

// TestBudgetUnknown 预算耗尽必须返回未知而非 0，且未知结果不得污染记忆：
// 增大预算后重试必须给出正确计数。
func TestBudgetUnknown(t *testing.T) {
	n := 8
	g := NewGraph(n)
	for u := 0; u < n; u++ {
		for v := 0; v < n; v++ {
			if u != v {
				g.AddEdge(u, v) // 完全图，状态数足够多
			}
		}
	}
	tool := NewTool(g, 0, 1, nil, 1) // 预算仅 1 个状态
	res := tool.Count()
	if res.Known {
		t.Fatalf("budget=1 should be unknown, got %v", res.Value)
	}
	if res.Value != nil {
		t.Fatalf("unknown result must carry nil value, got %v", res.Value)
	}

	tool.SetBudget(0) // 恢复默认预算重试
	res = tool.Count()
	want := bruteForce(g, 0, 1, 0)
	if !res.Known || res.Value.Cmp(big.NewInt(int64(len(want)))) != 0 {
		t.Fatalf("after retry got %v (known=%v), want %d", res.Value, res.Known, len(want))
	}

	// 枚举同样受预算约束：新工具在小预算下返回未知而不是部分结果
	fresh := NewTool(g, 0, 1, nil, 1)
	page := fresh.Enumerate([]int{0}, 0, 5)
	if page.Known || page.Paths != nil || page.Total != nil {
		t.Fatalf("enumerate under tiny budget = %+v, want unknown", page)
	}
}

// TestBigCount 用 K15 验证大整数计数（结果超过 2^32）。
func TestBigCount(t *testing.T) {
	n := 15
	g := NewGraph(n)
	for u := 0; u < n; u++ {
		for v := 0; v < n; v++ {
			if u != v {
				g.AddEdge(u, v)
			}
		}
	}
	// 完全图 K_n 中 s->t 的简单路径数 = sum_{j=0}^{n-2} (n-2)!/(n-2-j)!
	want := new(big.Int)
	term := new(big.Int)
	for j := 0; j <= n-2; j++ {
		// (n-2)!/(n-2-j)! 由连乘直接算
		term.SetInt64(1)
		for k := 0; k < j; k++ {
			term.Mul(term, big.NewInt(int64(n-2-k)))
		}
		want.Add(want, term)
	}
	if want.Cmp(big.NewInt(1<<32)) <= 0 {
		t.Fatalf("test expects count > 2^32, got %s", want)
	}
	tool := NewTool(g, 0, 1, nil, 0)
	res := tool.Count()
	if !res.Known || res.Value.Cmp(want) != 0 {
		t.Fatalf("got %v (known=%v), want %s", res.Value, res.Known, want)
	}
}

// TestVersionConsistency Update 后版本递增、记忆失效，计数与分页始终同版本。
func TestVersionConsistency(t *testing.T) {
	g := NewGraph(4)
	g.AddEdge(0, 3)
	tool := NewTool(g, 0, 3, nil, 0)

	r1 := tool.Count()
	if !r1.Known || r1.Value.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("v%d count = %v, want 1", r1.Version, r1.Value)
	}
	tool.Update(func(g *Graph) {
		g.AddEdge(0, 1)
		g.AddEdge(1, 2)
		g.AddEdge(2, 3)
	})
	r2 := tool.Count()
	if r2.Version != r1.Version+1 {
		t.Fatalf("version not bumped: %d -> %d", r1.Version, r2.Version)
	}
	if !r2.Known || r2.Value.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("v%d count = %v, want 2", r2.Version, r2.Value)
	}
	page := tool.Enumerate([]int{0}, 0, -1)
	if page.Version != r2.Version || page.Total.Cmp(r2.Value) != 0 {
		t.Fatalf("page version/total inconsistent with count: %+v vs %+v", page, r2)
	}
	if !pathsEqual(page.Paths, [][]int{{0, 1, 2, 3}, {0, 3}}) {
		t.Fatalf("paths = %v", page.Paths)
	}
}

// TestInvalidPrefix 非法前缀返回未知/失败而不是静默给空结果。
func TestInvalidPrefix(t *testing.T) {
	g := NewGraph(3)
	g.AddEdge(0, 1)
	tool := NewTool(g, 0, 1, nil, 0)
	for _, prefix := range [][]int{nil, {1}, {0, 2}, {0, 0}} {
		p := tool.Enumerate(prefix, 0, -1)
		if p.Known {
			t.Fatalf("prefix %v should be rejected", prefix)
		}
	}
}

// TestStartEqualsTarget s == t 时，空路径 [s] 在必经点都被 s 覆盖时计数为 1。
func TestStartEqualsTarget(t *testing.T) {
	g := NewGraph(3)
	g.AddEdge(0, 1)
	g.AddEdge(1, 2)
	if r := NewTool(g, 0, 0, nil, 0).Count(); !r.Known || r.Value.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("s==t no req: got %v, want 1", r.Value)
	}
	if r := NewTool(g, 0, 0, []int{2}, 0).Count(); !r.Known || r.Value.Sign() != 0 {
		t.Fatalf("s==t with req: got %v, want 0", r.Value)
	}
}

// TestMaxVertices 边界：22 点可构造，23 点 panic。
func TestMaxVertices(t *testing.T) {
	g := NewGraph(MaxVertices)
	for i := 0; i+1 < MaxVertices; i++ { // 链式图，1 条路径
		g.AddEdge(i, i+1)
	}
	tool := NewTool(g, 0, MaxVertices-1, []int{5, 17}, 0)
	if r := tool.Count(); !r.Known || r.Value.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("chain count = %v, want 1", r.Value)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("NewGraph(23) should panic")
		}
	}()
	NewGraph(MaxVertices + 1)
}

// Example 演示计数与分页的基本用法。
func Example() {
	g := NewGraph(4)
	g.AddUndirectedEdge(0, 1)
	g.AddUndirectedEdge(1, 2)
	g.AddUndirectedEdge(2, 3)
	g.AddEdge(0, 2)
	g.AddEdge(0, 3)
	tool := NewTool(g, 0, 3, []int{2}, 0)
	res := tool.Count()
	page := tool.Enumerate([]int{0}, 0, 10)
	fmt.Println(res.Value, res.Known)
	for _, p := range page.Paths {
		fmt.Println(p)
	}
	// Output:
	// 2 true
	// [0 1 2 3]
	// [0 2 3]
}
