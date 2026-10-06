// Package scheduler 求解带冷却与互斥约束的实验选择问题。
//
// 模型假设（两部分共用）：
//   - 每个候选实验占据一个整数时隙 [Slot, Slot+1)，收益可以为负。
//   - 同一设备上两次实验之间至少冷却 Cooldown 个完整时隙，
//     即时隙 t 的实验之后，同一设备下一次实验时隙 t' 需满足 t' >= t+1+Cooldown。
//   - Group != 0 的实验属于互斥组，同一组中至多选择一个；Group == 0 表示无互斥约束。
package scheduler

// Experiment 描述一个候选实验。
type Experiment struct {
	ID     int // 唯一标识
	Device int // 设备编号
	Slot   int // 占用的整数时隙
	Profit int // 收益，可以为负（单阶段求解时使用）
	Group  int // 互斥组编号，0 表示不属于任何互斥组
}

// Problem 是单阶段实验选择问题。
type Problem struct {
	Experiments []Experiment
	Cooldown    int
}

// LegalSubset 用逐对检查判断下标子集 sel 是否合法：
// 同设备时隙间隔满足冷却，且互斥组内至多一个。
func LegalSubset(exps []Experiment, cooldown int, sel []int) bool {
	seenGroup := make(map[int]bool)
	byDevice := make(map[int][]int)
	for _, i := range sel {
		e := exps[i]
		if e.Group != 0 {
			if seenGroup[e.Group] {
				return false
			}
			seenGroup[e.Group] = true
		}
		byDevice[e.Device] = append(byDevice[e.Device], e.Slot)
	}
	for _, slots := range byDevice {
		sortInts(slots)
		for k := 1; k < len(slots); k++ {
			if slots[k]-slots[k-1] < cooldown+1 {
				return false
			}
		}
	}
	return true
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
