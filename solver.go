package main

import "math"

// Group 是一个灰度分组：覆盖 [Lo, Hi] 内的非空灰度级，调色板代表值为 Rep。
type Group struct {
	Lo, Hi int   // 组内非空灰度级的最小/最大值（含）
	Rep    int   // 整数代表值（调色板项）
	Count  int64 // 组内像素总数
	SSE    int64 // 组内加权平方误差
	MaxErr int   // 组内最大绝对误差
}

// Result 为字典序最优解：先最小化最大绝对误差，再最小化加权平方误差。
type Result struct {
	MaxErr int // 全体像素的最大绝对误差（已最小化）
	SSE    int64
	Groups []Group
}

// minGroups 用直径不超过 twoE 的区间贪心覆盖所有非空灰度级，返回最少组数。
// 直线上定点覆盖的贪心是最优的。
func minGroups(levels []int, twoE int) int {
	n := len(levels)
	cnt := 0
	for i := 0; i < n; {
		cnt++
		limit := levels[i] + twoE
		for i < n && levels[i] <= limit {
			i++
		}
	}
	return cnt
}

// groupCost 计算覆盖 levels[i..j] 的一组的整数最优代表值与加权平方误差，
// 约束代表值 c 满足 |v-c| <= E 对组内所有 v 成立，即 c ∈ [levels[j]-E, levels[i]+E]。
// 无约束最优为加权均值四舍五入，再夹取到可行区间。
func groupCost(levels []int, w []int64, i, j, E int) (rep int, sse int64) {
	var num, den int64
	for t := i; t <= j; t++ {
		num += int64(levels[t]) * w[t]
		den += w[t]
	}
	c := (2*num + den) / (2 * den) // 四舍五入（半数向上）
	lo := int64(levels[j] - E)
	hi := int64(levels[i] + E)
	if c < lo {
		c = lo
	}
	if c > hi {
		c = hi
	}
	rep = int(c)
	for t := i; t <= j; t++ {
		d := int64(levels[t]) - c
		sse += d * d * w[t]
	}
	return rep, sse
}

// Solve 对非空灰度级 levels（升序）及其权重 w 求 K 组字典序最优量化：
// 第一阶段二分最小可行最大误差 E；第二阶段在组直径 <= 2E 约束下用精确 DP 最小化加权 SSE。
func Solve(levels []int, w []int64, K int) Result {
	n := len(levels)
	if n == 0 || K <= 0 {
		return Result{}
	}
	if K > n {
		K = n
	}

	// 第一阶段：最小化最大绝对误差 E
	lo, hi := 0, levels[n-1]-levels[0]+1
	for lo < hi {
		mid := (lo + hi) / 2
		if minGroups(levels, 2*mid) <= K {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	E := lo

	// 第二阶段：在最大误差 <= E 约束下精确 DP 最小化加权 SSE
	const INF = math.MaxInt64 / 4
	dp := make([][]int64, n+1)
	parent := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int64, K+1)
		parent[i] = make([]int, K+1)
		for g := range dp[i] {
			dp[i][g] = INF
			parent[i][g] = -1
		}
	}
	dp[0][0] = 0
	for g := 1; g <= K; g++ {
		for j := 1; j <= n; j++ {
			// 组 (i, j-1] 可行当且仅当 levels[j-1]-levels[i] <= 2E
			for i := j - 1; i >= 0 && levels[j-1]-levels[i] <= 2*E; i-- {
				if dp[i][g-1] == INF {
					continue
				}
				_, c := groupCost(levels, w, i, j-1, E)
				if dp[i][g-1]+c < dp[j][g] {
					dp[j][g] = dp[i][g-1] + c
					parent[j][g] = i
				}
			}
		}
	}

	res := Result{SSE: dp[n][K]}
	// 回溯分组
	var bounds [][2]int
	for j, g := n, K; g > 0; g-- {
		i := parent[j][g]
		bounds = append(bounds, [2]int{i, j - 1})
		j = i
	}
	for t := len(bounds) - 1; t >= 0; t-- {
		i, j := bounds[t][0], bounds[t][1]
		rep, sse := groupCost(levels, w, i, j, E)
		grp := Group{Lo: levels[i], Hi: levels[j], Rep: rep, SSE: sse}
		for k := i; k <= j; k++ {
			grp.Count += w[k]
			d := levels[k] - rep
			if d < 0 {
				d = -d
			}
			if d > grp.MaxErr {
				grp.MaxErr = d
			}
		}
		if grp.MaxErr > res.MaxErr {
			res.MaxErr = grp.MaxErr
		}
		res.Groups = append(res.Groups, grp)
	}
	return res
}
