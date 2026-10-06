package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// 观测组：观测物品体积完全相同的场景不可区分，必须采取相同搬仓动作
type cell struct {
	key  string
	scen []int
}

func (in *Instance) cells() []cell {
	idx := map[string]int{}
	var cells []cell
	for s := range in.Scenarios {
		parts := make([]string, len(in.Observe))
		for j, it := range in.Observe {
			parts[j] = strconv.FormatFloat(in.Scenarios[s].Volumes[it], 'f', -1, 64)
		}
		key := strings.Join(parts, "|")
		if k, ok := idx[key]; ok {
			cells[k].scen = append(cells[k].scen, s)
		} else {
			idx[key] = len(cells)
			cells = append(cells, cell{key: key, scen: []int{s}})
		}
	}
	return cells
}

func (in *Instance) cellLabel(cl cell) string {
	parts := make([]string, len(in.Observe))
	s := cl.scen[0]
	for j, it := range in.Observe {
		parts[j] = fmt.Sprintf("物品%d=%g", it, in.Scenarios[s].Volumes[it])
	}
	return strings.Join(parts, ",")
}

const maxMove = 10

// 一次搬仓：从 a0 出发，搬动 cnt 件物品（moved[k] 搬到 to[k]），总搬仓费 fee
type recourseOpt struct {
	moved [maxMove]int
	to    [maxMove]int
	cnt   int
	fee   float64
}

func (op recourseOpt) assignment(a0 []int) []int {
	r := make([]int, len(a0))
	copy(r, a0)
	for k := 0; k < op.cnt; k++ {
		r[op.moved[k]] = op.to[k]
	}
	return r
}

// 枚举满足共享上限（搬动数 ≤ MoveLimit 且搬仓费 ≤ MoveCostLimit）的全部搬仓方案；
// 写入调用方提供的缓冲区以避免热循环中的重复分配
func (in *Instance) recourseOptions(a0 []int, buf []recourseOpt) []recourseOpt {
	opts := buf[:0]
	var cur recourseOpt
	var dfs func(next int)
	dfs = func(next int) {
		opts = append(opts, cur)
		if cur.cnt >= in.MoveLimit {
			return
		}
		for j := next; j < in.Items; j++ {
			for b := 0; b < in.Bins; b++ {
				if b == a0[j] {
					continue
				}
				nf := cur.fee + in.MoveCost[j]
				if nf > in.MoveCostLimit+eps {
					continue
				}
				cur.moved[cur.cnt] = j
				cur.to[cur.cnt] = b
				oldFee := cur.fee
				cur.fee = nf
				cur.cnt++
				dfs(j + 1)
				cur.cnt--
				cur.fee = oldFee
			}
		}
	}
	dfs(0)
	return opts
}

// 快速容量检查（无堆分配）
func (in *Instance) fitsAllFast(a []int) bool {
	for s := range in.Scenarios {
		var l [3]float64
		v := in.Scenarios[s].Volumes
		for i, b := range a {
			l[b] += v[i]
		}
		cp := in.caps(s)
		for b := 0; b < in.Bins; b++ {
			if l[b] > cp[b]+eps {
				return false
			}
		}
	}
	return true
}

type branchResult struct {
	cl       cell
	opt      recourseOpt
	worst    float64
	scenCost []float64 // 与 cl.scen 对齐
}

type recourseResult struct {
	a0       []int
	branches []branchResult
	costs    []float64
	worst    float64
	avg      float64
	ok       bool
}

// 场景 s 在两阶段策略下的成本：按最终仓位计搬运费 + 搬仓费
// 增量计算：baseHandle 为 a0 下的搬运费，搬动物品改按新仓位计费
func (in *Instance) optCost(op recourseOpt, a0 []int, baseHandle float64, s int) float64 {
	c := baseHandle + op.fee
	v := in.Scenarios[s].Volumes
	for k := 0; k < op.cnt; k++ {
		i := op.moved[k]
		c += (in.Handle[i][op.to[k]] - in.Handle[i][a0[i]]) * v[i]
	}
	return c
}

// 给定 a0，为观测组 cl 选最优搬仓方案（最小化组内最坏成本，并列取组内平均最小）。
// scenCost 仅在最优方案确定后才分配，热路径不分配堆内存。
func (in *Instance) bestOptForCell(a0 []int, baseLoads [][]float64, baseHandle []float64, cl cell, opts []recourseOpt) (recourseOpt, float64, []float64, bool) {
	var bestOpt recourseOpt
	var bestCosts []float64
	bestWorst := 0.0
	bestAvg := 0.0
	found := false
	for oi := range opts {
		op := opts[oi]
		feas := true
		for _, s := range cl.scen {
			var l [3]float64
			copy(l[:], baseLoads[s])
			v := in.Scenarios[s].Volumes
			for k := 0; k < op.cnt; k++ {
				l[a0[op.moved[k]]] -= v[op.moved[k]]
				l[op.to[k]] += v[op.moved[k]]
			}
			cp := in.caps(s)
			for b := 0; b < in.Bins; b++ {
				if l[b] > cp[b]+eps {
					feas = false
					break
				}
			}
			if !feas {
				break
			}
		}
		if !feas {
			continue
		}
		sum := 0.0
		worst := 0.0
		for _, s := range cl.scen {
			c := in.optCost(op, a0, baseHandle[s], s)
			sum += c
			if c > worst {
				worst = c
			}
		}
		avg := sum / float64(len(cl.scen))
		if !found || better(worst, avg, bestWorst, bestAvg) {
			found = true
			bestOpt = op
			bestWorst = worst
			bestAvg = avg
			bestCosts = bestCosts[:0]
			for _, s := range cl.scen {
				bestCosts = append(bestCosts, in.optCost(op, a0, baseHandle[s], s))
			}
		}
	}
	return bestOpt, bestWorst, bestCosts, found
}

// 分解求解：给定 a0 时各观测组相互独立，逐组取最优即全局最优
func (in *Instance) solveRecourse() recourseResult {
	cls := in.cells()
	n := in.Items
	S := len(in.Scenarios)
	total := pow(in.Bins, n)
	var best recourseResult
	a0 := make([]int, n)
	baseHandle := make([]float64, S)
	baseLoads := make([][]float64, S)
	for s := 0; s < S; s++ {
		baseLoads[s] = make([]float64, in.Bins)
	}
	optBuf := make([]recourseOpt, 0, 2048)
	for idx := 0; idx < total; idx++ {
		x := idx
		for i := 0; i < n; i++ {
			a0[i] = x % in.Bins
			x /= in.Bins
		}
		if !in.fitsAllFast(a0) {
			continue
		}
		for s := 0; s < S; s++ {
			baseHandle[s] = in.handleCost(a0, s)
			l := baseLoads[s]
			for b := range l {
				l[b] = 0
			}
			for i, b := range a0 {
				l[b] += in.Scenarios[s].Volumes[i]
			}
		}
		opts := in.recourseOptions(a0, optBuf)
		costs := make([]float64, S)
		branches := make([]branchResult, len(cls))
		worst := 0.0
		feasible := true
		for ci, cl := range cls {
			op, w, sc, ok := in.bestOptForCell(a0, baseLoads, baseHandle, cl, opts)
			if !ok {
				feasible = false
				break
			}
			branches[ci] = branchResult{cl: cl, opt: op, worst: w, scenCost: sc}
			if w > worst {
				worst = w
			}
			for k, s := range cl.scen {
				costs[s] = sc[k]
			}
		}
		if !feasible {
			continue
		}
		sum := 0.0
		for _, c := range costs {
			sum += c
		}
		avg := sum / float64(S)
		if !best.ok || better(worst, avg, best.worst, best.avg) {
			best.a0 = append([]int(nil), a0...)
			best.branches = branches
			best.costs = costs
			best.worst = worst
			best.avg = avg
			best.ok = true
		}
	}
	return best
}

// 全策略枚举（核对用）：a0 × 各观测组搬仓方案的笛卡尔积，成本与容量全部从头计算
func (in *Instance) solveRecourseExhaustive() (recourseResult, int64) {
	cls := in.cells()
	n := in.Items
	S := len(in.Scenarios)
	total := pow(in.Bins, n)
	var best recourseResult
	var policies int64
	a0 := make([]int, n)
	optBuf := make([]recourseOpt, 0, 2048)
	for idx := 0; idx < total; idx++ {
		x := idx
		for i := 0; i < n; i++ {
			a0[i] = x % in.Bins
			x /= in.Bins
		}
		if !in.fitsAllFast(a0) {
			continue
		}
		opts := in.recourseOptions(a0, optBuf)
		perCell := make([][]recourseOpt, len(cls))
		okAll := true
		for ci, cl := range cls {
			for _, op := range opts {
				if in.fits(op.assignment(a0), cl.scen) {
					perCell[ci] = append(perCell[ci], op)
				}
			}
			if len(perCell[ci]) == 0 {
				okAll = false
				break
			}
		}
		if !okAll {
			continue
		}
		combo := make([]int, len(cls))
		var walk func(ci int)
		walk = func(ci int) {
			if ci == len(cls) {
				policies++
				costs := make([]float64, S)
				for cj, cl := range cls {
					op := perCell[cj][combo[cj]]
					final := op.assignment(a0)
					for _, s := range cl.scen {
						costs[s] = in.handleCost(final, s) + op.fee
					}
				}
				worst := 0.0
				sum := 0.0
				for _, c := range costs {
					if c > worst {
						worst = c
					}
					sum += c
				}
				avg := sum / float64(S)
				if !best.ok || better(worst, avg, best.worst, best.avg) {
					best.a0 = append([]int(nil), a0...)
					branches := make([]branchResult, len(cls))
					for cj, cl := range cls {
						op := perCell[cj][combo[cj]]
						w := 0.0
						sc := make([]float64, len(cl.scen))
						for k, s := range cl.scen {
							sc[k] = costs[s]
							if costs[s] > w {
								w = costs[s]
							}
						}
						branches[cj] = branchResult{cl: cl, opt: op, worst: w, scenCost: sc}
					}
					best.branches = branches
					best.costs = costs
					best.worst = worst
					best.avg = avg
					best.ok = true
				}
				return
			}
			for oi := range perCell[ci] {
				combo[ci] = oi
				walk(ci + 1)
			}
		}
		walk(0)
	}
	return best, policies
}

// 策略空间上界估计：3^n × (Σ_{j≤L} C(n,j)·2^j)^{观测组数}
func (in *Instance) estimatePolicies() float64 {
	n := in.Items
	opts := 0.0
	c := 1.0
	for j := 0; j <= in.MoveLimit && j <= n; j++ {
		if j > 0 {
			c = c * float64(n-j+1) / float64(j)
		}
		opts += c * math.Pow(2, float64(j))
	}
	return float64(pow(in.Bins, n)) * math.Pow(opts, float64(len(in.cells())))
}

func runRecourse(in *Instance, st staticResult) {
	fmt.Println("==================== 第二部分：观测部分物品体积后允许一次搬仓 ====================")
	fmt.Printf("观测分组: 揭晓前观测物品 %v 的体积；观测值相同的场景不可区分，不能选择不同动作（非预期性约束）\n", in.Observe)
	cls := in.cells()
	for ci, cl := range cls {
		names := []string{}
		for _, s := range cl.scen {
			names = append(names, in.Scenarios[s].Name)
		}
		fmt.Printf("  组%d（%s）: 场景 {%s}\n", ci, in.cellLabel(cl), strings.Join(names, ","))
	}
	fmt.Printf("共享上限: 搬动物品数 ≤ %d 且搬仓总费用 ≤ %.2f\n", in.MoveLimit, in.MoveCostLimit)
	res := in.solveRecourse()
	if !res.ok {
		fmt.Println("无可行两阶段策略。")
		return
	}
	fmt.Printf("第一阶段分配 a0: %s（揭晓前须满足所有场景容量）\n", fmtAssign(res.a0))
	fmt.Println("各分支搬仓方案与容量轨迹（负载/容量）:")
	for ci, br := range res.branches {
		moved := "不搬仓"
		if br.opt.cnt > 0 {
			parts := []string{}
			for k := 0; k < br.opt.cnt; k++ {
				i := br.opt.moved[k]
				parts = append(parts, fmt.Sprintf("物品%d:%s→%s", i, binName(res.a0[i]), binName(br.opt.to[k])))
			}
			moved = strings.Join(parts, " ")
		}
		fmt.Printf("  组%d（%s）: %s，搬仓费 %.2f，分支最坏成本 %.2f\n", ci, in.cellLabel(br.cl), moved, br.opt.fee, br.worst)
		fmt.Printf("    搬仓后分配: %s\n", fmtAssign(br.opt.assignment(res.a0)))
		for k, s := range br.cl.scen {
			l1 := in.loads(res.a0, s)
			l2 := in.loads(br.opt.assignment(res.a0), s)
			fmt.Printf("    场景 %-6s 成本 %8.2f | 阶段1%s → 阶段2%s\n",
				in.Scenarios[s].Name, br.scenCost[k], fmtLoads(l1, in.caps(s)), fmtLoads(l2, in.caps(s)))
		}
	}
	fmt.Printf("两阶段最坏成本: %.2f    平均成本: %.2f\n", res.worst, res.avg)
	if st.ok {
		fmt.Printf("对比静态固定分配最坏费用 %.2f：改善 %.2f（%.1f%%）\n", st.worst, st.worst-res.worst, 100*(st.worst-res.worst)/st.worst)
	}
	est := in.estimatePolicies()
	if est <= 5e7 {
		chk, policies := in.solveRecourseExhaustive()
		if chk.ok && abs(chk.worst-res.worst) < eps && abs(chk.avg-res.avg) < eps {
			fmt.Printf("核对: 全策略枚举共 %d 个策略，最优 (最坏 %.2f, 平均 %.2f) 与分解求解一致 ✓\n", policies, chk.worst, chk.avg)
		} else {
			fmt.Printf("核对失败: 全策略枚举 (%.2f, %.2f) 与分解求解 (%.2f, %.2f) 不一致 ✗\n", chk.worst, chk.avg, res.worst, res.avg)
		}
	} else {
		fmt.Printf("（本实例策略空间上界约 %.2e，全策略枚举核对请在小实例上运行）\n", est)
	}
	fmt.Println()
}
