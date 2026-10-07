package facility

import (
	"fmt"
	"math/bits"
	"sort"
)

// MaxRobustFacilities 是问题二支持的设施数量上限。
const MaxRobustFacilities = 12

// MaxScenarios 是问题二支持的情景数量上限。
const MaxScenarios = 5

// Scenario 描述一个覆盖情景:需求点收益不变,但覆盖关系随情景变化。
type Scenario struct {
	Name   string  // 情景名称
	Prob   float64 // 情景概率(用于期望费用)
	Group  int     // 观测组编号:同组情景的应急动作必须相同
	Covers [][]int // Covers[j] 为该情景下覆盖需求点 j 的设施集合
}

// RobustInstance 描述两阶段设施覆盖问题:
//   - 一阶段:在整数预算内开启一组设施(祖先闭包),决策先于情景揭晓;
//   - 二阶段:情景揭晓后,仅能在已开祖先下启用至多 EmergLimit 个应急设施,
//     同一观测组内的情景必须采取相同的应急动作;
//   - 目标:先最大化最坏情景覆盖收益,再最小化期望总费用;
//   - 约束:每个情景下 一阶段费用+应急费用 <= Budget。
type RobustInstance struct {
	Costs      []int64    // 一阶段开启费用
	EmergCosts []int64    // 应急启用费用
	Parents    []int      // 父设施,-1 表示根
	Profits    []int64    // 需求点收益(各情景相同)
	Budget     int64      // 每个情景的总费用预算
	EmergLimit int        // 每个情景最多启用的应急设施数
	Scenarios  []Scenario // 至多 MaxScenarios 个情景
}

// Policy 是问题二的解(策略树):根为一阶段开启集,每个观测组一个应急分支。
type Policy struct {
	Opened         []bool         // 一阶段开启集合(祖先闭包)
	Emergency      map[int][]bool // Emergency[g] 为观测组 g 的应急启用集合
	WorstProfit    int64          // 最坏情景覆盖收益
	ExpectedCost   float64        // 期望总费用(一阶段费用 + 期望应急费用)
	ScenarioProfit []int64        // 各情景覆盖收益
	ScenarioCost   []int64        // 各情景总费用(一阶段 + 应急)
}

// Validate 检查鲁棒实例数据的一致性。
func (in *RobustInstance) Validate() error {
	n := len(in.Costs)
	if n == 0 {
		return fmt.Errorf("facility: 至少需要一个设施")
	}
	if n > MaxRobustFacilities {
		return fmt.Errorf("facility: 设施数 %d 超过上限 %d", n, MaxRobustFacilities)
	}
	if len(in.Parents) != n || len(in.EmergCosts) != n {
		return fmt.Errorf("facility: Parents/EmergCosts 长度与设施数不一致")
	}
	base := &Instance{Costs: in.Costs, Parents: in.Parents, Profits: in.Profits, Budget: in.Budget}
	if err := base.Validate(); err != nil {
		return err
	}
	for i, c := range in.EmergCosts {
		if c < 0 {
			return fmt.Errorf("facility: 设施 %d 的应急费用为负", i)
		}
	}
	if in.EmergLimit < 0 {
		return fmt.Errorf("facility: 应急设施上限为负")
	}
	if len(in.Scenarios) == 0 {
		return fmt.Errorf("facility: 至少需要一个情景")
	}
	if len(in.Scenarios) > MaxScenarios {
		return fmt.Errorf("facility: 情景数 %d 超过上限 %d", len(in.Scenarios), MaxScenarios)
	}
	for s, sc := range in.Scenarios {
		if sc.Prob < 0 {
			return fmt.Errorf("facility: 情景 %d 概率为负", s)
		}
		if len(sc.Covers) != len(in.Profits) {
			return fmt.Errorf("facility: 情景 %d 的 Covers 长度与需求点数不一致", s)
		}
		for j, fs := range sc.Covers {
			for _, f := range fs {
				if f < 0 || f >= n {
					return fmt.Errorf("facility: 情景 %d 需求点 %d 的覆盖设施 %d 越界", s, j, f)
				}
			}
		}
	}
	return nil
}

// groupIDs 返回出现过的观测组编号(升序)。
func (in *RobustInstance) groupIDs() []int {
	set := make(map[int]bool)
	for _, sc := range in.Scenarios {
		set[sc.Group] = true
	}
	ids := make([]int, 0, len(set))
	for g := range set {
		ids = append(ids, g)
	}
	sort.Ints(ids)
	return ids
}

// scenarioCover 返回每个情景下每个设施的需求点覆盖位集。
func (in *RobustInstance) scenarioCover() [][]bitset {
	n := len(in.Costs)
	m := len(in.Profits)
	out := make([][]bitset, len(in.Scenarios))
	for s, sc := range in.Scenarios {
		out[s] = make([]bitset, n)
		for i := range out[s] {
			out[s][i] = newBitset(m)
		}
		for j, fs := range sc.Covers {
			for _, f := range fs {
				out[s][f].set(j)
			}
		}
	}
	return out
}

// SolveRobust 精确求解问题二。
//
// 方法:枚举一阶段闭包 F;对每个 F 枚举所有合法应急集 E(大小 <= EmergLimit、
// F∪E 闭包、逐情景预算可行),并按观测组汇总“组内最坏收益”。
// 对固定的 F,各观测组独立:组 g 可达到的最坏收益上界为 max_E m_g(E),
// 故 F 的最坏收益为 min_g max_E m_g(E);在达到全局最优最坏收益的 F 中,
// 选取期望费用最小者(组内取满足 m_g(E) >= t* 的最小应急费用)。
func (in *RobustInstance) SolveRobust() (*Policy, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	n := len(in.Costs)
	S := len(in.Scenarios)
	m := len(in.Profits)
	anc := ancestorMasks(in.Parents)
	cover := in.scenarioCover()
	groups := in.groupIDs()
	groupProb := make(map[int]float64)
	groupScen := make(map[int][]int)
	for s, sc := range in.Scenarios {
		groupProb[sc.Group] += sc.Prob
		groupScen[sc.Group] = append(groupScen[sc.Group], s)
	}

	// 一阶段闭包枚举(增量递推费用与祖先需求)。
	totalF := 1 << uint(n)
	costF := make([]int64, totalF)
	reqF := make([]uint64, totalF)
	coverF := make([][]bitset, totalF) // coverF[mask][s] 为一阶段并集
	for mask := 0; mask < totalF; mask++ {
		coverF[mask] = make([]bitset, S)
		for s := 0; s < S; s++ {
			coverF[mask][s] = newBitset(m)
		}
		if mask == 0 {
			continue
		}
		lsb := mask & (-mask)
		i := bits.TrailingZeros64(uint64(lsb))
		prev := mask ^ lsb
		costF[mask] = costF[prev] + in.Costs[i]
		reqF[mask] = reqF[prev] | anc[i]
		for s := 0; s < S; s++ {
			orInto(coverF[mask][s], coverF[prev][s])
			orInto(coverF[mask][s], cover[s][i])
		}
	}

	type groupInfo struct {
		bestM    int64   // 该组可达的最坏收益上界
		emergSet []int64 // 每个应急子集的费用(与枚举顺序一致)
		mOfE     []int64 // 每个应急子集的组内最坏收益
		emergID  []int   // 每个应急子集在候选序列中的编号(用于还原)
	}

	bestT := int64(-1)
	var bestExpCost float64
	var bestF int
	var bestChoice map[int]int // 组 -> 应急子集编号

	for fMask := 0; fMask < totalF; fMask++ {
		if reqF[fMask]&^uint64(fMask) != 0 || costF[fMask] > in.Budget {
			continue
		}
		// 候选应急设施:未在一阶段开启的设施。
		var cand []int
		for i := 0; i < n; i++ {
			if fMask>>uint(i)&1 == 0 {
				cand = append(cand, i)
			}
		}
		k := len(cand)
		totalE := 1 << uint(k)
		// 枚举应急子集:大小限制、闭包、逐情景预算。
		sizeE := make([]int, totalE)
		emergCostE := make([]int64, totalE)
		reqE := make([]uint64, totalE)
		profitE := make([][]int64, 0, totalE) // 仅保留合法子集
		var legalE []int
		for eMask := 0; eMask < totalE; eMask++ {
			if eMask > 0 {
				lsb := eMask & (-eMask)
				idx := bits.TrailingZeros64(uint64(lsb))
				prev := eMask ^ lsb
				sizeE[eMask] = sizeE[prev] + 1
				emergCostE[eMask] = emergCostE[prev] + in.EmergCosts[cand[idx]]
				reqE[eMask] = reqE[prev] | anc[cand[idx]]
			}
			if sizeE[eMask] > in.EmergLimit {
				continue
			}
			unionMask := uint64(fMask)
			for idx := 0; idx < k; idx++ {
				if eMask>>uint(idx)&1 == 1 {
					unionMask |= uint64(1) << uint(cand[idx])
				}
			}
			if reqE[eMask]&^unionMask != 0 {
				continue // F∪E 非闭包
			}
			if costF[fMask]+emergCostE[eMask] > in.Budget {
				continue // 逐情景预算(各情景相同)
			}
			profits := make([]int64, S)
			for s := 0; s < S; s++ {
				u := newBitset(m)
				orInto(u, coverF[fMask][s])
				for idx := 0; idx < k; idx++ {
					if eMask>>uint(idx)&1 == 1 {
						orInto(u, cover[s][cand[idx]])
					}
				}
				profits[s] = u.weightedCount(in.Profits)
			}
			legalE = append(legalE, eMask)
			profitE = append(profitE, profits)
		}
		if len(legalE) == 0 {
			continue
		}
		// 按观测组汇总。
		gInfos := make(map[int]*groupInfo)
		feasible := true
		fWorst := int64(-1)
		for _, g := range groups {
			gi := &groupInfo{bestM: -1}
			for pos, eMask := range legalE {
				mg := int64(-1)
				for _, s := range groupScen[g] {
					if mg == -1 || profitE[pos][s] < mg {
						mg = profitE[pos][s]
					}
				}
				gi.emergSet = append(gi.emergSet, emergCostE[eMask])
				gi.mOfE = append(gi.mOfE, mg)
				gi.emergID = append(gi.emergID, eMask)
				if mg > gi.bestM {
					gi.bestM = mg
				}
			}
			if gi.bestM == -1 {
				feasible = false
				break
			}
			gInfos[g] = gi
			if fWorst == -1 || gi.bestM < fWorst {
				fWorst = gi.bestM
			}
		}
		if !feasible {
			continue
		}
		if fWorst < bestT {
			continue
		}
		// 该 F 在阈值 fWorst 下的最小期望费用。
		expCost := float64(costF[fMask])
		choice := make(map[int]int)
		for _, g := range groups {
			gi := gInfos[g]
			minCost := int64(-1)
			minPos := -1
			for pos := range gi.emergID {
				if gi.mOfE[pos] >= fWorst && (minCost == -1 || gi.emergSet[pos] < minCost) {
					minCost = gi.emergSet[pos]
					minPos = pos
				}
			}
			expCost += groupProb[g] * float64(minCost)
			choice[g] = gi.emergID[minPos]
		}
		if fWorst > bestT || bestT == -1 || expCost < bestExpCost {
			bestT = fWorst
			bestExpCost = expCost
			bestF = fMask
			bestChoice = choice
		}
	}
	if bestT == -1 {
		return nil, fmt.Errorf("facility: 不存在可行策略")
	}

	// 还原策略。
	pol := &Policy{
		Opened:         make([]bool, n),
		Emergency:      make(map[int][]bool),
		WorstProfit:    bestT,
		ExpectedCost:   bestExpCost,
		ScenarioProfit: make([]int64, S),
		ScenarioCost:   make([]int64, S),
	}
	for i := 0; i < n; i++ {
		pol.Opened[i] = bestF>>uint(i)&1 == 1
	}
	var stageCost int64
	for i := 0; i < n; i++ {
		if pol.Opened[i] {
			stageCost += in.Costs[i]
		}
	}
	for g, eMask := range bestChoice {
		emerg := make([]bool, n)
		var cand []int
		for i := 0; i < n; i++ {
			if bestF>>uint(i)&1 == 0 {
				cand = append(cand, i)
			}
		}
		for idx, f := range cand {
			if eMask>>uint(idx)&1 == 1 {
				emerg[f] = true
			}
		}
		pol.Emergency[g] = emerg
	}
	// 逐情景结算。
	for s, sc := range in.Scenarios {
		emerg := pol.Emergency[sc.Group]
		var total int64 = stageCost
		u := newBitset(m)
		for i := 0; i < n; i++ {
			if pol.Opened[i] || emerg[i] {
				if emerg[i] && !pol.Opened[i] {
					total += in.EmergCosts[i]
				}
				orInto(u, cover[s][i])
			}
		}
		pol.ScenarioCost[s] = total
		pol.ScenarioProfit[s] = u.weightedCount(in.Profits)
	}
	return pol, nil
}
