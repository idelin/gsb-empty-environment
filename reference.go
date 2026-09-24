package cacheaudit

// referenceLinearizable 是独立的全排列参考核验实现：
// 枚举事件的全部排列，逐一检查实序一致性与顺序契约合法性。
// 它与 linearizable 不共享任何搜索代码，用于小规模历史的交叉验证。
// 复杂度为 O(n! * n^2)，仅适合小历史（建议 n <= 9）。
func referenceLinearizable(events []Event) bool {
	n := len(events)
	if n == 0 {
		return true
	}
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	used := make([]bool, n)
	pos := 0
	var enumerate func() bool
	enumerate = func() bool {
		if pos == n {
			return checkPermutation(events, perm)
		}
		for i := 0; i < n; i++ {
			if used[i] {
				continue
			}
			used[i] = true
			perm[pos] = i
			pos++
			if enumerate() {
				return true
			}
			pos--
			used[i] = false
		}
		return false
	}
	return enumerate()
}

// checkPermutation 检查一个完整排列是否同时满足实序约束与顺序契约。
func checkPermutation(events []Event, perm []int) bool {
	// 实序：排列中靠前的 a 与靠后的 b，不得存在 b 完成于 a 调用之前。
	for x := 0; x < len(perm); x++ {
		for y := x + 1; y < len(perm); y++ {
			a, b := events[perm[x]], events[perm[y]]
			if b.End <= a.Start {
				return false
			}
		}
	}
	// 顺序契约：独立的状态模拟，不复用 step。
	visible := make(map[string]uint64)
	pending := make(map[string]uint64)
	for _, idx := range perm {
		ev := events[idx]
		switch ev.Kind {
		case KindLoad:
			pending[ev.Key] = ev.Version
		case KindPublish:
			if pending[ev.Key] == 0 {
				return false
			}
			visible[ev.Key] = pending[ev.Key]
		case KindEvict:
			pending[ev.Key] = 0
			visible[ev.Key] = 0
		case KindHit:
			if ev.Version == 0 || visible[ev.Key] != ev.Version {
				return false
			}
		case KindMiss:
			if visible[ev.Key] != 0 {
				return false
			}
		}
	}
	return true
}
