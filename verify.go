package netsec

// Verify independently re-checks a Plan result against the request using a
// full-state BFS whose safety predicate is built with a disjoint-set union
// instead of the adjacency walk used by Plan. It returns true only when the
// evidence, feasibility, shortest length, and tie-breaking all hold.
func Verify(req *Request, result *Result) (bool, error) {
	comp, start, goal, err := validate(req)
	if err != nil {
		return false, err
	}
	if result == nil {
		return false, nil
	}

	safeTable := dsuSafeTable(comp)
	parent, depth, parentEdge := referenceBFS(comp, start, safeTable)
	if parent[goal] == -1 {
		return verifyInfeasible(comp, result, start, safeTable), nil
	}

	expected := buildSteps(comp, parent, parentEdge, start, goal)
	if !result.Feasible || len(result.Steps) != len(expected) {
		return false, nil
	}
	for i := range expected {
		if result.Steps[i].EdgeID != expected[i].EdgeID ||
			result.Steps[i].Enabled != expected[i].Enabled {
			return false, nil
		}
	}
	if !stepsReachGoal(comp, start, goal, result.Steps) {
		return false, nil
	}

	visitedCount, blockedCount := referenceCounts(comp, start, safeTable)
	if result.Evidence.TotalStates != 1<<len(comp.ids) ||
		len(result.Evidence.VisitedSafe) != visitedCount ||
		len(result.Evidence.Blocked) != blockedCount {
		return false, nil
	}

	visitedMasks := make(map[int]bool)
	for _, record := range result.Evidence.VisitedSafe {
		mask, ok := recordMask(comp, record.State)
		if !ok || !safeTable[mask] || visitedMasks[mask] ||
			record.Depth != depth[mask] {
			return false, nil
		}
		visitedMasks[mask] = true
	}

	blockedMasks := make(map[int]bool)
	for _, record := range result.Evidence.Blocked {
		mask, ok := recordMask(comp, record.State)
		if !ok || safeTable[mask] || blockedMasks[mask] || record.Depth < 0 {
			return false, nil
		}
		blockedMasks[mask] = true
	}

	if !visitedMasks[int(start)] || blockedMasks[int(start)] {
		return false, nil
	}
	return depth[goal] == len(result.Steps), nil
}

func verifyInfeasible(comp *compiled, result *Result, start uint16, safeTable []bool) bool {
	if result.Feasible {
		return false
	}
	visitedCount, blockedCount := referenceCounts(comp, start, safeTable)
	if result.Evidence.TotalStates != 1<<len(comp.ids) ||
		len(result.Evidence.VisitedSafe) != visitedCount ||
		len(result.Evidence.Blocked) != blockedCount ||
		len(result.Steps) != 0 {
		return false
	}

	seen := make(map[int]bool)
	for _, record := range result.Evidence.VisitedSafe {
		mask, ok := recordMask(comp, record.State)
		if !ok || !safeTable[mask] || seen[mask] {
			return false
		}
		seen[mask] = true
	}
	for _, record := range result.Evidence.Blocked {
		mask, ok := recordMask(comp, record.State)
		if !ok || safeTable[mask] || seen[mask] {
			return false
		}
		seen[mask] = true
	}
	return len(seen) == visitedCount+blockedCount
}

type dsu struct {
	parent []int
	rank   []int
}

func newDSU(size int) *dsu {
	parent := make([]int, size)
	for i := range parent {
		parent[i] = i
	}
	return &dsu{parent: parent, rank: make([]int, size)}
}

func (d *dsu) find(node int) int {
	root := node
	for d.parent[root] != root {
		root = d.parent[root]
	}
	for d.parent[node] != node {
		next := d.parent[node]
		d.parent[node] = root
		node = next
	}
	return root
}

func (d *dsu) union(a, b int) {
	ra, rb := d.find(a), d.find(b)
	if ra == rb {
		return
	}
	if d.rank[ra] < d.rank[rb] {
		ra, rb = rb, ra
	}
	d.parent[rb] = ra
	if d.rank[ra] == d.rank[rb] {
		d.rank[ra]++
	}
}

// dsuSafeTable evaluates every switch state independently with a fresh
// disjoint-set, giving Verify a safety oracle independent of Plan.safe.
func dsuSafeTable(comp *compiled) []bool {
	total := 1 << len(comp.ids)
	table := make([]bool, total)
	nodeCount := len(comp.nodeIDs)
	for mask := 0; mask < total; mask++ {
		set := newDSU(nodeCount)
		for edgeIndex := range comp.ids {
			if mask&(1<<edgeIndex) != 0 {
				set.union(comp.from[edgeIndex], comp.to[edgeIndex])
			}
		}
		ok := true
		for _, pair := range comp.pairs {
			if set.find(pair[0]) == set.find(pair[1]) {
				ok = false
				break
			}
		}
		table[mask] = ok
	}
	return table
}

// referenceBFS is an independent full-state BFS over the safe hypercube.
func referenceBFS(comp *compiled, start uint16, safeTable []bool) ([]int, []int, []int) {
	total := 1 << len(comp.ids)
	parent := make([]int, total)
	depth := make([]int, total)
	parentEdge := make([]int, total)
	for i := range parent {
		parent[i] = -1
		parentEdge[i] = -1
	}
	if !safeTable[start] {
		return parent, depth, parentEdge
	}
	parent[start] = -2
	queue := []int{int(start)}
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		for edgeIndex := range comp.ids {
			next := current ^ (1 << edgeIndex)
			if parent[next] != -1 || !safeTable[next] {
				continue
			}
			parent[next] = current
			parentEdge[next] = edgeIndex
			depth[next] = depth[current] + 1
			queue = append(queue, next)
		}
	}
	return parent, depth, parentEdge
}

// referenceCounts mirrors the evidence Plan emits: every visited safe state,
// and each distinct unsafe state encountered while expanding the frontier
// (an unsafe start is recorded on its own). Each blocked state counts once.
func referenceCounts(comp *compiled, start uint16, safeTable []bool) (visited, blocked int) {
	total := 1 << len(comp.ids)
	parent, _, _ := referenceBFS(comp, start, safeTable)
	for mask := 0; mask < total; mask++ {
		if parent[mask] != -1 {
			visited++
		}
	}
	blockedSet := make(map[int]bool)
	if !safeTable[start] {
		blockedSet[int(start)] = true
	}
	for mask := 0; mask < total; mask++ {
		if !safeTable[mask] || parent[mask] == -1 {
			continue
		}
		for edgeIndex := range comp.ids {
			neighbor := mask ^ (1 << edgeIndex)
			if !safeTable[neighbor] {
				blockedSet[neighbor] = true
			}
		}
	}
	return visited, len(blockedSet)
}

func recordMask(comp *compiled, state map[string]bool) (int, bool) {
	if len(state) != len(comp.ids) {
		return 0, false
	}
	var mask int
	for edgeIndex, id := range comp.ids {
		value, ok := state[id]
		if !ok {
			return 0, false
		}
		if value {
			mask |= 1 << edgeIndex
		}
	}
	return mask, true
}

func stepsReachGoal(comp *compiled, start, goal uint16, steps []Step) bool {
	mask := int(start)
	for _, step := range steps {
		edgeIndex, ok := comp.index[step.EdgeID]
		if !ok {
			return false
		}
		mask ^= 1 << edgeIndex
		if (mask&(1<<edgeIndex) != 0) != step.Enabled {
			return false
		}
	}
	return uint16(mask) == goal
}
