package netsec

import "sort"

// orderedEdge 是参考实现内部使用的一条“before 必须先于 after”的位置约束。
type orderedEdge struct{ before, after int }

// ReferenceEnumerate 用全排列穷举全部事件全序，再逐条过滤，作为主枚举器
// （偏序拓扑 + 连贯性剪枝）的独立参考实现。
//
// 参考实现不做任何剪枝：对每个排列直接检查线程程序序、读自边、发布/获取同步
// 边与每位置写序（读连贯性）。事件数超过 MaxReferenceEvents 时返回
// ErrLimitExceeded，因为全排列开销为 n!。
//
// 两个实现对同一 Query 返回的 Trace 集合（按相同规范化排序后）应当完全一致。
func ReferenceEnumerate(q Query) (*Result, error) {
	prog, rf, _, err := validate(q)
	if err != nil {
		return nil, err
	}

	var real []int
	for id, e := range prog.Events {
		if e.Thread >= 0 {
			real = append(real, id)
		}
	}
	if len(real) > MaxReferenceEvents {
		return nil, ErrLimitExceeded
	}

	allowed := func(a, b int) bool {
		if q.MayReorder != nil {
			for _, pr := range q.MayReorder {
				if pr.First == a && pr.Second == b {
					return true
				}
			}
			return false
		}
		return prog.MayReorder(a, b)
	}

	// 预先列出所有必须满足位置先后的 HB 直连边（读自边 + 同步边）。
	var hbEdges []orderedEdge
	for reader, writer := range rf {
		hbEdges = append(hbEdges, orderedEdge{writer, reader})
	}

	// 同步边独立于读自边：同位置的发布写与获取读（含屏障配对）。
	for ri, re := range prog.Events {
		if re.Thread < 0 || re.IsWrite || re.IsFence || !re.Order.isAcquireClass() {
			continue
		}
		for wi, we := range prog.Events {
			if we.Thread < 0 || !we.IsWrite || we.Loc != re.Loc || !we.Order.isReleaseClass() {
				continue
			}
			hbEdges = append(hbEdges, orderedEdge{wi, ri})
			if re.Thread < 0 {
				continue
			}
			wlo, _ := prog.ThreadEvents(we.Thread)
			releaseFence := -1
			for f := wlo; f < wi; f++ {
				if fe := prog.Events[f]; fe.IsFence && fe.Order.isReleaseClass() {
					releaseFence = f
				}
			}
			_, rhi := prog.ThreadEvents(re.Thread)
			acquireFence := -1
			for f := ri + 1; f < rhi; f++ {
				if fe := prog.Events[f]; fe.IsFence && fe.Order.isAcquireClass() {
					acquireFence = f
					break
				}
			}
			if releaseFence >= 0 {
				hbEdges = append(hbEdges, orderedEdge{releaseFence, ri})
			}
			if acquireFence >= 0 {
				hbEdges = append(hbEdges, orderedEdge{wi, acquireFence})
			}
			if releaseFence >= 0 && acquireFence >= 0 {
				hbEdges = append(hbEdges, orderedEdge{releaseFence, acquireFence})
			}
		}
	}

	// 普通访问 + 屏障的发布模式：读 r 经读自边读到发布屏障后的写 w，且 r
	// 之前有获取屏障，则两个屏障之间建同步边。
	for reader, writer := range rf {
		w, r := prog.Events[writer], prog.Events[reader]
		if w.Thread < 0 || r.Thread < 0 {
			continue
		}
		wlo, _ := prog.ThreadEvents(w.Thread)
		releaseFence := -1
		for f := wlo; f < writer; f++ {
			if fe := prog.Events[f]; fe.IsFence && fe.Order.isReleaseClass() {
				releaseFence = f
			}
		}
		rlo, _ := prog.ThreadEvents(r.Thread)
		acquireFence := -1
		for f := rlo; f < reader; f++ {
			if fe := prog.Events[f]; fe.IsFence && fe.Order.isAcquireClass() {
				acquireFence = f
				break
			}
		}
		if releaseFence >= 0 && acquireFence >= 0 {
			hbEdges = append(hbEdges, orderedEdge{releaseFence, acquireFence})
		}
	}

	limit := q.Limit
	if limit == 0 {
		limit = defaultLimit
	}

	// 对直连边求传递闭包，得到完整先行发生关系；参考实现据此做位置检查，
	// 与主枚举器 buildGraph 的 Warshall 闭包保持等价。
	nEvents := len(prog.Events)
	before := make([][]bool, nEvents)
	for i := range before {
		before[i] = make([]bool, nEvents)
	}
	for _, e := range hbEdges {
		before[e.before][e.after] = true
	}
	for t := 0; t < prog.ThreadCount; t++ {
		lo, hi := prog.ThreadEvents(t)
		for a := lo; a+1 < hi; a++ {
			if !allowed(a, a+1) {
				before[a][a+1] = true
			}
		}
	}
	for _, init := range prog.Initial {
		for j, e := range prog.Events {
			if e.Thread >= 0 {
				before[init][j] = true
			}
		}
	}
	for k := 0; k < nEvents; k++ {
		for i := 0; i < nEvents; i++ {
			if !before[i][k] {
				continue
			}
			for j := 0; j < nEvents; j++ {
				if before[k][j] {
					before[i][j] = true
				}
			}
		}
	}
	res := &Result{}
	lastWrite := make(map[string]int, len(prog.Initial))

	// 对全排列做原地回溯，避免反复分配。
	var permute func(int) bool
	permute = func(start int) bool {
		if start == len(real) {
			if referenceAccept(prog, rf, real, before, lastWrite) {
				res.Traces = append(res.Traces, append(Trace(nil), real...))
				if limit > 0 && len(res.Traces) >= limit {
					res.Truncated = true
					return true
				}
			}
			return false
		}
		for i := start; i < len(real); i++ {
			real[start], real[i] = real[i], real[start]
			if permute(start + 1) {
				return true
			}
			real[start], real[i] = real[i], real[start]
		}
		return false
	}
	permute(0)

	// 全排列的产生顺序与拓扑 DFS 不同，统一排序后再比较。
	sort.Slice(res.Traces, func(i, j int) bool {
		return traceLess(res.Traces[i], res.Traces[j])
	})
	if len(res.Traces) == 0 {
		return nil, ErrInfeasible
	}
	return res, nil
}

// referenceAccept 对单个排列做全部合法性检查，逻辑刻意与主枚举器分开编写。
func referenceAccept(
	prog *Program,
	rf map[int]int,
	order Trace,
	before [][]bool,
	lastWrite map[string]int,
) bool {
	pos := make([]int, len(prog.Events))
	for i, id := range order {
		pos[id] = i
	}

	// 1) 先行发生闭包：before[a][b] 要求 a 在全序中先于 b。线程程序序与
	// 初始写已并入 before；初始写不在排列中，跳过。
	for a := range before {
		if prog.Events[a].Thread < 0 {
			continue
		}
		for b := range before[a] {
			if before[a][b] && prog.Events[b].Thread >= 0 && pos[a] > pos[b] {
				return false
			}
		}
	}
	// 2) 每位置写序：读必须读到该位置当前最近一次写。
	for loc, init := range prog.Initial {
		lastWrite[loc] = init
	}
	for _, id := range order {
		e := prog.Events[id]
		switch {
		case e.IsFence:
			continue
		case e.IsWrite:
			lastWrite[e.Loc] = id
		default:
			if lastWrite[e.Loc] != rf[id] {
				return false
			}
		}
	}
	return true
}

// traceLess 给出执行的字典序，用于跨实现比较时规范化顺序。
func traceLess(a, b Trace) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// NormalizeTraces 复制并按字典序排序一组执行，便于两个实现做集合比较。
func NormalizeTraces(traces []Trace) []Trace {
	out := make([]Trace, len(traces))
	copy(out, traces)
	sort.Slice(out, func(i, j int) bool { return traceLess(out[i], out[j]) })
	return out
}
