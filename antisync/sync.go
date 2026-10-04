package antisync

// Stats 统计一次同步的通信量。
type Stats struct {
	// SummaryExchanges 是摘要交换轮数：每一轮双方就同一范围互发摘要并比较。
	SummaryExchanges int
	// RecordsSent 是双向传输的真实记录总条数（含墓碑）。
	RecordsSent int
	// RangesPruned 是因摘要相同而跳过的范围数。
	RangesPruned int
}

// Sync 在两个副本间执行一轮反熵同步：从根开始递归交换范围摘要，
// 摘要相同则跳过整棵子树；摘要不同且任一侧已是叶节点（或到达最大深度）
// 时交换该范围内的真实记录并按冲突规则合并。结束后双方数据收敛。
func Sync(a, b *Replica) Stats {
	st := Stats{}
	syncNodes(a, b, a.root, b.root, &st)
	return st
}

// syncNodes 处理一对覆盖相同范围的节点。分裂规则是确定性的，
// 因此递归过程中两侧节点范围始终一致；只有“一侧已分裂、另一侧仍是叶”
// 的不对称情况会提前落入记录交换分支。
func syncNodes(a, b *Replica, na, nb *node, st *Stats) {
	st.SummaryExchanges++
	if na.hash == nb.hash {
		st.RangesPruned++
		return
	}
	if na.leaf || nb.leaf {
		var ra, rb []Record
		na.collect(&ra)
		nb.collect(&rb)
		st.RecordsSent += len(ra) + len(rb)
		for _, rec := range rb {
			a.Apply(rec)
		}
		for _, rec := range ra {
			b.Apply(rec)
		}
		return
	}
	syncNodes(a, b, na.children[0], nb.children[0], st)
	syncNodes(a, b, na.children[1], nb.children[1], st)
}
