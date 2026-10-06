package testselect

// edge 表示一条调用边 caller -> callee。
type edge struct {
	caller string
	callee string
}

// edgeSet 合并静态与动态边，便于统一做差集与遍历。
func edgeSet(callGraph, dynamic map[string][]string) map[edge]bool {
	set := make(map[edge]bool)
	for _, graph := range []map[string][]string{callGraph, dynamic} {
		for caller, callees := range graph {
			for _, callee := range callees {
				set[edge{caller: caller, callee: callee}] = true
			}
		}
	}
	return set
}

// unionGraph 构建旧图与新图的并集（含静态边与动态边）。
// 在并集上传播可保证：无论是新增边还是删除边，受影响的调用方都能被覆盖到。
func unionGraph(oldEdges, newEdges map[edge]bool) map[string][]string {
	graph := make(map[string][]string)
	add := func(e edge) {
		graph[e.caller] = append(graph[e.caller], e.callee)
	}
	for e := range oldEdges {
		add(e)
	}
	for e := range newEdges {
		add(e)
	}
	return graph
}

// reverseReachable 返回 seeds 及其在 graph 上的全部传递调用方（含 seeds 自身）。
func reverseReachable(graph map[string][]string, seeds map[string]bool) map[string]bool {
	callers := make(map[string][]string)
	for caller, callees := range graph {
		for _, callee := range callees {
			callers[callee] = append(callers[callee], caller)
		}
	}
	reached := make(map[string]bool)
	stack := make([]string, 0, len(seeds))
	for seed := range seeds {
		if !reached[seed] {
			reached[seed] = true
			stack = append(stack, seed)
		}
	}
	for len(stack) > 0 {
		fn := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, caller := range callers[fn] {
			if !reached[caller] {
				reached[caller] = true
				stack = append(stack, caller)
			}
		}
	}
	return reached
}

// depClosure 返回 roots 在 graph 上的全依赖闭包（含 roots 自身），
// 即 roots 直接或间接依赖的全部函数。
func depClosure(graph map[string][]string, roots []string) map[string]bool {
	closure := make(map[string]bool)
	stack := make([]string, 0, len(roots))
	for _, root := range roots {
		if !closure[root] {
			closure[root] = true
			stack = append(stack, root)
		}
	}
	for len(stack) > 0 {
		fn := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, callee := range graph[fn] {
			if !closure[callee] {
				closure[callee] = true
				stack = append(stack, callee)
			}
		}
	}
	return closure
}
