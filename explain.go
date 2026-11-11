package netsec

import "sort"

// Explanation 是维持当前决策所需的最少证据。
type Explanation struct {
	// Decision 是完整事实赋值下的当前决策。
	Decision Decision
	// Facts 是最少证据中被选中的事实 ID，按字典序排列。
	Facts []string
	// Values 与 Facts 等长、一一对应，给出各选中事实在完整赋值中的取值。
	Values []Value
	// Size 等于 len(Facts)，即最少事实数量。
	Size int
}

// Explain 求维持 facts 下当前结论所需的最少事实集合。
//
// 规则：未选事实一律视为 Unknown（缺失不等于失败）；只从规则可达且在
// facts 中取 Pass/Fail 的事实里挑选。按子集规模升序枚举，同规模按 ID
// 字典序枚举，第一个仍能推出当前决策的子集即答案，因此结果既是规模最小
// 的，也是同规模下 ID 字典序最小的。事实总数受 MaxFacts 限制。
func (r *Rule) Explain(facts map[string]Value) Explanation {
	target := r.Eval(facts)

	reachable := make(map[string]bool)
	collectFacts(r, reachable)

	candidates := make([]string, 0, len(reachable))
	for id := range reachable {
		if v, ok := facts[id]; ok && v != Unknown {
			candidates = append(candidates, id)
		}
	}
	sort.Strings(candidates)

	for size := 0; size <= len(candidates); size++ {
		found := false
		var picked []string
		forEachCombination(candidates, size, func(combo []string) bool {
			reduced := make(map[string]Value, size)
			for _, id := range combo {
				reduced[id] = facts[id]
			}
			if r.Decide(reduced) == Decide(target) {
				found = true
				picked = append(picked, combo...)
				return false
			}
			return true
		})
		if found {
			values := make([]Value, len(picked))
			for i, id := range picked {
				values[i] = facts[id]
			}
			return Explanation{
				Decision: Decide(target),
				Facts:    picked,
				Values:   values,
				Size:     len(picked),
			}
		}
	}

	// 不可达：完整候选集本身必然保持当前决策。
	panic("netsec: no explanation found")
}

// collectFacts 收集从节点可达的全部原子事实 ID。
func collectFacts(r *Rule, out map[string]bool) {
	if r.op == opFact {
		out[r.factID] = true
		return
	}
	for _, child := range r.children {
		collectFacts(child, out)
	}
}

// forEachCombination 按字典序枚举 items 中所有长度为 size 的组合，
// fn 返回 false 时提前终止。
func forEachCombination(items []string, size int, fn func(combo []string) bool) {
	if size == 0 {
		fn(nil)
		return
	}
	if size > len(items) {
		return
	}
	indices := make([]int, size)
	for i := range indices {
		indices[i] = i
	}
	combo := make([]string, size)
	for {
		for i, idx := range indices {
			combo[i] = items[idx]
		}
		if !fn(combo) {
			return
		}

		i := size - 1
		for i >= 0 && indices[i] == len(items)-size+i {
			i--
		}
		if i < 0 {
			return
		}
		indices[i]++
		for j := i + 1; j < size; j++ {
			indices[j] = indices[j-1] + 1
		}
	}
}
