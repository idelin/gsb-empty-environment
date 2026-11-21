package commit

import "container/heap"

// relax 是无剪枝的精确松弛：每个完整状态 (W, C, 磁头) 独立保留最优双指标标签。
// 缓存超集并不安全支配子集（超集多占槽、且历史路径不同），因此不做任何前沿
// 剪枝；约束很小（目标 ≤11、源 ≤15、K≤5），完整状态可枚举，并由独立小图
// 枚举器逐实例交叉核对双指标，保证严格最优。
func (e *engine) relax(best map[uint64]*node, queue *pq,
	parent *node, key uint64, label [2]int64, action traceAction) {
	if old, ok := best[key]; ok {
		if !lexLess(label, old.label) {
			return
		}
		old.label = label
		old.parent = parent.key
		old.action = action
		heap.Push(queue, heapItem{key: key, label: label})
		return
	}
	best[key] = &node{key: key, label: label, parent: parent.key, action: action}
	heap.Push(queue, heapItem{key: key, label: label})
}

// trace 沿父指针恢复宏动作（读/写），返回时间正序。
func (e *engine) trace(best map[uint64]*node, goal *node) []traceAction {
	var rev []traceAction
	for k := goal.key; k != 0; {
		n := best[k]
		rev = append(rev, n.action)
		k = n.parent
	}
	out := make([]traceAction, len(rev))
	for i, a := range rev {
		out[len(rev)-1-i] = a
	}
	return out
}
