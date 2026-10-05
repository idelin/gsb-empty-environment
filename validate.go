package planner

import "fmt"

// ValidateSequence 按给定顺序逐步校验前置状态：
// 每个操作的 Requires 必须由初始状态或之前操作的 Provides 满足。
// 这保证了「新增列 → 填充数据 → 建立约束」之类的顺序约束不会被违反：
// 任何错序的执行序列都会在校验阶段报错。
func ValidateSequence(ops []Op, preexisting []string) error {
	state := map[string]bool{}
	for _, s := range preexisting {
		state[s] = true
	}
	for _, op := range ops {
		for _, req := range op.Requires {
			if !state[req] {
				return fmt.Errorf("操作 %q 的前置状态 %q 在其执行前未满足（顺序错误）", op.ID, req)
			}
		}
		for _, p := range op.Provides {
			state[p] = true
		}
	}
	return nil
}
