package adgame

import "sort"

// EnumerateInclusionMinimal 在候选状态集合上枚举关于集合包含关系的极小模型。
//
// 返回的每个状态 s 都满足 pred(s) == true, 且候选集合中不存在另一个
// 满足 pred 的状态 t 使 t 是 s 的真子集 (t ⊊ s)。候选状态与谓词都由
// 调用方提供 (验证器传入“可达状态”作为论域), 因此这里的极小模型是
// 闭式世界语义下、相对于可达论域的极小事实集见证。
//
// 状态空间至多 2^16, 算法按事实数升序扫描并用子掩码查询做极小性检查,
// 避免对谓词做指数级重复求值。
func EnumerateInclusionMinimal(candidates []State, pred func(State) bool) []State {
	if len(candidates) == 0 {
		return nil
	}
	// 直接以 16 位掩码做下标, 最多 65536 项。
	inCandidate := make([]bool, 1<<FactCount)
	minimal := make([]bool, 1<<FactCount)
	for _, s := range candidates {
		inCandidate[s] = true
	}

	ordered := append([]State(nil), candidates...)
	sort.Slice(ordered, func(i, j int) bool {
		ci, cj := ordered[i].Size(), ordered[j].Size()
		if ci != cj {
			return ci < cj
		}
		return ordered[i] < ordered[j]
	})

	var result []State
	for _, s := range ordered {
		if !pred(s) {
			continue
		}
		if hasMinimalSubset(s, inCandidate, minimal) {
			continue
		}
		minimal[s] = true
		result = append(result, s)
	}
	return result
}

// hasMinimalSubset 报告候选论域中 s 的某个真子掩码是否已是极小模型。
func hasMinimalSubset(s State, inCandidate, minimal []bool) bool {
	if s == 0 {
		return false
	}
	// 标准子掩码枚举: sub 依次取 s 的全部真子集 (含 0)。
	for sub := (s - 1) & s; ; sub = (sub - 1) & s {
		if inCandidate[sub] && minimal[sub] {
			return true
		}
		if sub == 0 {
			return false
		}
	}
}

// ClosureRule 是最小 Horn 闭包使用的单调推理规则: 当前状态包含 Require
// 时, Add 中的事实被单调推出。删除动作不参与单调闭包 (它们是非单调的)。
type ClosureRule struct {
	Require State
	Add     State
}

// LeastFactClosure 从种子状态出发, 反复应用全部单调规则直到不动点,
// 返回最小的闭包事实集 (单调 Horn 程序的最小模型)。
//
// 该闭包是真实可达状态的上近似: 任何只靠“增加事实”的攻击链能得到的
// 事实都在闭包中; 若闭包都不包含目标所需事实, 则任何攻守交互都无法
// 让攻方达成目标, 这是一条与极小极大求解相互独立的不可达证书。
func LeastFactClosure(seed State, rules []ClosureRule) State {
	closure := seed
	changed := true
	for changed {
		changed = false
		for _, r := range rules {
			if closure.Contains(r.Require) {
				next := closure | r.Add
				if next != closure {
					closure = next
					changed = true
				}
			}
		}
	}
	return closure
}
