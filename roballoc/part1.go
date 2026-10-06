package main

import "fmt"

// ---------- 第一部分数据 ----------
// 10 件物品、3 个仓位、4 个等权场景。分配在场景揭晓前固定，且必须在
// 每个场景下都满足仓位容量。费用 = 物品体积 x 仓位费率（按场景体积计）。
// 目标：先最小化最坏场景费用，再最小化平均场景费用（等权 => 最小化费用和）。

const p1Items = 10
const p1Bins = 3

var p1Cap = [p1Bins]int{34, 34, 34}
var p1Rate = [p1Bins]int{1, 2, 3}

var p1Scen = []string{"S1", "S2", "S3", "S4"}

// p1Vol[s][i] 为场景 s 下物品 i 的体积
var p1Vol = [][]int{
	{8, 6, 9, 7, 10, 6, 8, 7, 9, 5},
	{9, 7, 8, 8, 11, 7, 9, 6, 10, 6},
	{7, 8, 10, 6, 9, 8, 7, 9, 8, 7},
	{10, 6, 8, 9, 8, 7, 10, 8, 7, 8},
}

func p1Loads(assign []int) [][p1Bins]int {
	loads := make([][p1Bins]int, len(p1Vol))
	for i, b := range assign {
		for s := range p1Vol {
			loads[s][b] += p1Vol[s][i]
		}
	}
	return loads
}

func p1Costs(assign []int) []int {
	costs := make([]int, len(p1Vol))
	for i, b := range assign {
		for s := range p1Vol {
			costs[s] += p1Vol[s][i] * p1Rate[b]
		}
	}
	return costs
}

func p1Feasible(assign []int) bool {
	loads := p1Loads(assign)
	for s := range loads {
		for b := 0; b < p1Bins; b++ {
			if loads[s][b] > p1Cap[b] {
				return false
			}
		}
	}
	return true
}

func worstAndSum(costs []int) (int, int) {
	w, sum := 0, 0
	for _, c := range costs {
		if c > w {
			w = c
		}
		sum += c
	}
	return w, sum
}

// ---------- 求解器：分支定界（同一分配跨全部场景） ----------

type p1Solver struct {
	assign    []int
	loads     [][p1Bins]int
	costs     []int
	suffix    [][]int // suffix[s][i]：物品 i..n-1 在场景 s 的最小追加费用下界
	bestWorst int
	bestSum   int
	best      []int
	nodes     int
}

func newP1Solver() *p1Solver {
	n := len(p1Vol)
	sv := &p1Solver{
		assign:    make([]int, p1Items),
		loads:     make([][p1Bins]int, n),
		costs:     make([]int, n),
		suffix:    make([][]int, n),
		bestWorst: maxInt,
		bestSum:   maxInt,
	}
	minRate := p1Rate[0]
	for b := 1; b < p1Bins; b++ {
		if p1Rate[b] < minRate {
			minRate = p1Rate[b]
		}
	}
	for s := 0; s < n; s++ {
		sv.suffix[s] = make([]int, p1Items+1)
		for i := p1Items - 1; i >= 0; i-- {
			sv.suffix[s][i] = sv.suffix[s][i+1] + p1Vol[s][i]*minRate
		}
	}
	return sv
}

func (sv *p1Solver) dfs(i int) {
	sv.nodes++
	if i == p1Items {
		w, sum := worstAndSum(sv.costs)
		if w < sv.bestWorst || (w == sv.bestWorst && sum < sv.bestSum) {
			sv.bestWorst, sv.bestSum = w, sum
			sv.best = append([]int(nil), sv.assign...)
		}
		return
	}
	for b := 0; b < p1Bins; b++ {
		ok := true
		for s := range p1Vol {
			if sv.loads[s][b]+p1Vol[s][i] > p1Cap[b] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		for s := range p1Vol {
			sv.loads[s][b] += p1Vol[s][i]
			sv.costs[s] += p1Vol[s][i] * p1Rate[b]
		}
		sv.assign[i] = b
		lbW, lbSum := 0, 0
		for s := range p1Vol {
			c := sv.costs[s] + sv.suffix[s][i+1]
			if c > lbW {
				lbW = c
			}
			lbSum += c
		}
		if lbW < sv.bestWorst || (lbW == sv.bestWorst && lbSum < sv.bestSum) {
			sv.dfs(i + 1)
		}
		for s := range p1Vol {
			sv.loads[s][b] -= p1Vol[s][i]
			sv.costs[s] -= p1Vol[s][i] * p1Rate[b]
		}
	}
}

// ---------- 核对：三进制枚举全部 3^10 个分配 ----------

func p1Enumerate() (bestWorst, bestSum, feasibleCount, total int) {
	bestWorst, bestSum = maxInt, maxInt
	total = 1
	for i := 0; i < p1Items; i++ {
		total *= p1Bins
	}
	assign := make([]int, p1Items)
	for t := 0; t < total; t++ {
		x := t
		for i := 0; i < p1Items; i++ {
			assign[i] = x % p1Bins
			x /= p1Bins
		}
		if !p1Feasible(assign) {
			continue
		}
		feasibleCount++
		w, sum := worstAndSum(p1Costs(assign))
		if w < bestWorst || (w == bestWorst && sum < bestSum) {
			bestWorst, bestSum = w, sum
		}
	}
	return
}

// 参考下界：若（违规地）允许按场景分别分配，各场景单独的最优费用。
func p1SingleScenarioOpt(s int) int {
	best := maxInt
	total := 1
	for i := 0; i < p1Items; i++ {
		total *= p1Bins
	}
	assign := make([]int, p1Items)
	for t := 0; t < total; t++ {
		x := t
		load := [p1Bins]int{}
		cost := 0
		ok := true
		for i := 0; i < p1Items; i++ {
			b := x % p1Bins
			x /= p1Bins
			assign[i] = b
			load[b] += p1Vol[s][i]
			cost += p1Vol[s][i] * p1Rate[b]
		}
		for b := 0; b < p1Bins; b++ {
			if load[b] > p1Cap[b] {
				ok = false
				break
			}
		}
		if ok && cost < best {
			best = cost
		}
	}
	return best
}

func binName(b int) string {
	return string(rune('A' + b))
}

func printAssign(assign []int) {
	for i, b := range assign {
		fmt.Printf("  物品%2d -> 仓位%s\n", i+1, binName(b))
	}
}

func runPart1() {
	fmt.Printf("仓位容量: A=%d B=%d C=%d；费率(每单位体积): A=%d B=%d C=%d\n",
		p1Cap[0], p1Cap[1], p1Cap[2], p1Rate[0], p1Rate[1], p1Rate[2])
	fmt.Println("各场景物品体积:")
	fmt.Print("        ")
	for i := 0; i < p1Items; i++ {
		fmt.Printf("物品%-4d", i+1)
	}
	fmt.Println(" 合计")
	for s, name := range p1Scen {
		fmt.Printf("  %-5s ", name)
		sum := 0
		for i := 0; i < p1Items; i++ {
			fmt.Printf("%-7d", p1Vol[s][i])
			sum += p1Vol[s][i]
		}
		fmt.Printf(" %d\n", sum)
	}

	sv := newP1Solver()
	sv.dfs(0)
	if sv.best == nil {
		fmt.Println("\n无可行分配。")
		return
	}
	costs := p1Costs(sv.best)
	fmt.Printf("\n[求解器] 分支定界访问节点 %d 个\n", sv.nodes)
	fmt.Printf("最优固定分配（最坏费用 %d，平均费用 %.2f）:\n",
		sv.bestWorst, float64(sv.bestSum)/float64(len(p1Vol)))
	printAssign(sv.best)

	fmt.Println("\n[逐场景容量证据] 仓位负载 vs 容量:")
	loads := p1Loads(sv.best)
	for s, name := range p1Scen {
		fmt.Printf("  %s: ", name)
		for b := 0; b < p1Bins; b++ {
			mark := "OK"
			if loads[s][b] > p1Cap[b] {
				mark = "超载!"
			}
			fmt.Printf("%s=%2d/%-2d(%s)  ", binName(b), loads[s][b], p1Cap[b], mark)
		}
		fmt.Printf("费用=%d\n", costs[s])
	}
	w, sum := worstAndSum(costs)
	fmt.Printf("  最坏费用=%d  平均费用=%.2f\n", w, float64(sum)/float64(len(costs)))

	fmt.Println("\n[核对] 三进制枚举 3^10 = 59049 个分配（同一分配跨全部场景）:")
	eW, eS, feas, total := p1Enumerate()
	fmt.Printf("  枚举总数=%d，可行分配=%d，最优(最坏=%d, 费用和=%d)\n", total, feas, eW, eS)
	if eW == sv.bestWorst && eS == sv.bestSum {
		fmt.Println("  核对通过：分支定界与三进制枚举的最优值一致。PASS")
	} else {
		fmt.Printf("  核对失败：求解器(%d,%d) != 枚举(%d,%d)。FAIL\n",
			sv.bestWorst, sv.bestSum, eW, eS)
	}

	fmt.Println("\n[参考] 若违规地按场景分别分配，各场景单独最优费用（不可行的下界）:")
	for s, name := range p1Scen {
		fmt.Printf("  %s: 单独最优=%d\n", name, p1SingleScenarioOpt(s))
	}
	fmt.Println("  固定分配必须统一承担所有场景，故最坏费用不低于上述各值。")
}
