package taskpkg

// hungarianAssign returns the minimum-cost assignment of every row to a
// distinct column. Rows and columns are 0-based; the input must be square.
func hungarianAssign(cost [][]int) []int {
	n := len(cost)
	if n == 0 {
		return nil
	}
	const inf = int(^uint(0) >> 2)
	u := make([]int, n+1)
	v := make([]int, n+1)
	p := make([]int, n+1)
	way := make([]int, n+1)
	for i := 1; i <= n; i++ {
		way[0] = 0
		p[0] = i
		j0 := 0
		minv := make([]int, n+1)
		for j := range minv {
			minv[j] = inf
		}
		used := make([]bool, n+1)
		for {
			used[j0] = true
			i0 := p[j0]
			delta := inf
			j1 := 0
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				cur := cost[i0-1][j-1] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j] = cur
					way[j] = j0
				}
				if minv[j] < delta {
					delta = minv[j]
					j1 = j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
			if j0 == 0 {
				break
			}
		}
	}
	result := make([]int, n)
	for j := 1; j <= n; j++ {
		if p[j] != 0 {
			result[p[j]-1] = j - 1
		}
	}
	return result
}

func initialAssignments(oldInfo, newInfo *treeInfo, base [][]int, opts Options, useIDs bool) [][]int {
	nO, nN := len(oldInfo.active), len(newInfo.active)
	out := make([][]int, 0, 3)

	// Baseline: delete every old block and insert every new block.
	baseline := make([]int, nO)
	for i := range baseline {
		baseline[i] = -1
	}
	out = append(out, baseline)

	// Greedy in old preorder.
	greedy := make([]int, nO)
	for i := range greedy {
		greedy[i] = -1
	}
	used := make([]bool, nN)
	for i := 0; i < nO; i++ {
		bj := -1
		bc := opts.DeleteCost + opts.InsertCost
		for j := 0; j < nN; j++ {
			if used[j] {
				continue
			}
			c := base[i][j]
			if parentMovedForAssign(oldInfo, newInfo, greedy, i, j, useIDs, false) {
				c += opts.MoveCost
			}
			if c < bc || (c == bc && bj < 0) {
				bc, bj = c, j
			}
		}
		if bj >= 0 {
			greedy[i] = bj
			used[bj] = true
		}
	}
	out = append(out, greedy)

	if nO == 0 || nN == 0 {
		return out
	}

	// Hungarian assignment with dummy rows/columns. This is exact for an
	// additive parent-key cost and usually supplies a tight incumbent before
	// the parent-aware branch-and-bound starts.
	size := nO + nN
	matrix := make([][]int, size)
	const inf = int(^uint(0) >> 2)
	for i := range matrix {
		matrix[i] = make([]int, size)
		for j := range matrix[i] {
			matrix[i][j] = inf
		}
	}
	for i := 0; i < nO; i++ {
		for j := 0; j < nN; j++ {
			c := base[i][j]
			if oldInfo.active[i].parentKey(useIDs) != newInfo.active[j].parentKey(useIDs) {
				c += opts.MoveCost
			}
			matrix[i][j] = c
		}
		for j := nN; j < size; j++ {
			matrix[i][j] = opts.DeleteCost
		}
	}
	for i := nO; i < size; i++ {
		for j := 0; j < nN; j++ {
			matrix[i][j] = opts.InsertCost
		}
		for j := nN; j < size; j++ {
			matrix[i][j] = 0
		}
	}
	cols := hungarianAssign(matrix)
	hung := make([]int, nO)
	for i := 0; i < nO; i++ {
		if cols[i] < nN {
			hung[i] = cols[i]
		} else {
			hung[i] = -1
		}
	}
	out = append(out, hung)
	return out
}

// parentMovedForAssign is used while constructing greedy incumbents and final
// plans. Parents are earlier in preorder, so an unassigned processed parent is
// a deleted parent and therefore means the child moved. The structural-key
// fallback is only needed if this helper is called before a parent is visited.
func parentMovedForAssign(oldInfo, newInfo *treeInfo, assign []int, i, j int, useIDs, final bool) bool {
	oldParent := oldInfo.active[i].activeParent
	newParent := newInfo.active[j].activeParent
	if oldParent < 0 && newParent < 0 {
		return false
	}
	if oldParent < 0 || newParent < 0 {
		return true
	}
	if oldParent < i {
		return assign[oldParent] != newParent
	}
	if final {
		return true
	}
	return oldInfo.active[oldParent].parentKey(useIDs) != newInfo.active[newParent].parentKey(useIDs)
}

// evaluateAssignment returns the exact parent-aware cost of an assignment.
func evaluateAssignment(oldInfo, newInfo *treeInfo, base [][]int, assign []int, opts Options) int {
	nO, nN := len(oldInfo.active), len(newInfo.active)
	cost := 0
	oldToNew := assign
	for i := 0; i < nO; i++ {
		if oldToNew[i] < 0 {
			cost += opts.DeleteCost
			continue
		}
		cost += base[i][oldToNew[i]]
		if parentMovedForAssign(oldInfo, newInfo, oldToNew, i, oldToNew[i], false, true) {
			cost += opts.MoveCost
		}
	}
	usedNew := make([]bool, nN)
	for _, j := range oldToNew {
		if j >= 0 {
			usedNew[j] = true
			if j >= nN {
				panic("assignment out of range")
			}
		}
	}
	for j := 0; j < nN; j++ {
		if !usedNew[j] {
			cost += opts.InsertCost
		}
	}
	return cost
}

func betterAssignment(oldInfo, newInfo *treeInfo, base [][]int, opts Options, a, b []int, aCost, bCost int) ([]int, int) {
	if bCost < aCost {
		return append([]int(nil), b...), bCost
	}
	if bCost > aCost {
		return a, aCost
	}
	// Stable tie-break: prefer the assignment whose matched new indices are
	// smaller in preorder, which keeps duplicate paragraphs in reading order.
	if assignmentSequenceLess(a, b) {
		return append([]int(nil), a...), aCost
	}
	return b, bCost
}

func assignmentSequenceLess(a, b []int) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		ai, bi := a[i], b[i]
		if ai < 0 {
			ai = int(^uint(0) >> 2)
		}
		if bi < 0 {
			bi = int(^uint(0) >> 2)
		}
		if ai != bi {
			return ai < bi
		}
	}
	return len(a) < len(b)
}

func chooseIncumbent(oldInfo, newInfo *treeInfo, base [][]int, opts Options, useIDs bool) ([]int, int) {
	candidates := initialAssignments(oldInfo, newInfo, base, opts, useIDs)
	var best []int
	bestCost := 0
	for k, a := range candidates {
		c := evaluateAssignment(oldInfo, newInfo, base, a, opts)
		if k == 0 {
			best, bestCost = append([]int(nil), a...), c
			continue
		}
		best, bestCost = betterAssignment(oldInfo, newInfo, base, opts, best, a, bestCost, c)
	}
	return best, bestCost
}
