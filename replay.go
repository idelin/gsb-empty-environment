package scheduler

// ReplayLegal 用时间回放独立地核对子集合法性：
// 每台设备沿时间轴回放选中实验，维护 nextFree（冷却结束后
// 第一个可用时隙），事件落点早于 nextFree 即判非法。
// 与 LegalSubset 的逐对间隔检查互为交叉验证。
func ReplayLegal(exps []Experiment, cooldown int, sel []int) bool {
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
		nextFree := slots[0]
		for _, s := range slots {
			if s < nextFree {
				return false // 与上一实验冲突或冷却未结束
			}
			nextFree = s + 1 + cooldown
		}
	}
	return true
}
