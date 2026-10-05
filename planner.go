package planner

import "fmt"

// Build 根据操作的读写对象与前置状态生成迁移计划。
//
// 流程：
//  1. 由 Requires/Provides 推导依赖边，冲突但无依赖的操作按输入顺序补边；
//  2. 拓扑分层（ASAP），同层冲突的操作顺延到下一层；
//  3. 在可行区间内把停写操作聚拢到已有停写层，最小化停写窗口数；
//  4. 合并相邻可合并的步骤；
//  5. 校验步骤数上限与最终顺序，并标注不可逆边界。
func Build(ops []Op, preexisting []string) (*Plan, error) {
	if len(ops) == 0 {
		return nil, fmt.Errorf("没有任何迁移操作")
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if op.ID == "" {
			return nil, fmt.Errorf("存在缺少 id 的操作")
		}
		if seen[op.ID] {
			return nil, fmt.Errorf("操作 id %q 重复", op.ID)
		}
		seen[op.ID] = true
	}

	deps, err := buildDeps(ops, preexisting)
	if err != nil {
		return nil, err
	}
	order, err := topoSort(deps)
	if err != nil {
		return nil, err
	}

	layer := asapLayers(ops, deps, order)
	layer = gatherWriteStopOps(ops, deps, layer, order)
	steps := compact(ops, deps, layer)

	if len(steps) > MaxSteps {
		return nil, fmt.Errorf("计划需要 %d 步，超过上限 %d 步", len(steps), MaxSteps)
	}

	plan := &Plan{}
	var flat []Op
	for i, idxs := range steps {
		st := Step{Index: i + 1}
		for _, oi := range idxs {
			op := ops[oi]
			st.Ops = append(st.Ops, op)
			flat = append(flat, op)
			if op.NeedsWriteStop {
				st.NeedsWriteStop = true
			}
			if op.Rollback == "" {
				st.Irreversible = true
			}
		}
		if st.NeedsWriteStop {
			plan.WriteStopWindows++
		}
		if st.Irreversible {
			plan.IrreversibleBoundaries = append(plan.IrreversibleBoundaries, st.Index)
		}
		plan.Steps = append(plan.Steps, st)
	}

	// 自检：规划结果必须满足全部前置状态顺序。
	if err := ValidateSequence(flat, preexisting); err != nil {
		return nil, fmt.Errorf("规划结果未通过顺序校验: %v", err)
	}
	return plan, nil
}

// buildDeps 推导依赖边。返回 deps[j] = {j 依赖的操作下标集合}。
func buildDeps(ops []Op, preexisting []string) (map[int]map[int]bool, error) {
	deps := map[int]map[int]bool{}
	add := func(j, i int) {
		if deps[j] == nil {
			deps[j] = map[int]bool{}
		}
		deps[j][i] = true
	}
	// 状态依赖：j 需要的状态由 i 提供，则 i 必须先执行。
	for j, op := range ops {
		for _, req := range op.Requires {
			if contains(preexisting, req) {
				continue
			}
			provider := -1
			for i, other := range ops {
				if i != j && contains(other.Provides, req) {
					provider = i
					break
				}
			}
			if provider < 0 {
				return nil, fmt.Errorf("操作 %q 需要状态 %q，但没有任何操作提供它", op.ID, req)
			}
			add(j, provider)
		}
	}
	// 冲突定序：冲突但互相无依赖的操作按输入顺序补边，保证结果确定。
	for i := 0; i < len(ops); i++ {
		for j := i + 1; j < len(ops); j++ {
			if !conflicts(ops[i], ops[j]) {
				continue
			}
			if deps[j][i] || reachable(deps, i, j) || reachable(deps, j, i) {
				continue
			}
			add(j, i)
		}
	}
	return deps, nil
}

// reachable 判断从 from 出发沿依赖边能否到达 to。
func reachable(deps map[int]map[int]bool, from, to int) bool {
	visited := map[int]bool{}
	stack := []int{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == to {
			return true
		}
		if visited[n] {
			continue
		}
		visited[n] = true
		for d := range deps[n] {
			stack = append(stack, d)
		}
	}
	return false
}

// topoSort 返回一个拓扑序（操作下标），存在环时报错。
func topoSort(deps map[int]map[int]bool) ([]int, error) {
	indeg := map[int]int{}
	nodes := map[int]bool{}
	for j, ds := range deps {
		nodes[j] = true
		for i := range ds {
			nodes[i] = true
			indeg[j]++
		}
	}
	var queue []int
	for n := range nodes {
		if indeg[n] == 0 {
			queue = append(queue, n)
		}
	}
	var order []int
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)
		for m, ds := range deps {
			if ds[n] {
				indeg[m]--
				if indeg[m] == 0 {
					queue = append(queue, m)
				}
			}
		}
	}
	if len(order) != len(nodes) {
		return nil, fmt.Errorf("操作依赖存在环，无法规划")
	}
	return order, nil
}

// asapLayers 尽早调度：每个操作放在满足依赖且与同层操作不冲突的最早一层。
func asapLayers(ops []Op, deps map[int]map[int]bool, order []int) []int {
	layer := make([]int, len(ops))
	assigned := make([]bool, len(ops))
	for _, j := range order {
		l := 0
		for d := range deps[j] {
			if layer[d]+1 > l {
				l = layer[d] + 1
			}
		}
		for {
			ok := true
			for k := range ops {
				if assigned[k] && layer[k] == l && conflicts(ops[j], ops[k]) {
					ok = false
					break
				}
			}
			if ok {
				break
			}
			l++
		}
		layer[j] = l
		assigned[j] = true
	}
	return layer
}

// gatherWriteStopOps 在依赖允许的范围 [最早层, 最晚层] 内，
// 把需要停写的操作移动到已含停写操作的层，从而合并停写窗口。
func gatherWriteStopOps(ops []Op, deps map[int]map[int]bool, layer []int, order []int) []int {
	n := len(ops)
	maxLayer := 0
	for _, l := range layer {
		if l > maxLayer {
			maxLayer = l
		}
	}
	// 反向拓扑计算每个操作的最晚可行层。
	latest := make([]int, n)
	for i := range latest {
		latest[i] = maxLayer
	}
	succ := map[int][]int{}
	for j, ds := range deps {
		for d := range ds {
			succ[d] = append(succ[d], j)
		}
	}
	for k := len(order) - 1; k >= 0; k-- {
		j := order[k]
		for _, s := range succ[j] {
			if latest[s]-1 < latest[j] {
				latest[j] = latest[s] - 1
			}
		}
	}
	// 逐个尝试把停写操作并入已有的停写层。
	for j, op := range ops {
		if !op.NeedsWriteStop {
			continue
		}
		best := -1
		for l := layer[j] + 1; l <= latest[j]; l++ {
			if !layerHasWriteStop(ops, layer, j, l) {
				continue
			}
			if fitsLayer(ops, layer, j, l) {
				best = l // 尽量往后聚拢，取最远的可行停写层
			}
		}
		if best >= 0 {
			layer[j] = best
		}
	}
	return layer
}

func layerHasWriteStop(ops []Op, layer []int, self, l int) bool {
	for k, op := range ops {
		if k != self && layer[k] == l && op.NeedsWriteStop {
			return true
		}
	}
	return false
}

// fitsLayer 判断操作 j 放入第 l 层时是否与该层其他操作冲突。
func fitsLayer(ops []Op, layer []int, j, l int) bool {
	for k := range ops {
		if k != j && layer[k] == l && conflicts(ops[j], ops[k]) {
			return false
		}
	}
	return true
}

// compact 按层号归组、丢弃空层，并反复合并相邻可合并的步骤。
func compact(ops []Op, deps map[int]map[int]bool, layer []int) [][]int {
	maxLayer := 0
	for _, l := range layer {
		if l > maxLayer {
			maxLayer = l
		}
	}
	steps := make([][]int, maxLayer+1)
	for j := range ops {
		steps[layer[j]] = append(steps[layer[j]], j)
	}
	var nonEmpty [][]int
	for _, s := range steps {
		if len(s) > 0 {
			nonEmpty = append(nonEmpty, s)
		}
	}
	// 合并相邻步骤：i+1 中的操作不依赖 i 中的操作，且两边互不冲突。
	merged := true
	for merged {
		merged = false
		for i := 0; i+1 < len(nonEmpty); i++ {
			if canMerge(nonEmpty[i], nonEmpty[i+1], ops, deps) {
				nonEmpty[i] = append(nonEmpty[i], nonEmpty[i+1]...)
				nonEmpty = append(nonEmpty[:i+1], nonEmpty[i+2:]...)
				merged = true
				break
			}
		}
	}
	return nonEmpty
}

func canMerge(a, b []int, ops []Op, deps map[int]map[int]bool) bool {
	inA := map[int]bool{}
	for _, i := range a {
		inA[i] = true
	}
	for _, j := range b {
		for d := range deps[j] {
			if inA[d] {
				return false // b 中操作依赖 a 中操作，必须分步
			}
		}
		for _, i := range a {
			if conflicts(ops[i], ops[j]) {
				return false
			}
		}
	}
	return true
}
