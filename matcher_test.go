package matcher

import (
	"math/rand"
	"testing"
)

// bruteForce 用指数级枚举求最大权匹配的总产值，作为对拍基准。
// 工人可匹配一个未屏蔽任务（需有边）或保持未匹配。
func bruteForce(w [][]int64, has [][]bool, maskW, maskT []bool) int64 {
	n := len(w)
	m := 0
	if n > 0 {
		m = len(w[0])
	}
	used := make([]bool, m)
	var best int64
	var dfs func(u int, cur int64)
	dfs = func(u int, cur int64) {
		if u == n {
			if cur > best {
				best = cur
			}
			return
		}
		if maskW[u] {
			dfs(u+1, cur)
			return
		}
		dfs(u+1, cur) // 工人 u 不匹配
		for v := 0; v < m; v++ {
			if used[v] || maskT[v] || !has[u][v] {
				continue
			}
			used[v] = true
			dfs(u+1, cur+w[u][v])
			used[v] = false
		}
	}
	dfs(0, 0)
	return best
}

// randomGraph 生成 n×m 随机图：约 80% 的边存在，边权 [-5, 50]。
func randomGraph(r *rand.Rand, n, m int) (w [][]int64, has [][]bool) {
	w = make([][]int64, n)
	has = make([][]bool, n)
	for u := 0; u < n; u++ {
		w[u] = make([]int64, m)
		has[u] = make([]bool, m)
		for v := 0; v < m; v++ {
			if r.Intn(10) < 8 {
				has[u][v] = true
				w[u][v] = int64(r.Intn(56) - 5)
			}
		}
	}
	return w, has
}

func buildMatcher(n, m int, w [][]int64, has [][]bool) *Matcher {
	mt := New(n, m)
	for u := 0; u < n; u++ {
		for v := 0; v < m; v++ {
			if has[u][v] {
				mt.SetEdge(u, v, w[u][v])
			}
		}
	}
	return mt
}

// checkResult 校验匹配结果本身的合法性（边存在、互不冲突、未屏蔽）。
func checkResult(t *testing.T, mt *Matcher, res Result, has [][]bool, maskW, maskT []bool) {
	t.Helper()
	usedW := make(map[int]bool)
	usedT := make(map[int]bool)
	var sum int64
	for _, p := range res.Pairs {
		if !has[p.Worker][p.Task] {
			t.Fatalf("pair %+v uses non-existent edge", p)
		}
		if maskW[p.Worker] || maskT[p.Task] {
			t.Fatalf("pair %+v involves masked vertex", p)
		}
		if usedW[p.Worker] || usedT[p.Task] {
			t.Fatalf("pair %+v conflicts with another pair", p)
		}
		usedW[p.Worker] = true
		usedT[p.Task] = true
		sum += p.Weight
	}
	if sum != res.Total {
		t.Fatalf("Total=%d but pairs sum to %d", res.Total, sum)
	}
	if sum != mt.Total() {
		t.Fatalf("Total()=%d but result Total=%d", mt.Total(), res.Total)
	}
	// 机会成本一致性：Min 应等于所有未匹配顶点机会成本的最小值。
	if len(res.UnmatchedWorkers)+len(res.UnmatchedTasks) == 0 {
		if res.MinOpportunityCost != 0 {
			t.Fatalf("no unmatched vertex but MinOpportunityCost=%d", res.MinOpportunityCost)
		}
		return
	}
	first := true
	var minC int64
	for _, c := range res.WorkerOpportunity {
		if first || c < minC {
			minC = c
			first = false
		}
	}
	for _, c := range res.TaskOpportunity {
		if first || c < minC {
			minC = c
			first = false
		}
	}
	if minC != res.MinOpportunityCost {
		t.Fatalf("MinOpportunityCost=%d, want %d", res.MinOpportunityCost, minC)
	}
}

func TestBasicPerfectMatching(t *testing.T) {
	// 经典 3×3 例子，最优总产值 5+4+5=14... 实际手算：
	//   w0: t0=3 t1=4 t2=5
	//   w1: t0=2 t1=5 t2=1
	//   w2: t0=4 t1=2 t2=3
	// 最优：w0->t2(5), w1->t1(5), w2->t0(4) = 14
	mt := New(3, 3)
	w := [][]int64{{3, 4, 5}, {2, 5, 1}, {4, 2, 3}}
	for u := range w {
		for v, x := range w[u] {
			mt.SetEdge(u, v, x)
		}
	}
	res := mt.Match()
	if res.Total != 14 {
		t.Fatalf("Total=%d, want 14", res.Total)
	}
	if len(res.Pairs) != 3 {
		t.Fatalf("got %d pairs, want 3", len(res.Pairs))
	}
	if len(res.UnmatchedWorkers) != 0 || len(res.UnmatchedTasks) != 0 {
		t.Fatalf("unexpected unmatched: %+v", res)
	}
	if res.MinOpportunityCost != 0 {
		t.Fatalf("MinOpportunityCost=%d, want 0 (perfect matching)", res.MinOpportunityCost)
	}
}

func TestRectangular(t *testing.T) {
	// 2 工人 4 任务：最多匹配 2 对。
	mt := New(2, 4)
	w := [][]int64{{9, 1, 1, 1}, {1, 8, 7, 6}}
	for u := range w {
		for v, x := range w[u] {
			mt.SetEdge(u, v, x)
		}
	}
	res := mt.Match()
	if res.Total != 9+8 {
		t.Fatalf("Total=%d, want 17", res.Total)
	}
	if len(res.UnmatchedTasks) != 2 {
		t.Fatalf("unmatched tasks=%v, want 2", res.UnmatchedTasks)
	}

	// 4 工人 2 任务：最多匹配 2 对，2 名工人未匹配。
	mt2 := New(4, 2)
	w2 := [][]int64{{9, 1}, {8, 2}, {3, 7}, {4, 5}}
	for u := range w2 {
		for v, x := range w2[u] {
			mt2.SetEdge(u, v, x)
		}
	}
	res2 := mt2.Match()
	if res2.Total != 9+7 {
		t.Fatalf("Total=%d, want 16", res2.Total)
	}
	if len(res2.UnmatchedWorkers) != 2 {
		t.Fatalf("unmatched workers=%v, want 2", res2.UnmatchedWorkers)
	}
	// 未匹配工人为 w2(对 t1 权 7 被 w3... 实际 w3->t1? 9+7: w0->t0, w2->t1)
	// 未匹配工人 w1: 放弃最大产值 max(8,2)=8；w3: max(4,5)=5 → 最小机会成本 5。
	if res2.MinOpportunityCost != 5 {
		t.Fatalf("MinOpportunityCost=%d, want 5", res2.MinOpportunityCost)
	}
}

func TestRandomVsBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		n := 1 + r.Intn(5)
		m := 1 + r.Intn(5)
		w, has := randomGraph(r, n, m)
		mt := buildMatcher(n, m, w, has)
		res := mt.Match()
		maskW := make([]bool, n)
		maskT := make([]bool, m)
		checkResult(t, mt, res, has, maskW, maskT)
		if want := bruteForce(w, has, maskW, maskT); res.Total != want {
			t.Fatalf("iter=%d n=%d m=%d: Total=%d, brute force=%d", iter, n, m, res.Total, want)
		}
	}
}

func TestDynamicUpdatesVsBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for iter := 0; iter < 120; iter++ {
		n := 1 + r.Intn(4)
		m := 1 + r.Intn(4)
		w, has := randomGraph(r, n, m)
		maskW := make([]bool, n)
		maskT := make([]bool, m)
		mt := buildMatcher(n, m, w, has)
		mt.Match()
		// 随机执行 20 次动态操作，每次都与暴力解对拍。
		for step := 0; step < 20; step++ {
			var res Result
			switch r.Intn(5) {
			case 0, 1: // 调整/新增单条边权
				u, v := r.Intn(n), r.Intn(m)
				x := int64(r.Intn(56) - 5)
				w[u][v] = x
				has[u][v] = true
				res = mt.UpdateEdge(u, v, x)
			case 2: // 屏蔽/恢复工人
				u := r.Intn(n)
				if maskW[u] {
					maskW[u] = false
					res = mt.UnmaskWorker(u)
				} else {
					maskW[u] = true
					res = mt.MaskWorker(u)
				}
			case 3: // 屏蔽/恢复任务
				v := r.Intn(m)
				if maskT[v] {
					maskT[v] = false
					res = mt.UnmaskTask(v)
				} else {
					maskT[v] = true
					res = mt.MaskTask(v)
				}
			default: // 删除一条边
				u, v := r.Intn(n), r.Intn(m)
				has[u][v] = false
				mt.RemoveEdge(u, v)
				res = mt.Rematch()
			}
			checkResult(t, mt, res, has, maskW, maskT)
			if want := bruteForce(w, has, maskW, maskT); res.Total != want {
				t.Fatalf("iter=%d step=%d: Total=%d, brute force=%d", iter, step, res.Total, want)
			}
		}
	}
}

// TestMaskHighValueWorker 验收场景：高产值工人临时请假下线，
// 局部调整顶标后快速切换到次优匹配；恢复后应回到原最优。
func TestMaskHighValueWorker(t *testing.T) {
	mt := New(3, 3)
	w := [][]int64{
		{100, 90, 80}, // 高产值工人 w0
		{10, 12, 11},
		{9, 8, 13},
	}
	for u := range w {
		for v, x := range w[u] {
			mt.SetEdge(u, v, x)
		}
	}
	before := mt.Match()
	if before.Total != 100+12+13 {
		t.Fatalf("before Total=%d, want 125", before.Total)
	}

	// w0 下线：次优为 w1->t0(10)+w2->t2(13)=23 或 w1->t1(12)+w2->t2(13)=25。
	after := mt.MaskWorker(0)
	if after.Total != 25 {
		t.Fatalf("after mask Total=%d, want 25", after.Total)
	}
	if len(after.OfflineWorkers) != 1 || after.OfflineWorkers[0] != 0 {
		t.Fatalf("OfflineWorkers=%v, want [0]", after.OfflineWorkers)
	}
	for _, p := range after.Pairs {
		if p.Worker == 0 {
			t.Fatalf("masked worker still matched: %+v", p)
		}
	}

	// w0 复工：恢复最优。
	restored := mt.UnmaskWorker(0)
	if restored.Total != before.Total {
		t.Fatalf("restored Total=%d, want %d", restored.Total, before.Total)
	}
}

func TestUpdateEdgeSwitch(t *testing.T) {
	// 单条边权变化应引起匹配切换。
	mt := New(2, 2)
	mt.SetEdge(0, 0, 10)
	mt.SetEdge(0, 1, 9)
	mt.SetEdge(1, 0, 1)
	mt.SetEdge(1, 1, 8)
	res := mt.Match()
	if res.Total != 10+8 {
		t.Fatalf("Total=%d, want 18", res.Total)
	}
	// 抬高 w1->t0 到 20：最优变为 w0->t1(9)+w1->t0(20)=29。
	res = mt.UpdateEdge(1, 0, 20)
	if res.Total != 29 {
		t.Fatalf("after update Total=%d, want 29", res.Total)
	}
	// 调低 w1->t0 回 1：应回到 18。
	res = mt.UpdateEdge(1, 0, 1)
	if res.Total != 18 {
		t.Fatalf("after revert Total=%d, want 18", res.Total)
	}
}

func TestOpportunityCost(t *testing.T) {
	// 3 工人抢 2 任务，必有一人未匹配。
	mt := New(3, 2)
	w := [][]int64{{50, 40}, {30, 45}, {20, 10}}
	for u := range w {
		for v, x := range w[u] {
			mt.SetEdge(u, v, x)
		}
	}
	res := mt.Match()
	// 最优：w0->t0(50)+w1->t1(45)=95，w2 未匹配。
	if res.Total != 95 {
		t.Fatalf("Total=%d, want 95", res.Total)
	}
	if len(res.UnmatchedWorkers) != 1 || res.UnmatchedWorkers[0] != 2 {
		t.Fatalf("UnmatchedWorkers=%v, want [2]", res.UnmatchedWorkers)
	}
	// w2 放弃的最大产值为 max(20,10)=20，即最小机会成本。
	if res.MinOpportunityCost != 20 {
		t.Fatalf("MinOpportunityCost=%d, want 20", res.MinOpportunityCost)
	}
	if res.WorkerOpportunity[2] != 20 {
		t.Fatalf("WorkerOpportunity[2]=%d, want 20", res.WorkerOpportunity[2])
	}
}

func TestLabelsFeasible(t *testing.T) {
	// 顶标对偶可行性：lx[u]+ly[v] >= a[u][v]，且匹配边紧绷。
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 50; iter++ {
		n := 1 + r.Intn(4)
		m := 1 + r.Intn(4)
		w, has := randomGraph(r, n, m)
		mt := buildMatcher(n, m, w, has)
		mt.Match()
		// 再来几次动态操作后同样校验。
		for step := 0; step < 5; step++ {
			u, v := r.Intn(n), r.Intn(m)
			x := int64(r.Intn(56) - 5)
			w[u][v] = x
			has[u][v] = true
			mt.UpdateEdge(u, v, x)
		}
		lx, ly := mt.Labels()
		size := n
		if m > size {
			size = m
		}
		for u := 0; u < size; u++ {
			for v := 0; v < size; v++ {
				var a int64
				if u < n && v < m && has[u][v] {
					a = w[u][v]
				}
				if lx[u]+ly[v] < a {
					t.Fatalf("iter=%d: dual infeasible at (%d,%d): %d+%d < %d", iter, u, v, lx[u], ly[v], a)
				}
			}
		}
	}
}

func TestEmptyAndSingle(t *testing.T) {
	mt := New(0, 0)
	if res := mt.Match(); res.Total != 0 {
		t.Fatalf("empty Total=%d, want 0", res.Total)
	}
	mt2 := New(1, 1)
	mt2.SetEdge(0, 0, 7)
	if res := mt2.Match(); res.Total != 7 || len(res.Pairs) != 1 {
		t.Fatalf("single: %+v", res)
	}
	// 负权边：不匹配更优。
	mt3 := New(1, 1)
	mt3.SetEdge(0, 0, -3)
	res := mt3.Match()
	if res.Total != 0 || len(res.Pairs) != 0 {
		t.Fatalf("negative edge should stay unmatched: %+v", res)
	}
	if res.MinOpportunityCost != 0 {
		t.Fatalf("MinOpportunityCost=%d, want 0 (only negative edge forgone)", res.MinOpportunityCost)
	}
}
