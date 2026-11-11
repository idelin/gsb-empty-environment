package suppress

// Solve 返回保护全部敏感单元的最低成本补充抑制方案。
//
// 搜索空间为“非敏感单元”的全部子集（4×4 至多 16 个候选），按行优先坐标
// 序做深度优先枚举。总成本严格更优才替换当前最优；成本相同时只接受坐标
// 序更小的集合，因此输出天然按坐标排序。找到方案后为每个敏感单元重建
// 两份完整表见证。
func Solve(p *Problem) (*Plan, error) {
	n, err := normalize(p)
	if err != nil {
		return nil, err
	}
	if _, ok := feasibleTable(n.lower, n.upper, n.rowSums, n.colSums); !ok {
		return nil, ErrInfeasible
	}

	isSensitive := make(map[Cell]bool, len(n.sensitive))
	for _, cell := range n.sensitive {
		isSensitive[cell] = true
	}
	var candidates []Cell
	for i := 0; i < n.rows; i++ {
		for j := 0; j < n.cols; j++ {
			cell := Cell{Row: i, Col: j}
			if !isSensitive[cell] {
				candidates = append(candidates, cell)
			}
		}
	}

	bestCost := int(^uint(0) >> 1)
	var bestExtra []Cell
	var chosen []Cell
	found := false

	var search func(idx, cost int)
	search = func(idx, cost int) {
		if cost > bestCost {
			return
		}
		if evaluates(n, chosen) {
			if !found || cost < bestCost || (cost == bestCost && lexSmaller(chosen, bestExtra)) {
				found = true
				bestCost = cost
				bestExtra = append([]Cell(nil), chosen...)
			}
		}
		for k := idx; k < len(candidates); k++ {
			cell := candidates[k]
			next := cost + n.cost[cell.Row][cell.Col]
			if next > bestCost {
				continue
			}
			chosen = append(chosen, cell)
			search(k+1, next)
			chosen = chosen[:len(chosen)-1]
		}
	}
	search(0, 0)

	if !found {
		return nil, ErrUnprotectable
	}

	hid := buildHidden(n.rows, n.cols, n.sensitive, bestExtra)
	witnesses := make([]WitnessSet, 0, len(n.sensitive))
	for _, target := range n.sensitive {
		low, high, ok := protectedWithWitnesses(n.lower, n.upper, n.rowSums, n.colSums, hid, target)
		if !ok {
			// 理论上不会发生：bestExtra 已通过同一判据的全部检查。
			return nil, ErrUnprotectable
		}
		witnesses = append(witnesses, WitnessSet{Sensitive: target, A: low, B: high})
	}

	return &Plan{
		Extra:     bestExtra,
		TotalCost: bestCost,
		Witnesses: witnesses,
	}, nil
}

// evaluates 判定当前额外抑制集合（连同全部敏感单元）能否保护每个敏感单元。
func evaluates(n *normalized, extra []Cell) bool {
	hid := buildHidden(n.rows, n.cols, n.sensitive, extra)
	for _, target := range n.sensitive {
		if !isProtected(n.lower, n.upper, n.rowSums, n.colSums, hid, target) {
			return false
		}
	}
	return true
}

// lexSmaller 按行优先坐标序比较两个已排序的单元集合：第一个不同坐标更小
// 则为 true；a 是 b 的真前缀也视为更小。
func lexSmaller(a, b []Cell) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return cellLess(a[i], b[i])
		}
	}
	return len(a) < len(b)
}

func cellLess(a, b Cell) bool {
	if a.Row != b.Row {
		return a.Row < b.Row
	}
	return a.Col < b.Col
}
