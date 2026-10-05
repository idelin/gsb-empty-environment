package main

import (
	"math/rand"
	"testing"
)

// bruteForce 穷举所有不超过 K 组的连续分组，返回字典序最优的 (最大误差, 加权SSE)。
// 用于小规模核验 DP 求解器。
func bruteForce(levels []int, w []int64, K int) (bestE int, bestSSE int64) {
	n := len(levels)
	if K > n {
		K = n
	}
	bestE, bestSSE = 1<<30, int64(1<<62)

	// 枚举 n-1 个间隙的切分，组数 <= K
	var rec func(pos, groups int, cuts []int)
	cuts := []int{0}
	eval := func() {
		E, sse := partitionCost(levels, w, cuts)
		if E < bestE || (E == bestE && sse < bestSSE) {
			bestE, bestSSE = E, sse
		}
	}
	rec = func(pos, groups int, _ []int) {
		if pos == n {
			eval()
			return
		}
		// 当前组延伸到 pos+1（不切）
		rec(pos+1, groups, nil)
		// 在 pos 后切一刀（开启新组）
		if groups < K && pos+1 < n {
			cuts = append(cuts, pos+1)
			rec(pos+1, groups+1, nil)
			cuts = cuts[:len(cuts)-1]
		}
	}
	rec(0, 1, nil)
	return bestE, bestSSE
}

// partitionCost 计算由 cuts（各组起始下标，cuts[0]=0）划分的目标值：
// 组 g 的最小最大误差为 ceil(直径/2)，整体 E 取各组最大；
// 每组在 c ∈ [hi-E, lo+E] 约束下取加权均值夹取后的整数代表值求 SSE。
func partitionCost(levels []int, w []int64, cuts []int) (int, int64) {
	n := len(levels)
	type span struct{ i, j int }
	var spans []span
	for s := 0; s < len(cuts); s++ {
		end := n - 1
		if s+1 < len(cuts) {
			end = cuts[s+1] - 1
		}
		spans = append(spans, span{cuts[s], end})
	}
	E := 0
	for _, sp := range spans {
		d := levels[sp.j] - levels[sp.i]
		if (d+1)/2 > E {
			E = (d + 1) / 2
		}
	}
	var sse int64
	for _, sp := range spans {
		_, c := groupCost(levels, w, sp.i, sp.j, E)
		sse += c
	}
	return E, sse
}

func TestSolveAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		maxV := 4 + rng.Intn(14) // 灰度范围 0..maxV
		var levels []int
		var w []int64
		for v := 0; v <= maxV; v++ {
			if c := rng.Intn(6); c > 0 { // 允许空灰度
				levels = append(levels, v)
				w = append(w, int64(c))
			}
		}
		if len(levels) == 0 {
			continue
		}
		K := 1 + rng.Intn(5)
		res := Solve(levels, w, K)
		wantE, wantSSE := bruteForce(levels, w, K)
		if res.MaxErr != wantE || res.SSE != wantSSE {
			t.Errorf("trial %d: levels=%v w=%v K=%d\n  Solve=(E=%d,SSE=%d) 暴力=(E=%d,SSE=%d)",
				trial, levels, w, K, res.MaxErr, res.SSE, wantE, wantSSE)
		}
		// 校验分组结构：组误差不超过全局误差、组数不超过 K
		for _, g := range res.Groups {
			if g.MaxErr > res.MaxErr {
				t.Errorf("trial %d: 组误差 %d 超过全局 %d", trial, g.MaxErr, res.MaxErr)
			}
		}
		if len(res.Groups) > K {
			t.Errorf("trial %d: 组数 %d 超过 K=%d", trial, len(res.Groups), K)
		}
	}
}

func TestSolveEdgeCases(t *testing.T) {
	// 空直方图
	if r := Solve(nil, nil, 3); len(r.Groups) != 0 {
		t.Errorf("空直方图应无分组, 得到 %v", r.Groups)
	}
	// 单一灰度级：误差为 0
	r := Solve([]int{128}, []int64{100}, 4)
	if r.MaxErr != 0 || r.SSE != 0 || len(r.Groups) != 1 || r.Groups[0].Rep != 128 {
		t.Errorf("单灰度级结果错误: %+v", r)
	}
	// K 大于灰度级数：每级一组，误差 0
	r = Solve([]int{10, 200}, []int64{3, 7}, 10)
	if r.MaxErr != 0 || r.SSE != 0 || len(r.Groups) != 2 {
		t.Errorf("K>=级数结果错误: %+v", r)
	}
	// 字典序检查：{0:1, 10:1} K=1 —— E=5, c=5, SSE=50
	r = Solve([]int{0, 10}, []int64{1, 1}, 1)
	if r.MaxErr != 5 || r.SSE != 50 || r.Groups[0].Rep != 5 {
		t.Errorf("字典序基本情形错误: %+v", r)
	}
	// 字典序优先于 SSE：{0:1, 9:1, 10:1} K=2
	// 方案A [0,9][10]: E=5(ceil(9/2)), SSE=... ; 方案B [0][9,10]: E=1
	// 最小 E=1，应选 B 而非 SSE 可能更小的其他方案
	r = Solve([]int{0, 9, 10}, []int64{1, 1, 1}, 2)
	if r.MaxErr != 1 {
		t.Errorf("应先最小化最大误差到 1, 得到 %d (%+v)", r.MaxErr, r)
	}
}
