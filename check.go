package netsec

// checkConcrete 沿抽象路径逐步枚举具体可达集合，判定反例是否可行。
// 伪反例时返回首个断链位置 pivot，以及该位置上相互冲突的两组状态：
// reachSet 是实际可达取值；badSet 是同一抽象立方内会把路径继续引向
// 错误（断言失败或落入后继抽象立方）、但具体不可达的取值。
func (e *engine) checkConcrete(path []int) (trace []TraceStep, feasible bool, pivot int, reachSet, badSet valSet) {
	if len(path) == 0 {
		return nil, false, -1, nil, nil
	}

	current := make(valSet)
	for _, s := range e.initialStates() {
		current.add(s)
	}
	e.charge(len(current))

	// incoming[i] 是进入 path[i] 前的具体可达集合。
	incoming := make([]valSet, len(path))
	incoming[0] = current.copy()

	for idx, nodeIdx := range path {
		node := e.prog.Nodes[nodeIdx]
		next := make(valSet)
		failing := make(valSet)
		for s := range current {
			succs, fails := concreteSuccs(node, s)
			for _, ns := range succs {
				next.add(ns)
			}
			if fails {
				failing.add(s)
			}
		}

		// 错误汇聚前的最后一个节点：必须存在具体可达且断言失败的取值。
		if node.ErrTarget {
			if len(failing) > 0 {
				trace = e.extractWitness(path, incoming, failing.slice()[0])
				return trace, true, nodeIdx, nil, nil
			}
			bad := e.abstractSiblings(current, failingTarget(node))
			// bad 为空意味着当前谓词已把失败取值完全排除，抽象可达
			// 结果来自过时摘要或路径重建；跳过该边继续检查后续节点。
			if len(bad) == 0 {
				if idx+1 < len(path) {
					incoming[idx+1] = next.copy()
					current = next
				}
				continue
			}
			return nil, false, nodeIdx, current.copy(), bad
		}

		if idx+1 < len(path) {
			incoming[idx+1] = next.copy()
			// 抽象路径要求后继非空；若具体枚举得到空后继，则该边不可行，
			// 当前节点上的可达取值与立方内其余取值构成冲突。
			if len(next) == 0 {
				bad := e.abstractSiblings(current, pathContinueTarget(node))
				return nil, false, nodeIdx, current.copy(), bad
			}
			current = next
		}
	}

	// 路径未触达任何 ErrTarget 却被当作反例：保守报伪反例。
	last := path[len(path)-1]
	return nil, false, last, current.copy(), make(valSet)
}

// failingTarget 构造“断言条件为假”这一目标谓词（仅用于枚举同立方冲突取值）。
func failingTarget(node Node) Expr {
	if a, ok := node.Stmt.(Assert); ok {
		return Not{Inner: a.Cond}
	}
	return FALSE
}

// pathContinueTarget 构造“能沿某条后继边继续推进路径”的目标谓词。
// 布尔程序里非断言语句的后继不会因条件被阻断，这里返回 TRUE 表示
// 立方内所有取值都属于需要被区分的候选；具体不可达部分由调用方剔除。
func pathContinueTarget(node Node) Expr {
	if len(node.Next) == 0 {
		return FALSE
	}
	return TRUE
}

// abstractSiblings 在 reach 所在的抽象立方内枚举满足 target 的取值，
// 并剔除其中实际可达的部分，得到纯冲突集合 badSet。
func (e *engine) abstractSiblings(reach valSet, target Expr) valSet {
	cube := e.abstractOf(reach)
	bad := make(valSet)
	members := e.cubeMembers(cube)
	e.charge(len(members))
	for _, s := range members {
		if target.eval(s) && !reach.contains(s) {
			bad.add(s)
		}
	}
	return bad
}

// extractWitness 从失败取值出发反向回溯，构造一条从入口取值到断言失败的
// 逐步具体见证，保证每一步的进入取值都在对应 incoming 集合内。
func (e *engine) extractWitness(path []int, incoming []valSet, failingVal Valuation) []TraceStep {
	chain := make([]Valuation, len(path))
	cur := failingVal
	for idx := len(path) - 1; idx >= 0; idx-- {
		chain[idx] = cur
		if idx == 0 {
			break
		}
		node := e.prog.Nodes[path[idx]]
		predecessor := Valuation(0)
		found := false
		for candidate := range incoming[idx-1] {
			succs, _ := concreteSuccs(node, candidate)
			for _, ns := range succs {
				if ns == cur {
					predecessor, found = candidate, true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			predecessor = anyValuation(incoming[idx-1])
		}
		cur = predecessor
	}

	trace := make([]TraceStep, 0, len(path))
	for idx, nodeIdx := range path {
		in := chain[idx]
		out := in
		if idx+1 < len(path) {
			out = chain[idx+1]
		}
		trace = append(trace, TraceStep{Node: nodeIdx, In: in, Out: out})
	}
	return trace
}

func anyValuation(set valSet) Valuation {
	for v := range set {
		return v
	}
	return 0
}
