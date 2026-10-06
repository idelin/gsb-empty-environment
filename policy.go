package scheduler

import (
	"fmt"
	"strconv"
	"strings"
)

// Scenario 描述一个情景下各候选实验的收益。
type Scenario struct {
	ID      int
	Profits []int // 与 TwoStageProblem.Candidates 下标对齐
}

// TwoStageProblem 是两阶段带观测的实验计划问题。
//
// 阶段一在观测前选择一批实验执行；执行后观测到它们在所选情景下的
// 实际收益，据此把情景划分为可区分的观测组；阶段二只能依据观测组
// （而非具体情景）选择剩余实验。冷却与互斥约束跨阶段延续，
// 同一实验不能被执行两次。目标：最大化最坏情景下的总收益。
type TwoStageProblem struct {
	Candidates []Experiment // Profit 字段忽略，收益取自 Scenario
	Cooldown   int
	Scenarios  []Scenario
}

// Policy 是一棵两阶段策略树：根为阶段一选择，每个观测键对应一个阶段二选择。
type Policy struct {
	Stage1 []int            // 阶段一执行的候选下标
	Stage2 map[string][]int // 观测键 -> 阶段二执行的候选下标
	Value  int              // 最坏情景总收益
}

// obsKey 由阶段一选中实验在情景 s 下的收益向量构成观测键；
// 收益向量相同的情景在此观测下不可区分。
func obsKey(p TwoStageProblem, s int, stage1 []int) string {
	parts := make([]string, len(stage1))
	for k, i := range stage1 {
		parts[k] = strconv.Itoa(p.Scenarios[s].Profits[i])
	}
	return strings.Join(parts, ",")
}

// SolveTwoStage 枚举策略树，返回最坏收益最大的策略。
// 规模上限：10 候选、6 情景。并列时先选总实验数更少者，
// 再按全部选中实验排序后的 id 列表字典序更小者。
func SolveTwoStage(p TwoStageProblem) Policy {
	n := len(p.Candidates)
	if n > 10 {
		panic(fmt.Sprintf("SolveTwoStage: 候选数 %d 超过上限 10", n))
	}
	if len(p.Scenarios) > 6 {
		panic(fmt.Sprintf("SolveTwoStage: 情景数 %d 超过上限 6", len(p.Scenarios)))
	}
	for _, sc := range p.Scenarios {
		if len(sc.Profits) != n {
			panic("SolveTwoStage: 情景收益长度与候选数不一致")
		}
	}
	nmask := 1 << uint(n)

	// 预计算每个掩码的（情景无关）结构合法性。
	legal := make([]bool, nmask)
	for m := 0; m < nmask; m++ {
		legal[m] = ReplayLegal(p.Candidates, p.Cooldown, maskIndices(m, n))
	}
	// 预计算 profit[情景][掩码]。
	profit := make([][]int, len(p.Scenarios))
	for s := range p.Scenarios {
		profit[s] = make([]int, nmask)
		for m := 1; m < nmask; m++ {
			low := m & (-m)
			bit := 0
			for v := low; v > 1; v >>= 1 {
				bit++
			}
			profit[s][m] = profit[s][m^low] + p.Scenarios[s].Profits[bit]
		}
	}

	best := Policy{Stage2: make(map[string][]int)}
	bestValue := 0
	bestCount := 0
	bestIDs := []int(nil) // 全空策略：收益 0，永远合法
	first := true

	for m1 := 0; m1 < nmask; m1++ {
		if !legal[m1] {
			continue
		}
		stage1 := maskIndices(m1, n)
		// 按观测键划分情景。
		groups := make(map[string][]int)
		var keys []string
		for s := range p.Scenarios {
			k := obsKey(p, s, stage1)
			if _, ok := groups[k]; !ok {
				keys = append(keys, k)
			}
			groups[k] = append(groups[k], s)
		}
		// 对每个观测组独立选最优阶段二子集。
		stage2 := make(map[string][]int)
		worst := 0
		totalCount := popcount(m1)
		unionMask := m1
		for gi, k := range keys {
			gs := groups[k]
			gBest, gVal := bestStage2(p, n, m1, legal, profit, gs)
			stage2[k] = gBest
			if gi == 0 || gVal < worst {
				worst = gVal
			}
			totalCount += len(gBest)
			for _, i := range gBest {
				unionMask |= 1 << uint(i)
			}
		}
		ids := sortedIDs(p.Candidates, maskIndices(unionMask, n))
		if first || better(worst, totalCount, ids, bestValue, bestCount, bestIDs) {
			first = false
			bestValue, bestCount, bestIDs = worst, totalCount, ids
			best = Policy{Stage1: stage1, Stage2: stage2, Value: worst}
		}
	}
	return best
}

// bestStage2 在给定阶段一掩码 m1 与观测组 gs 下，枚举阶段二子集，
// 最大化组内最坏总收益；并列先少后按 id 字典序。
func bestStage2(p TwoStageProblem, n, m1 int, legal []bool, profit [][]int, gs []int) ([]int, int) {
	nmask := 1 << uint(n)
	bestSel := []int(nil)
	bestVal := 0
	bestIDs := []int(nil)
	first := true
	for m2 := 0; m2 < nmask; m2++ {
		if m2&m1 != 0 || !legal[m1|m2] {
			continue
		}
		val := 0
		for gi, s := range gs {
			v := profit[s][m1] + profit[s][m2]
			if gi == 0 || v < val {
				val = v
			}
		}
		sel := maskIndices(m2, n)
		ids := sortedIDs(p.Candidates, sel)
		if first || better(val, len(sel), ids, bestVal, len(bestSel), bestIDs) {
			first = false
			bestVal, bestSel, bestIDs = val, sel, ids
		}
	}
	return bestSel, bestVal
}

func popcount(m int) int {
	c := 0
	for m != 0 {
		m &= m - 1
		c++
	}
	return c
}
