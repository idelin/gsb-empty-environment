package rematch

import (
	"math/rand"
	"reflect"
	"testing"
)

// ---------- 暴力枚举（小规模核对基准） ----------

// bruteKey 为匹配方案的评价键：先未匹配人数，再岗位变更次数，
// 最后按员工顺序的岗位向量（未匹配取哨兵 m）。字典序小者优。
func bruteKey(assign, orig []int, m int) []int {
	unmatched, changes := 0, 0
	key := make([]int, 0, len(assign)+2)
	for e, a := range assign {
		if a == Unassigned {
			unmatched++
		}
		if orig[e] != Unassigned && a != orig[e] {
			changes++
		}
	}
	key = append(key, unmatched, changes)
	for _, a := range assign {
		if a == Unassigned {
			key = append(key, m)
		} else {
			key = append(key, a)
		}
	}
	return key
}

func lessKey(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// bruteForce 枚举所有匹配，返回最优方案（assign[e] 为员工 e 的岗位）。
func bruteForce(n, m int, adj [][]int, orig []int) []int {
	var best []int
	var bestKey []int
	assign := make([]int, n)
	used := make([]bool, m)
	var rec func(e int)
	rec = func(e int) {
		if e == n {
			key := bruteKey(assign, orig, m)
			if bestKey == nil || lessKey(key, bestKey) {
				bestKey = key
				best = append([]int(nil), assign...)
			}
			return
		}
		assign[e] = Unassigned
		rec(e + 1)
		for _, p := range adj[e] {
			if !used[p] {
				used[p] = true
				assign[e] = p
				rec(e + 1)
				used[p] = false
			}
		}
		assign[e] = Unassigned
	}
	rec(0)
	return best
}

func buildAdj(n, m int, edges [][2]int) [][]int {
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
	}
	return adj
}

// ---------- 结果校验辅助 ----------

func checkValid(t *testing.T, n, m int, edges [][2]int, assign []int) {
	t.Helper()
	allowed := make(map[[2]int]bool)
	for _, e := range edges {
		allowed[e] = true
	}
	used := make(map[int]int)
	for e := 0; e < n; e++ {
		a := assign[e]
		if a == Unassigned {
			continue
		}
		if a < 0 || a >= m {
			t.Fatalf("员工 %d 分配到非法岗位 %d", e, a)
		}
		if !allowed[[2]int{e, a}] {
			t.Fatalf("员工 %d 分配到不存在的边 (%d,%d)", e, e, a)
		}
		if prev, ok := used[a]; ok {
			t.Fatalf("岗位 %d 同时分配给员工 %d 和 %d", a, prev, e)
		}
		used[a] = e
	}
}

func checkChains(t *testing.T, n int, orig, assign []int, res Result) {
	t.Helper()
	// 链中每一步都与原/新匹配一致，且确为变动。
	var chainMoves []Move
	for _, c := range res.Chains {
		if len(c.Moves) == 0 {
			t.Fatalf("空调整链")
		}
		for i, mv := range c.Moves {
			if mv.From != orig[mv.Employee] || mv.To != assign[mv.Employee] {
				t.Fatalf("链中移动 %+v 与匹配结果不符", mv)
			}
			if mv.From == mv.To {
				t.Fatalf("链中包含未变动的员工 %+v", mv)
			}
			if i+1 < len(c.Moves) && c.Moves[i].To != c.Moves[i+1].From {
				t.Fatalf("链 %+v 中第 %d 步与第 %d 步不衔接", c, i, i+1)
			}
		}
		if c.Cyclic && len(c.Moves) > 1 &&
			c.Moves[len(c.Moves)-1].To != c.Moves[0].From {
			t.Fatalf("环链 %+v 首尾不衔接", c)
		}
		chainMoves = append(chainMoves, c.Moves...)
	}
	// 链的并集恰好是全部失配人员。
	want := make(map[int]bool)
	for e := 0; e < n; e++ {
		if orig[e] != assign[e] {
			want[e] = true
		}
	}
	got := make(map[int]bool)
	for _, mv := range chainMoves {
		if got[mv.Employee] {
			t.Fatalf("员工 %d 出现在多条链中", mv.Employee)
		}
		got[mv.Employee] = true
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("链覆盖的失配人员 %v 与实际 %v 不符", got, want)
	}
	// Moves 列表与链一致。
	if len(res.Moves) != len(chainMoves) {
		t.Fatalf("Moves 数量 %d 与链中移动数量 %d 不符", len(res.Moves), len(chainMoves))
	}
	for _, mv := range res.Moves {
		if !got[mv.Employee] {
			t.Fatalf("Moves 中员工 %d 未出现在任何链中", mv.Employee)
		}
	}
}

// ---------- 针对性单元测试 ----------

func TestKeepOriginalWhenPossible(t *testing.T) {
	// 两人争一岗，原本在岗者保留，未分配者保持未分配（不算变更）。
	res := Reassign(2, 1, [][2]int{{0, 0}, {1, 0}}, []int{0, Unassigned})
	want := []int{0, Unassigned}
	if !reflect.DeepEqual(res.Assignment, want) {
		t.Fatalf("got %v want %v", res.Assignment, want)
	}
	if len(res.Moves) != 0 {
		t.Fatalf("不应有失配人员: %v", res.Moves)
	}
}

func TestFailedEdgeForcesSwapCycle(t *testing.T) {
	// 边 (0,0) 失效：0 顶到 1 的岗位，1 顶到 0 的岗位，形成环链。
	res := Reassign(2, 2, [][2]int{{0, 1}, {1, 0}, {1, 1}}, []int{0, 1})
	want := []int{1, 0}
	if !reflect.DeepEqual(res.Assignment, want) {
		t.Fatalf("got %v want %v", res.Assignment, want)
	}
	if len(res.Chains) != 1 || !res.Chains[0].Cyclic {
		t.Fatalf("应为一条环链: %+v", res.Chains)
	}
	checkChains(t, 2, []int{0, 1}, res.Assignment, res)
}

func TestDisplacementChain(t *testing.T) {
	// 新员工 2 入职 p0，连锁顶岗：2->0, 0->1, 1->2。
	edges := [][2]int{{0, 1}, {1, 1}, {1, 2}, {2, 0}}
	orig := []int{0, 1, Unassigned}
	res := Reassign(3, 3, edges, orig)
	want := []int{1, 2, 0}
	if !reflect.DeepEqual(res.Assignment, want) {
		t.Fatalf("got %v want %v", res.Assignment, want)
	}
	if len(res.Chains) != 1 || res.Chains[0].Cyclic {
		t.Fatalf("应为一条路径链: %+v", res.Chains)
	}
	c := res.Chains[0]
	wantChain := []Move{
		{Employee: 2, From: Unassigned, To: 0},
		{Employee: 0, From: 0, To: 1},
		{Employee: 1, From: 1, To: 2},
	}
	if !reflect.DeepEqual(c.Moves, wantChain) {
		t.Fatalf("链 %+v 与期望 %+v 不符", c.Moves, wantChain)
	}
	checkChains(t, 3, orig, res.Assignment, res)
}

func TestLexicographicTieBreak(t *testing.T) {
	// 无历史包袱的完全二分图：按员工顺序取字典序最小向量 [0 1 2]。
	var edges [][2]int
	for e := 0; e < 3; e++ {
		for p := 0; p < 3; p++ {
			edges = append(edges, [2]int{e, p})
		}
	}
	res := Reassign(3, 3, edges, []int{Unassigned, Unassigned, Unassigned})
	want := []int{0, 1, 2}
	if !reflect.DeepEqual(res.Assignment, want) {
		t.Fatalf("got %v want %v", res.Assignment, want)
	}
}

func TestLexicographicPrefersRealPositionForEarlierEmployee(t *testing.T) {
	// 只能匹配一人时，优先让靠前的员工上岗（哨兵大于任何岗位编号）。
	res := Reassign(2, 1, [][2]int{{0, 0}, {1, 0}}, []int{Unassigned, Unassigned})
	want := []int{0, Unassigned}
	if !reflect.DeepEqual(res.Assignment, want) {
		t.Fatalf("got %v want %v", res.Assignment, want)
	}
}

func TestLostPositionCountsAsChange(t *testing.T) {
	// 员工 0 的边失效且无替代：失去岗位，记为失配。
	res := Reassign(1, 1, nil, []int{0})
	if res.Assignment[0] != Unassigned {
		t.Fatalf("got %v", res.Assignment)
	}
	wantMoves := []Move{{Employee: 0, From: 0, To: Unassigned}}
	if !reflect.DeepEqual(res.Moves, wantMoves) {
		t.Fatalf("got %v want %v", res.Moves, wantMoves)
	}
	checkChains(t, 1, []int{0}, res.Assignment, res)
}

func TestMaxCardinalityBeatsKeeping(t *testing.T) {
	// 保持原岗只能匹配 1 人，换人可匹配 2 人：人数优先。
	// 员工 0 原岗 0；员工 1 只能上岗 1；员工 0 也能上岗 1。
	edges := [][2]int{{0, 0}, {0, 1}, {1, 1}}
	res := Reassign(2, 2, edges, []int{0, Unassigned})
	want := []int{0, 1}
	if !reflect.DeepEqual(res.Assignment, want) {
		t.Fatalf("got %v want %v", res.Assignment, want)
	}
}

func TestInvalidEdgesIgnored(t *testing.T) {
	edges := [][2]int{{-1, 0}, {0, 0}, {0, 5}, {0, 0}}
	res := Reassign(1, 1, edges, []int{Unassigned})
	if res.Assignment[0] != 0 {
		t.Fatalf("got %v", res.Assignment)
	}
}

// ---------- 随机小规模暴力核对 ----------

func TestRandomAgainstBruteForce(t *testing.T) {
	const trials = 400
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < trials; trial++ {
		n := 1 + rng.Intn(5) // 1..5 名员工
		m := rng.Intn(6)     // 0..5 个岗位
		// 完整候选图。
		var full [][2]int
		for e := 0; e < n; e++ {
			for p := 0; p < m; p++ {
				if rng.Float64() < 0.6 {
					full = append(full, [2]int{e, p})
				}
			}
		}
		// 在完整图上贪心构造一个原匹配。
		orig := make([]int, n)
		usedP := make([]bool, m)
		perm := rng.Perm(n)
		for e := 0; e < n; e++ {
			orig[e] = Unassigned
		}
		for _, e := range perm {
			var cands []int
			for _, ed := range full {
				if ed[0] == e && !usedP[ed[1]] {
					cands = append(cands, ed[1])
				}
			}
			if len(cands) > 0 {
				p := cands[rng.Intn(len(cands))]
				orig[e] = p
				usedP[p] = true
			}
		}
		// 部分边失效，得到当前图。
		var edges [][2]int
		for _, ed := range full {
			if rng.Float64() < 0.7 {
				edges = append(edges, ed)
			}
		}

		res := Reassign(n, m, edges, orig)
		checkValid(t, n, m, edges, res.Assignment)
		adj := buildAdj(n, m, edges)
		want := bruteForce(n, m, adj, orig)
		if !reflect.DeepEqual(res.Assignment, want) {
			t.Fatalf("trial %d: n=%d m=%d edges=%v orig=%v\ngot  %v\nwant %v",
				trial, n, m, edges, orig, res.Assignment, want)
		}
		checkChains(t, n, orig, res.Assignment, res)
	}
}
