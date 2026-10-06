package scheduler

import "fmt"

// Solve 在至多 20 个候选中选出收益最大的合法子集。
// 并列时先选实验数更少者，再按排序后的 id 列表字典序更小者。
// 返回排序后的实验 id 列表。
//
// 方法：子集穷举（2^20 约一百万），每个子集用时间回放核对合法性。
func Solve(p Problem) []int {
	n := len(p.Experiments)
	if n > 20 {
		panic(fmt.Sprintf("Solve: 候选数 %d 超过上限 20", n))
	}
	bestProfit := 0
	bestCount := 0
	bestIDs := []int(nil) // 空子集永远合法，收益 0
	for mask := 1; mask < (1 << uint(n)); mask++ {
		sel := maskIndices(mask, n)
		if !ReplayLegal(p.Experiments, p.Cooldown, sel) {
			continue
		}
		profit := 0
		for _, i := range sel {
			profit += p.Experiments[i].Profit
		}
		ids := sortedIDs(p.Experiments, sel)
		if better(profit, len(sel), ids, bestProfit, bestCount, bestIDs) {
			bestProfit, bestCount, bestIDs = profit, len(sel), ids
		}
	}
	return bestIDs
}

// better 报告候选 (profit,count,ids) 是否优于当前最优。
func better(profit, count int, ids []int, bProfit, bCount int, bIDs []int) bool {
	if profit != bProfit {
		return profit > bProfit
	}
	if count != bCount {
		return count < bCount
	}
	return lessIDs(ids, bIDs)
}

// lessIDs 比较两个已排序 id 列表的字典序。
func lessIDs(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func maskIndices(mask, n int) []int {
	var sel []int
	for i := 0; i < n; i++ {
		if mask&(1<<uint(i)) != 0 {
			sel = append(sel, i)
		}
	}
	return sel
}

func sortedIDs(exps []Experiment, sel []int) []int {
	ids := make([]int, 0, len(sel))
	for _, i := range sel {
		ids = append(ids, exps[i].ID)
	}
	sortInts(ids)
	return ids
}
