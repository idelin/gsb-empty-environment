package facility

import "fmt"

// MaxFacilities 是问题一支持的设施数量上限(子集穷举规模为 2^n)。
const MaxFacilities = 20

// Instance 描述确定性设施覆盖问题:
//   - 设施构成树(森林),开启子设施必须同时开启其全部祖先;
//   - 每个需求点被任意已开启设施覆盖即获得一次收益(重叠覆盖不重复计分);
//   - 在整数预算内先最大化覆盖收益,再最小化开启费用。
type Instance struct {
	Costs   []int64 // Costs[i] 为设施 i 的开启费用
	Parents []int   // Parents[i] 为设施 i 的父设施,-1 表示根
	Profits []int64 // Profits[j] 为需求点 j 的收益
	Covers  [][]int // Covers[j] 为覆盖需求点 j 的设施集合
	Budget  int64   // 整数预算(总开启费用上限)
}

// Solution 是问题一的解:开启集合(祖先闭包)及其收益与费用。
type Solution struct {
	Opened  []bool // Opened[i] 表示设施 i 是否开启
	Profit  int64  // 覆盖收益(每个需求点至多计一次)
	Cost    int64  // 总开启费用
	Covered []bool // Covered[j] 表示需求点 j 是否被覆盖
}

// Validate 检查实例数据的一致性。
func (in *Instance) Validate() error {
	n := len(in.Costs)
	if n == 0 {
		return fmt.Errorf("facility: 至少需要一个设施")
	}
	if n > MaxFacilities {
		return fmt.Errorf("facility: 设施数 %d 超过上限 %d", n, MaxFacilities)
	}
	if len(in.Parents) != n {
		return fmt.Errorf("facility: Parents 长度 %d 与设施数 %d 不一致", len(in.Parents), n)
	}
	for i, p := range in.Parents {
		if p < -1 || p >= n {
			return fmt.Errorf("facility: 设施 %d 的父节点 %d 越界", i, p)
		}
		if p == i {
			return fmt.Errorf("facility: 设施 %d 的父节点不能是自身", i)
		}
	}
	for i, c := range in.Costs {
		if c < 0 {
			return fmt.Errorf("facility: 设施 %d 的费用为负", i)
		}
	}
	if in.Budget < 0 {
		return fmt.Errorf("facility: 预算为负")
	}
	for j, f := range in.Profits {
		if f < 0 {
			return fmt.Errorf("facility: 需求点 %d 的收益为负", j)
		}
	}
	for j, fs := range in.Covers {
		for _, f := range fs {
			if f < 0 || f >= n {
				return fmt.Errorf("facility: 需求点 %d 的覆盖设施 %d 越界", j, f)
			}
		}
	}
	// 父链必须无环(森林)。
	for i := 0; i < n; i++ {
		seen := make([]bool, n)
		for p := in.Parents[i]; p != -1; p = in.Parents[p] {
			if seen[p] {
				return fmt.Errorf("facility: 父链存在环(设施 %d)", i)
			}
			seen[p] = true
		}
	}
	return nil
}

// ancestorMasks 返回每个设施的祖先掩码(含自身),用于闭包判定。
func ancestorMasks(parents []int) []uint64 {
	n := len(parents)
	masks := make([]uint64, n)
	for i := 0; i < n; i++ {
		m := uint64(1) << uint(i)
		for p := parents[i]; p != -1; p = parents[p] {
			m |= uint64(1) << uint(p)
		}
		masks[i] = m
	}
	return masks
}

// facilityCover 返回每个设施覆盖的需求点位集。
func facilityCover(in *Instance) []bitset {
	n := len(in.Costs)
	m := len(in.Profits)
	fac := make([]bitset, n)
	for i := range fac {
		fac[i] = newBitset(m)
	}
	for j, fs := range in.Covers {
		for _, f := range fs {
			fac[f].set(j)
		}
	}
	return fac
}

// Solve 精确求解问题一:枚举全部 2^n 个子集,过滤出满足祖先闭包与
// 预算约束的开启集,先最大化覆盖收益,再最小化开启费用。
func (in *Instance) Solve() (*Solution, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	n := len(in.Costs)
	anc := ancestorMasks(in.Parents)
	fac := facilityCover(in)
	m := len(in.Profits)

	total := 1 << uint(n)
	// 按最低位增量递推:需求点并集、祖先需求并集、费用。
	coverOf := make([]bitset, total)
	for i := range coverOf {
		coverOf[i] = newBitset(m)
	}
	reqOf := make([]uint64, total)
	costOf := make([]int64, total)

	// 空集恒可行(费用 0、闭包、收益 0),作为基准候选。
	bestMask := 0
	var bestProfit, bestCost int64
	for mask := 1; mask < total; mask++ {
		lsb := mask & (-mask)
		i := bitsIndex(lsb)
		prev := mask ^ lsb
		orInto(coverOf[mask], coverOf[prev])
		orInto(coverOf[mask], fac[i])
		reqOf[mask] = reqOf[prev] | anc[i]
		costOf[mask] = costOf[prev] + in.Costs[i]

		if reqOf[mask]&^uint64(mask) != 0 {
			continue // 违反祖先闭包
		}
		if costOf[mask] > in.Budget {
			continue // 超预算
		}
		profit := coverOf[mask].weightedCount(in.Profits)
		if profit > bestProfit || (profit == bestProfit && costOf[mask] < bestCost) {
			bestMask, bestProfit, bestCost = mask, profit, costOf[mask]
		}
	}
	return buildSolution(in, bestMask, coverOf[bestMask], costOf[bestMask]), nil
}

func bitsIndex(lsb int) int {
	i := 0
	for lsb > 1 {
		lsb >>= 1
		i++
	}
	return i
}

func buildSolution(in *Instance, mask int, cover bitset, cost int64) *Solution {
	n := len(in.Costs)
	m := len(in.Profits)
	sol := &Solution{
		Opened:  make([]bool, n),
		Cost:    cost,
		Covered: make([]bool, m),
	}
	for i := 0; i < n; i++ {
		sol.Opened[i] = mask>>uint(i)&1 == 1
	}
	for j := 0; j < m; j++ {
		sol.Covered[j] = cover.get(j)
	}
	sol.Profit = cover.weightedCount(in.Profits)
	return sol
}

// Verify 独立核对解的合法性:祖先闭包、预算,并逐需求点重新计分,
// 确认重叠覆盖不被重复计分(收益 = 被覆盖需求点收益之和,而非
// “设施-需求点”覆盖关系数加权和)。
func (in *Instance) Verify(sol *Solution) error {
	n := len(in.Costs)
	if len(sol.Opened) != n {
		return fmt.Errorf("facility: Opened 长度 %d 与设施数 %d 不一致", len(sol.Opened), n)
	}
	// 祖先闭包:开启子设施必须同时开启祖先。
	for i := 0; i < n; i++ {
		if !sol.Opened[i] {
			continue
		}
		for p := in.Parents[i]; p != -1; p = in.Parents[p] {
			if !sol.Opened[p] {
				return fmt.Errorf("facility: 设施 %d 已开启但祖先 %d 未开启", i, p)
			}
		}
	}
	// 费用与预算。
	var cost int64
	for i := 0; i < n; i++ {
		if sol.Opened[i] {
			cost += in.Costs[i]
		}
	}
	if cost != sol.Cost {
		return fmt.Errorf("facility: 费用不符,声明 %d,实算 %d", sol.Cost, cost)
	}
	if cost > in.Budget {
		return fmt.Errorf("facility: 费用 %d 超预算 %d", cost, in.Budget)
	}
	// 逐需求点核对:覆盖计数 >=1 才计一次收益。
	var profit int64
	for j, fs := range in.Covers {
		cnt := 0
		for _, f := range fs {
			if sol.Opened[f] {
				cnt++
			}
		}
		covered := cnt > 0
		if covered != sol.Covered[j] {
			return fmt.Errorf("facility: 需求点 %d 覆盖标记不符", j)
		}
		if covered {
			profit += in.Profits[j] // 无论被几个设施覆盖,只计一次
		}
	}
	if profit != sol.Profit {
		return fmt.Errorf("facility: 收益不符,声明 %d,实算 %d", sol.Profit, profit)
	}
	return nil
}

// CrossCheck 用独立的朴素子集穷举复核 Solve 的最优性,
// 并确认每个需求点在所有枚举中均只计一次收益。
func (in *Instance) CrossCheck(sol *Solution) error {
	n := len(in.Costs)
	anc := ancestorMasks(in.Parents)
	bestProfit, bestCost := int64(-1), int64(0)
	total := 1 << uint(n)
	first := true
	for mask := 0; mask < total; mask++ {
		var cost int64
		var req uint64
		for i := 0; i < n; i++ {
			if mask>>uint(i)&1 == 1 {
				cost += in.Costs[i]
				req |= anc[i]
			}
		}
		if req&^uint64(mask) != 0 || cost > in.Budget {
			continue
		}
		// 朴素逐点计分:每个需求点统计覆盖它的已开启设施数,>=1 计一次。
		var profit int64
		for j, fs := range in.Covers {
			cnt := 0
			for _, f := range fs {
				if mask>>uint(f)&1 == 1 {
					cnt++
				}
			}
			if cnt > 0 {
				profit += in.Profits[j]
			}
		}
		if first || profit > bestProfit || (profit == bestProfit && cost < bestCost) {
			bestProfit, bestCost = profit, cost
			first = false
		}
	}
	if sol.Profit != bestProfit {
		return fmt.Errorf("facility: 复核收益 %d 与求解结果 %d 不一致", bestProfit, sol.Profit)
	}
	if sol.Cost != bestCost {
		return fmt.Errorf("facility: 复核费用 %d 与求解结果 %d 不一致", bestCost, sol.Cost)
	}
	return nil
}
