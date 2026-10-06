package scheduler

import "fmt"

// VerifyPolicy 逐路径回放策略树，验证：
//  1. 阶段一选择本身合法（时间回放）；
//  2. 每条情景路径都能走通：观测键在策略树中有对应分支，
//     即策略不依赖未观测到的信息（不能预知未来）；
//  3. 阶段一与阶段二的并集满足跨阶段的冷却与互斥约束，
//     且同一实验不重复执行；
//  4. 观测相同的情景必然得到相同的阶段二决策（非预知性）；
//  5. 回放得到的最坏收益与 pol.Value 一致。
func VerifyPolicy(p TwoStageProblem, pol Policy) error {
	n := len(p.Candidates)
	for _, i := range pol.Stage1 {
		if i < 0 || i >= n {
			return fmt.Errorf("阶段一包含非法下标 %d", i)
		}
	}
	if !ReplayLegal(p.Candidates, p.Cooldown, pol.Stage1) {
		return fmt.Errorf("阶段一选择违反冷却或互斥约束")
	}
	inStage1 := make(map[int]bool)
	for _, i := range pol.Stage1 {
		inStage1[i] = true
	}

	// 非预知性：观测键 -> 阶段二决策必须唯一（map 结构保证，
	// 这里显式回放每条情景路径确认分支存在且一致）。
	type decision struct {
		key  string
		seen bool
	}
	worst := 0
	for s, sc := range p.Scenarios {
		key := obsKey(p, s, pol.Stage1)
		stage2, ok := pol.Stage2[key]
		if !ok {
			return fmt.Errorf("情景 %d 的观测 %q 在策略树中无对应分支：策略依赖了未观测信息", sc.ID, key)
		}
		for _, i := range stage2 {
			if i < 0 || i >= n {
				return fmt.Errorf("情景 %d 路径的阶段二包含非法下标 %d", sc.ID, i)
			}
			if inStage1[i] {
				return fmt.Errorf("情景 %d 路径重复执行实验 %d", sc.ID, p.Candidates[i].ID)
			}
		}
		union := append(append([]int(nil), pol.Stage1...), stage2...)
		if !ReplayLegal(p.Candidates, p.Cooldown, union) {
			return fmt.Errorf("情景 %d 路径违反跨阶段冷却或互斥约束", sc.ID)
		}
		total := 0
		for _, i := range union {
			total += sc.Profits[i]
		}
		if s == 0 || total < worst {
			worst = total
		}
	}
	// 检查观测相同的情景确实共享同一分支（逐对确认）。
	for a := 0; a < len(p.Scenarios); a++ {
		for b := a + 1; b < len(p.Scenarios); b++ {
			ka, kb := obsKey(p, a, pol.Stage1), obsKey(p, b, pol.Stage1)
			if ka == kb {
				da, db := pol.Stage2[ka], pol.Stage2[kb]
				if !equalInts(da, db) {
					return fmt.Errorf("情景 %d 与 %d 观测相同但阶段二决策不同：预知未来",
						p.Scenarios[a].ID, p.Scenarios[b].ID)
				}
			}
		}
	}
	if worst != pol.Value {
		return fmt.Errorf("回放最坏收益 %d 与策略声明值 %d 不一致", worst, pol.Value)
	}
	return nil
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
