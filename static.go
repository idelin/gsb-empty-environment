package main

import "fmt"

type staticResult struct {
	assign []int
	costs  []float64
	worst  float64
	avg    float64
	ok     bool
}

// 字典序比较：先最坏费用，再平均费用
func better(w1, a1, w2, a2 float64) bool {
	if w1 < w2-eps {
		return true
	}
	if w1 <= w2+eps && a1 < a2-eps {
		return true
	}
	return false
}

// 目标值并列时按分配向量的字典序取规范解，保证不同枚举顺序结果一致
func lexLess(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// 综合比较：候选 (w, avg, a) 是否优于当前最优
func betterStatic(w, avg float64, a []int, best staticResult) bool {
	if better(w, avg, best.worst, best.avg) {
		return true
	}
	if abs(w-best.worst) < eps && abs(avg-best.avg) < eps && lexLess(a, best.assign) {
		return true
	}
	return false
}

func (in *Instance) evalStatic(a []int) (worst, avg float64, costs []float64, ok bool) {
	if !in.fitsAll(a) {
		return 0, 0, nil, false
	}
	costs = make([]float64, len(in.Scenarios))
	sum := 0.0
	for s := range in.Scenarios {
		c := in.handleCost(a, s)
		costs[s] = c
		sum += c
		if c > worst {
			worst = c
		}
	}
	return worst, sum / float64(len(in.Scenarios)), costs, true
}

// 三进制计数枚举：idx 的第 i 位三进制数字即物品 i 的仓位
func (in *Instance) solveStaticTernary() (staticResult, int, int) {
	n := in.Items
	total := pow(in.Bins, n)
	var best staticResult
	feas := 0
	a := make([]int, n)
	for idx := 0; idx < total; idx++ {
		x := idx
		for i := 0; i < n; i++ {
			a[i] = x % in.Bins
			x /= in.Bins
		}
		worst, avg, costs, ok := in.evalStatic(a)
		if !ok {
			continue
		}
		feas++
		if !best.ok || betterStatic(worst, avg, a, best) {
			best.assign = append([]int(nil), a...)
			best.costs = costs
			best.worst = worst
			best.avg = avg
			best.ok = true
		}
	}
	return best, total, feas
}

// 独立递归枚举（核对用，不复用三进制计数逻辑）
func (in *Instance) solveStaticRecursive() staticResult {
	n := in.Items
	var best staticResult
	a := make([]int, n)
	var dfs func(i int)
	dfs = func(i int) {
		if i == n {
			worst, avg, costs, ok := in.evalStatic(a)
			if !ok {
				return
			}
			if !best.ok || betterStatic(worst, avg, a, best) {
				best.assign = append([]int(nil), a...)
				best.costs = costs
				best.worst = worst
				best.avg = avg
				best.ok = true
			}
			return
		}
		for b := 0; b < in.Bins; b++ {
			a[i] = b
			dfs(i + 1)
		}
	}
	dfs(0)
	return best
}

func runStatic(in *Instance) staticResult {
	fmt.Println("==================== 第一部分：固定分配（场景揭晓前确定，全场景共用同一分配） ====================")
	fmt.Printf("枚举空间: %d^%d = %d 种分配（三进制枚举；禁止按场景分别分配）\n", in.Bins, in.Items, pow(in.Bins, in.Items))
	best, total, feas := in.solveStaticTernary()
	if !best.ok {
		fmt.Println("无可行分配：任何固定分配都无法满足所有场景的容量约束。")
		return best
	}
	fmt.Printf("可行分配 %d / %d\n", feas, total)
	fmt.Printf("最优固定分配: %s\n", fmtAssign(best.assign))
	fmt.Printf("最坏搬运费: %.2f    平均搬运费: %.2f（场景等权）\n", best.worst, best.avg)
	fmt.Println("逐场景容量证据（负载/容量）:")
	for s, sc := range in.Scenarios {
		fmt.Printf("  场景 %-6s 费用 %8.2f |%s\n", sc.Name, best.costs[s], fmtLoads(in.loads(best.assign, s), in.caps(s)))
	}
	chk := in.solveStaticRecursive()
	if chk.ok && abs(chk.worst-best.worst) < eps && abs(chk.avg-best.avg) < eps && equalInts(chk.assign, best.assign) {
		fmt.Println("核对: 递归枚举与三进制计数枚举的最优值与最优解一致 ✓")
	} else {
		fmt.Printf("核对失败: 递归枚举 (%s, %.2f, %.2f) 与三进制枚举 (%s, %.2f, %.2f) 不一致 ✗\n",
			fmtAssign(chk.assign), chk.worst, chk.avg, fmtAssign(best.assign), best.worst, best.avg)
	}
	fmt.Println()
	return best
}
