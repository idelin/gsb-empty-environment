package main

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------- 第二部分数据 ----------
// 6 件物品、3 个仓位、3 个等权场景。第一阶段分配在场景揭晓前固定。
// 随后观测到“已观测物品”的真实体积（观测分组 = 已观测体积相同的场景归为一组），
// 每个分组允许一次搬仓：搬动件数与搬仓费用受共享上限约束，且同一分组内
// （即未观测部分不同的场景）必须采取同一搬仓动作。容量约束在场景完全
// 揭晓后对搬仓后的分配生效（搬仓正是为了纠偏第一阶段的超载）。
// 场景总成本 = 搬仓后仓位费率费用 + 搬仓费用。目标：最小化最坏场景总成本，
// 平局时最小化平均（费用和）。

const p2Items = 6
const p2Bins = 3

var p2Cap = [p2Bins]int{20, 20, 20}
var p2Rate = [p2Bins]int{1, 2, 3}
var p2MoveCost = [p2Items]int{3, 3, 3, 4, 4, 4}

const p2MaxMoves = 2   // 共享上限：最多搬动件数
const p2MoveBudget = 7 // 共享上限：搬仓费用预算

var p2Scen = []string{"T1", "T2", "T3"}

// p2Vol[s][i] 为场景 s 下物品 i 的体积。
// T1/T2 的已观测物品（1-3）体积相同 => 同组；T3 的已观测体积不同 => 独立成组。
// 该实例经搜索筛选：两阶段最优严格优于禁止搬仓的固定鲁棒分配。
var p2Vol = [][]int{
	{7, 6, 9, 8, 3, 6},
	{7, 6, 9, 7, 8, 7},
	{8, 7, 3, 8, 6, 8},
}

// 物品 1..3 的体积可观测，物品 4..6 不可观测
var p2Observed = []bool{true, true, true, false, false, false}

func p2CostFor(s int, assign []int) int {
	c := 0
	for i, b := range assign {
		c += p2Vol[s][i] * p2Rate[b]
	}
	return c
}

func p2LoadsFor(s int, assign []int) [p2Bins]int {
	var load [p2Bins]int
	for i, b := range assign {
		load[b] += p2Vol[s][i]
	}
	return load
}

func p2FeasibleFor(s int, assign []int) bool {
	load := p2LoadsFor(s, assign)
	for b := 0; b < p2Bins; b++ {
		if load[b] > p2Cap[b] {
			return false
		}
	}
	return true
}

// 观测分组：已观测物品体积完全相同的场景不可区分，必须同组同动作。
func p2Groups() ([][]int, []string) {
	index := map[string]int{}
	var groups [][]int
	var keys []string
	for s := range p2Vol {
		parts := make([]string, 0, p2Items)
		for i := 0; i < p2Items; i++ {
			if p2Observed[i] {
				parts = append(parts, strconv.Itoa(p2Vol[s][i]))
			}
		}
		key := strings.Join(parts, ",")
		g, ok := index[key]
		if !ok {
			g = len(groups)
			index[key] = g
			groups = append(groups, []int{})
			keys = append(keys, key)
		}
		groups[g] = append(groups[g], s)
	}
	return groups, keys
}

// ---------- 搬仓动作 ----------

type p2Recourse struct {
	assign   []int
	moves    int
	moveCost int
	worst    int // 组内 max(搬仓后费用 + 搬仓费)
	sum      int // 组内费用和
}

// 从第一阶段分配 x 出发、满足共享搬动上限且在组内全场景可行的全部搬仓动作。
func p2Recourses(x []int, group []int) []p2Recourse {
	total := 1
	for i := 0; i < p2Items; i++ {
		total *= p2Bins
	}
	y := make([]int, p2Items)
	var out []p2Recourse
	for t := 0; t < total; t++ {
		tt := t
		for i := 0; i < p2Items; i++ {
			y[i] = tt % p2Bins
			tt /= p2Bins
		}
		moves, mc := 0, 0
		for i := 0; i < p2Items; i++ {
			if y[i] != x[i] {
				moves++
				mc += p2MoveCost[i]
			}
		}
		if moves > p2MaxMoves || mc > p2MoveBudget {
			continue
		}
		ok := true
		for _, s := range group {
			if !p2FeasibleFor(s, y) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		worst, sum := 0, 0
		for _, s := range group {
			c := p2CostFor(s, y) + mc
			if c > worst {
				worst = c
			}
			sum += c
		}
		out = append(out, p2Recourse{
			assign:   append([]int(nil), y...),
			moves:    moves,
			moveCost: mc,
			worst:    worst,
			sum:      sum,
		})
	}
	return out
}

// ---------- 两阶段求解器 ----------

type p2Policy struct {
	x        []int
	recourse []p2Recourse // 每个观测分组一个搬仓动作
	worst    int
	sum      int
}

func p2FirstStageAssignments() [][]int {
	total := 1
	for i := 0; i < p2Items; i++ {
		total *= p2Bins
	}
	x := make([]int, p2Items)
	var out [][]int
	for t := 0; t < total; t++ {
		tt := t
		for i := 0; i < p2Items; i++ {
			x[i] = tt % p2Bins
			tt /= p2Bins
		}
		out = append(out, append([]int(nil), x...))
	}
	return out
}

// 基准：禁止搬仓时（等价于固定鲁棒分配）的最优最坏成本。
func p2NoRecourseBenchmark() (int, int) {
	bestW, bestS := maxInt, maxInt
	for _, x := range p2FirstStageAssignments() {
		feasible := true
		for s := range p2Vol {
			if !p2FeasibleFor(s, x) {
				feasible = false
				break
			}
		}
		if !feasible {
			continue
		}
		w, sum := 0, 0
		for s := range p2Vol {
			c := p2CostFor(s, x)
			if c > w {
				w = c
			}
			sum += c
		}
		if w < bestW || (w == bestW && sum < bestS) {
			bestW, bestS = w, sum
		}
	}
	return bestW, bestS
}

func p2Solve(groups [][]int) p2Policy {
	firsts := p2FirstStageAssignments()
	// 第一遍：最优最坏值 W*。给定 x，各分组独立取最优搬仓，策略最坏值 = 各组最坏值的最大。
	W := maxInt
	for _, x := range firsts {
		pw := 0
		for _, g := range groups {
			gw := maxInt
			for _, r := range p2Recourses(x, g) {
				if r.worst < gw {
					gw = r.worst
				}
			}
			if gw > pw {
				pw = gw
			}
		}
		if pw < W {
			W = pw
		}
	}
	// 第二遍：在最坏值不超过 W* 的约束下，逐组最小化费用和（即平均费用）。
	best := p2Policy{worst: W, sum: maxInt}
	for _, x := range firsts {
		recs := make([]p2Recourse, len(groups))
		sum := 0
		ok := true
		for gi, g := range groups {
			chosen := p2Recourse{}
			found := false
			bestSum := maxInt
			for _, r := range p2Recourses(x, g) {
				if r.worst <= W && r.sum < bestSum {
					bestSum = r.sum
					chosen = r
					found = true
				}
			}
			if !found {
				ok = false
				break
			}
			recs[gi] = chosen
			sum += bestSum
		}
		if ok && sum < best.sum {
			best.sum = sum
			best.x = append([]int(nil), x...)
			best.recourse = recs
		}
	}
	return best
}

// ---------- 核对：小实例全策略枚举 ----------
// 策略 = 第一阶段分配 x（任意）x 每个观测分组各选一个合法搬仓动作
// （搬仓后须在组内全场景可行；无法纠偏的 x 自动被排除）。
// 同一分组内的场景被迫共享动作，这正是“未观察的情景不能选择不同动作”。

func p2EnumeratePolicies(groups [][]int) (bestW, bestS int, count int64) {
	bestW, bestS = maxInt, maxInt
	for _, x := range p2FirstStageAssignments() {
		lists := make([][]p2Recourse, len(groups))
		empty := false
		for gi, g := range groups {
			lists[gi] = p2Recourses(x, g)
			if len(lists[gi]) == 0 {
				empty = true
				break
			}
		}
		if empty {
			continue // 该第一阶段分配无法被任何合法搬仓纠偏，不构成策略
		}
		var rec func(gi, w, s int)
		rec = func(gi, w, s int) {
			if gi == len(lists) {
				count++
				if w < bestW || (w == bestW && s < bestS) {
					bestW, bestS = w, s
				}
				return
			}
			for _, r := range lists[gi] {
				nw := w
				if r.worst > nw {
					nw = r.worst
				}
				rec(gi+1, nw, s+r.sum)
			}
		}
		rec(0, 0, 0)
	}
	return
}

func printP2Assign(prefix string, assign []int) {
	parts := make([]string, p2Items)
	for i, b := range assign {
		parts[i] = fmt.Sprintf("物品%d->%s", i+1, binName(b))
	}
	fmt.Printf("%s%s\n", prefix, strings.Join(parts, "  "))
}

func printTrajectory(name string, before, after [p2Bins]int) {
	fmt.Printf("    %s: ", name)
	for b := 0; b < p2Bins; b++ {
		m1, m2 := "OK", "OK"
		if before[b] > p2Cap[b] {
			m1 = "超载"
		}
		if after[b] > p2Cap[b] {
			m2 = "超载!"
		}
		fmt.Printf("%s %2d(%s)->%2d/%-2d(%s)  ", binName(b), before[b], m1, after[b], p2Cap[b], m2)
	}
	fmt.Println()
}

func runPart2() {
	fmt.Printf("仓位容量: A=%d B=%d C=%d；费率: A=%d B=%d C=%d\n",
		p2Cap[0], p2Cap[1], p2Cap[2], p2Rate[0], p2Rate[1], p2Rate[2])
	fmt.Printf("搬仓共享上限: 最多搬动 %d 件，搬仓费用预算 %d；单件搬仓费: %v\n",
		p2MaxMoves, p2MoveBudget, p2MoveCost)
	obs := []string{}
	for i, o := range p2Observed {
		if o {
			obs = append(obs, strconv.Itoa(i+1))
		}
	}
	fmt.Printf("可观测物品: {%s}；其余物品体积在搬仓决策时未知\n", strings.Join(obs, ","))
	fmt.Println("各场景物品体积:")
	for s, name := range p2Scen {
		fmt.Printf("  %s: %v\n", name, p2Vol[s])
	}

	groups, keys := p2Groups()
	fmt.Println("\n[观测分组] 已观测体积相同 => 不可区分 => 必须同一搬仓动作:")
	for gi, g := range groups {
		names := []string{}
		for _, s := range g {
			names = append(names, p2Scen[s])
		}
		fmt.Printf("  分支 G%d（观测到物品1-3体积=%s）: 场景 {%s}\n", gi+1, keys[gi], strings.Join(names, ","))
	}

	pol := p2Solve(groups)
	fmt.Printf("\n[求解器] 最优两阶段策略：最坏总成本=%d，平均总成本=%.2f\n",
		pol.worst, float64(pol.sum)/float64(len(p2Vol)))
	printP2Assign("  第一阶段分配: ", pol.x)
	for gi := range groups {
		r := pol.recourse[gi]
		fmt.Printf("  分支 G%d 搬仓: ", gi+1)
		if r.moves == 0 {
			fmt.Println("不搬动")
		} else {
			moved := []string{}
			for i := 0; i < p2Items; i++ {
				if r.assign[i] != pol.x[i] {
					moved = append(moved, fmt.Sprintf("物品%d %s->%s", i+1, binName(pol.x[i]), binName(r.assign[i])))
				}
			}
			fmt.Printf("%s（搬动 %d 件，搬仓费 %d）\n", strings.Join(moved, "，"), r.moves, r.moveCost)
		}
		printP2Assign("    搬仓后分配: ", r.assign)
	}

	fmt.Println("\n[容量轨迹] 各分支逐场景：阶段一负载 -> 搬仓后负载 vs 容量:")
	for gi, g := range groups {
		fmt.Printf("  分支 G%d:\n", gi+1)
		for _, s := range g {
			before := p2LoadsFor(s, pol.x)
			after := p2LoadsFor(s, pol.recourse[gi].assign)
			printTrajectory(p2Scen[s], before, after)
			fmt.Printf("         场景总成本（搬仓后费用+搬仓费）=%d\n",
				p2CostFor(s, pol.recourse[gi].assign)+pol.recourse[gi].moveCost)
		}
	}

	bW, bS := p2NoRecourseBenchmark()
	fmt.Printf("\n[基准] 禁止搬仓的固定鲁棒分配：最优最坏成本=%d，平均=%.2f\n",
		bW, float64(bS)/float64(len(p2Vol)))
	fmt.Printf("  两阶段策略相对基准的最坏成本改善: %d -> %d\n", bW, pol.worst)
	fmt.Println("\n[核对] 小实例全策略枚举（第一阶段 x 各分组搬仓动作的笛卡尔积）:")
	eW, eS, cnt := p2EnumeratePolicies(groups)
	fmt.Printf("  枚举策略总数=%d，最优(最坏=%d, 费用和=%d)\n", cnt, eW, eS)
	if eW == pol.worst && eS == pol.sum {
		fmt.Println("  核对通过：两阶段求解器与全策略枚举的最优值一致。PASS")
	} else {
		fmt.Printf("  核对失败：求解器(%d,%d) != 枚举(%d,%d)。FAIL\n", pol.worst, pol.sum, eW, eS)
	}
}
