package linearleak

// proofPredicate 判定给定方程子集（连同固定条件）是否不可行。
type proofPredicate func(subset []int) triState

// minimizeProof 返回一个使 pred 成立（确认不可行）的最小方程下标集合。
//
// 分两阶段：
//  1. 逐个尝试删除方程，删除后仍确认不可行则永久移除，得到包含极小核；
//     任何一步返回 triUnknown 都保守保留该方程，并把 exact 置为 false。
//  2. 预算允许时按基数从小到大做规范 DFS，证明不存在比核更小的集合；
//     找到更小集合则替换，预算耗尽则保留核并标记 exact=false。
//
// 返回的 proof 始终是合法证明，exact 仅表示“基数最小”是否已被证明。
func (an *analyzer) minimizeProof(all []int, pred proofPredicate) (proof []int, exact bool) {
	// 阶段一：包含极小核。
	core := append([]int(nil), all...)
	exact = true
	for i := 0; i < len(core); {
		trial := append(append([]int(nil), core[:i]...), core[i+1:]...)
		switch pred(trial) {
		case triTrue:
			core = trial // 删除后仍不可行，方程冗余。
		case triFalse:
			i++ // 删除后恢复可行，该方程必要。
		default:
			exact = false
			i++ // 预算不足，保守保留。
		}
	}

	if !exact || len(core) <= 1 {
		return core, exact
	}

	// 阶段二：搜索基数更小的不可行子集。
	smaller, outcome := an.searchSmaller(all, len(core), pred)
	if outcome == triUnknown {
		return core, false
	}
	if outcome == triTrue {
		return smaller, true
	}
	return core, true
}

// searchSmaller 寻找基数严格小于 limit 的不可行子集。
// triTrue 表示找到并通过 subset 返回；triFalse 表示预算内已穷尽、
// 不存在更小集合；triUnknown 表示预算耗尽无法穷尽。
func (an *analyzer) searchSmaller(all []int, limit int, pred proofPredicate) (subset []int, outcome triState) {
	var chosen []int
	sawUnknown := false

	var dfs func(start int) triState
	dfs = func(start int) triState {
		if len(chosen) >= limit {
			return triFalse
		}
		for k := start; k < len(all); k++ {
			// 从当前位置最多只能把 chosen 扩展到该长度，更小集合
			// 的长度上界是 limit-1；剩余元素不足时可安全剪枝。
			maxReachable := len(chosen) + (len(all) - k)
			if maxReachable < 1 {
				break
			}
			chosen = append(chosen, all[k])
			switch pred(append([]int(nil), chosen...)) {
			case triTrue:
				return triTrue
			case triUnknown:
				sawUnknown = true
			}
			if deeper := dfs(k + 1); deeper == triTrue {
				return triTrue
			} else if deeper == triUnknown {
				sawUnknown = true
			}
			chosen = chosen[:len(chosen)-1]
		}
		if sawUnknown {
			return triUnknown
		}
		return triFalse
	}

	result := dfs(0)
	if result == triTrue {
		return append([]int(nil), chosen...), triTrue
	}
	if result == triUnknown {
		return nil, triUnknown
	}
	return nil, triFalse
}
