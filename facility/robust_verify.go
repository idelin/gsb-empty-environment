package facility

import (
	"fmt"
	"strings"
)

// ScenarioCheck 是逐情景核对结果。
type ScenarioCheck struct {
	Scenario   int
	Name       string
	Group      int
	Profit     int64 // 该情景覆盖收益
	TotalCost  int64 // 一阶段费用 + 应急费用
	Budget     int64
	BudgetOK   bool // 总费用是否不超预算
	EmergCount int  // 启用的应急设施数
}

// VerifyPolicy 独立核对策略的合法性并重算目标值:
//   - 一阶段开启集为祖先闭包且不超预算;
//   - 每个情景:应急集与一阶段之并仍为闭包、应急数量不超限、
//     逐情景预算满足、同观测组应急动作一致;
//   - 重算最坏收益与期望费用并与策略声明值比对。
func (in *RobustInstance) VerifyPolicy(pol *Policy) error {
	n := len(in.Costs)
	if len(pol.Opened) != n {
		return fmt.Errorf("facility: Opened 长度 %d 与设施数 %d 不一致", len(pol.Opened), n)
	}
	// 一阶段闭包与预算。
	var stageCost int64
	for i := 0; i < n; i++ {
		if pol.Opened[i] {
			stageCost += in.Costs[i]
			for p := in.Parents[i]; p != -1; p = in.Parents[p] {
				if !pol.Opened[p] {
					return fmt.Errorf("facility: 一阶段设施 %d 已开启但祖先 %d 未开启", i, p)
				}
			}
		}
	}
	if stageCost > in.Budget {
		return fmt.Errorf("facility: 一阶段费用 %d 超预算 %d", stageCost, in.Budget)
	}
	// 同观测组应急动作一致(策略按组存储,天然满足;此处核对组编号覆盖)。
	for _, sc := range in.Scenarios {
		if _, ok := pol.Emergency[sc.Group]; !ok {
			return fmt.Errorf("facility: 观测组 %d 缺少应急动作", sc.Group)
		}
	}
	worst := int64(-1)
	expCost := float64(stageCost)
	for s, sc := range in.Scenarios {
		emerg := pol.Emergency[sc.Group]
		if len(emerg) != n {
			return fmt.Errorf("facility: 观测组 %d 应急集长度不符", sc.Group)
		}
		cnt := 0
		var emergCost int64
		for i := 0; i < n; i++ {
			if !emerg[i] {
				continue
			}
			if pol.Opened[i] {
				return fmt.Errorf("facility: 设施 %d 同时在一阶段与应急集", i)
			}
			cnt++
			emergCost += in.EmergCosts[i]
			// 应急设施的祖先必须已开(一阶段或同为应急)。
			for p := in.Parents[i]; p != -1; p = in.Parents[p] {
				if !pol.Opened[p] && !emerg[p] {
					return fmt.Errorf("facility: 情景 %d 应急设施 %d 的祖先 %d 未开启", s, i, p)
				}
			}
		}
		if cnt > in.EmergLimit {
			return fmt.Errorf("facility: 情景 %d 应急设施数 %d 超过上限 %d", s, cnt, in.EmergLimit)
		}
		if stageCost+emergCost > in.Budget {
			return fmt.Errorf("facility: 情景 %d 总费用 %d 超预算 %d", s, stageCost+emergCost, in.Budget)
		}
		// 逐需求点重算该情景收益(重叠覆盖只计一次)。
		var profit int64
		for j, fs := range sc.Covers {
			for _, f := range fs {
				if pol.Opened[f] || emerg[f] {
					profit += in.Profits[j]
					break
				}
			}
		}
		if profit != pol.ScenarioProfit[s] {
			return fmt.Errorf("facility: 情景 %d 收益不符,声明 %d,实算 %d", s, pol.ScenarioProfit[s], profit)
		}
		if stageCost+emergCost != pol.ScenarioCost[s] {
			return fmt.Errorf("facility: 情景 %d 费用不符,声明 %d,实算 %d", s, pol.ScenarioCost[s], stageCost+emergCost)
		}
		if worst == -1 || profit < worst {
			worst = profit
		}
		expCost += sc.Prob * float64(emergCost)
	}
	if worst != pol.WorstProfit {
		return fmt.Errorf("facility: 最坏收益不符,声明 %d,实算 %d", pol.WorstProfit, worst)
	}
	if expCost != pol.ExpectedCost {
		return fmt.Errorf("facility: 期望费用不符,声明 %v,实算 %v", pol.ExpectedCost, expCost)
	}
	return nil
}

// CheckScenarios 逐情景检查预算并返回核对表。
func (in *RobustInstance) CheckScenarios(pol *Policy) []ScenarioCheck {
	n := len(in.Costs)
	var stageCost int64
	for i := 0; i < n; i++ {
		if pol.Opened[i] {
			stageCost += in.Costs[i]
		}
	}
	out := make([]ScenarioCheck, len(in.Scenarios))
	for s, sc := range in.Scenarios {
		emerg := pol.Emergency[sc.Group]
		cnt := 0
		var emergCost int64
		for i := 0; i < n; i++ {
			if emerg[i] && !pol.Opened[i] {
				cnt++
				emergCost += in.EmergCosts[i]
			}
		}
		out[s] = ScenarioCheck{
			Scenario:   s,
			Name:       sc.Name,
			Group:      sc.Group,
			Profit:     pol.ScenarioProfit[s],
			TotalCost:  stageCost + emergCost,
			Budget:     in.Budget,
			BudgetOK:   stageCost+emergCost <= in.Budget,
			EmergCount: cnt,
		}
	}
	return out
}

// RenderPolicyTree 以树形文本输出策略:根为一阶段开启集,
// 每个观测组一个应急分支,叶为各情景的结算与预算核对。
func (in *RobustInstance) RenderPolicyTree(pol *Policy) string {
	var b strings.Builder
	n := len(in.Costs)
	var opened []int
	var stageCost int64
	for i := 0; i < n; i++ {
		if pol.Opened[i] {
			opened = append(opened, i)
			stageCost += in.Costs[i]
		}
	}
	fmt.Fprintf(&b, "一阶段开启 %v (费用 %d, 预算 %d)\n", opened, stageCost, in.Budget)
	checks := in.CheckScenarios(pol)
	groups := in.groupIDs()
	byGroup := make(map[int][]int)
	for s, sc := range in.Scenarios {
		byGroup[sc.Group] = append(byGroup[sc.Group], s)
	}
	for gi, g := range groups {
		lastG := gi == len(groups)-1
		prefix, child := "├─ ", "│  "
		if lastG {
			prefix, child = "└─ ", "   "
		}
		var emerg []int
		for i := 0; i < n; i++ {
			if pol.Emergency[g][i] && !pol.Opened[i] {
				emerg = append(emerg, i)
			}
		}
		var eCost int64
		for _, i := range emerg {
			eCost += in.EmergCosts[i]
		}
		fmt.Fprintf(&b, "%s观测组 %d: 应急启用 %v (应急费用 %d)\n", prefix, g, emerg, eCost)
		ss := byGroup[g]
		for si, s := range ss {
			lastS := si == len(ss)-1
			sprefix := "├─ "
			if lastS {
				sprefix = "└─ "
			}
			c := checks[s]
			mark := "OK"
			if !c.BudgetOK {
				mark = "超预算!"
			}
			fmt.Fprintf(&b, "%s%s情景 %s (p=%.3g): 收益 %d, 总费用 %d/%d %s\n",
				child, sprefix, c.Name, in.Scenarios[s].Prob, c.Profit, c.TotalCost, c.Budget, mark)
		}
	}
	fmt.Fprintf(&b, "最坏收益 %d, 期望费用 %.6g\n", pol.WorstProfit, pol.ExpectedCost)
	return b.String()
}

// CrossCheckRobust 用独立的暴力枚举复核 SolveRobust 的最优性:
// 直接枚举一阶段闭包 F 与各观测组应急集的全部组合,
// 按 (最坏收益降序... 即先最大最坏收益,再最小期望费用) 比较。
func (in *RobustInstance) CrossCheckRobust(pol *Policy) error {
	n := len(in.Costs)
	S := len(in.Scenarios)
	anc := ancestorMasks(in.Parents)
	cover := in.scenarioCover()
	m := len(in.Profits)
	groups := in.groupIDs()
	groupProb := make(map[int]float64)
	groupScen := make(map[int][]int)
	for s, sc := range in.Scenarios {
		groupProb[sc.Group] += sc.Prob
		groupScen[sc.Group] = append(groupScen[sc.Group], s)
	}

	bestT := int64(-1)
	bestExp := 0.0
	totalF := 1 << uint(n)
	for fMask := 0; fMask < totalF; fMask++ {
		var stageCost int64
		var req uint64
		for i := 0; i < n; i++ {
			if fMask>>uint(i)&1 == 1 {
				stageCost += in.Costs[i]
				req |= anc[i]
			}
		}
		if req&^uint64(fMask) != 0 || stageCost > in.Budget {
			continue
		}
		// 收集该 F 下全部合法应急子集(与组无关)。
		var cand []int
		for i := 0; i < n; i++ {
			if fMask>>uint(i)&1 == 0 {
				cand = append(cand, i)
			}
		}
		k := len(cand)
		type eOpt struct {
			cost    int64
			profits []int64
		}
		var opts []eOpt
		for eMask := 0; eMask < 1<<uint(k); eMask++ {
			if popcount(eMask) > in.EmergLimit {
				continue
			}
			var eCost int64
			var eReq uint64
			unionMask := uint64(fMask)
			for idx := 0; idx < k; idx++ {
				if eMask>>uint(idx)&1 == 1 {
					eCost += in.EmergCosts[cand[idx]]
					eReq |= anc[cand[idx]]
					unionMask |= uint64(1) << uint(cand[idx])
				}
			}
			if eReq&^unionMask != 0 || stageCost+eCost > in.Budget {
				continue
			}
			profits := make([]int64, S)
			for s := 0; s < S; s++ {
				u := newBitset(m)
				for i := 0; i < n; i++ {
					if unionMask>>uint(i)&1 == 1 {
						orInto(u, cover[s][i])
					}
				}
				profits[s] = u.weightedCount(in.Profits)
			}
			opts = append(opts, eOpt{cost: eCost, profits: profits})
		}
		if len(opts) == 0 {
			continue
		}
		// 组内最坏收益上界。
		fWorst := int64(-1)
		for _, g := range groups {
			gBest := int64(-1)
			for _, o := range opts {
				mg := int64(-1)
				for _, s := range groupScen[g] {
					if mg == -1 || o.profits[s] < mg {
						mg = o.profits[s]
					}
				}
				if mg > gBest {
					gBest = mg
				}
			}
			if fWorst == -1 || gBest < fWorst {
				fWorst = gBest
			}
		}
		if bestT != -1 && fWorst < bestT {
			continue
		}
		exp := float64(stageCost)
		for _, g := range groups {
			minC := int64(-1)
			for _, o := range opts {
				mg := int64(-1)
				for _, s := range groupScen[g] {
					if mg == -1 || o.profits[s] < mg {
						mg = o.profits[s]
					}
				}
				if mg >= fWorst && (minC == -1 || o.cost < minC) {
					minC = o.cost
				}
			}
			exp += groupProb[g] * float64(minC)
		}
		if fWorst > bestT || bestT == -1 || exp < bestExp {
			bestT, bestExp = fWorst, exp
		}
	}
	if pol.WorstProfit != bestT {
		return fmt.Errorf("facility: 暴力复核最坏收益 %d 与求解结果 %d 不一致", bestT, pol.WorstProfit)
	}
	if pol.ExpectedCost != bestExp {
		return fmt.Errorf("facility: 暴力复核期望费用 %v 与求解结果 %v 不一致", bestExp, pol.ExpectedCost)
	}
	return nil
}

func popcount(x int) int {
	c := 0
	for x != 0 {
		x &= x - 1
		c++
	}
	return c
}
