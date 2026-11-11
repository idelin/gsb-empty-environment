package suppress

import (
	"math/rand"
	"testing"
)

func cellsEqual(a, b []Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// bruteForceOracle 穷举全部非敏感单元子集，返回与 Solve 相同规则下的
// 坐标序最小最低成本集合；不存在时返回 nil。
func bruteForceOracle(t *testing.T, p *Problem) []Cell {
	t.Helper()
	if _, err := normalize(p); err != nil {
		t.Fatalf("normalize 失败: %v", err)
	}
	tables, err := Enumerate(p, 0)
	if err != nil {
		t.Fatalf("Enumerate 失败: %v", err)
	}
	return bruteForceFromTables(t, p, tables)
}

func bruteForceFromTables(t *testing.T, p *Problem, tables [][][]int) []Cell {
	t.Helper()
	n, err := normalize(p)
	if err != nil {
		t.Fatalf("normalize 失败: %v", err)
	}
	isSensitive := map[Cell]bool{}
	for _, cell := range n.sensitive {
		isSensitive[cell] = true
	}
	var candidates []Cell
	for i := 0; i < n.rows; i++ {
		for j := 0; j < n.cols; j++ {
			cell := Cell{i, j}
			if !isSensitive[cell] {
				candidates = append(candidates, cell)
			}
		}
	}

	bestCost := int(^uint(0) >> 1)
	var best []Cell
	var chosen []Cell
	var walk func(k, cost int)
	walk = func(k, cost int) {
		if cost > bestCost {
			return
		}
		hid := buildHidden(n.rows, n.cols, n.sensitive, chosen)
		ok := true
		for _, target := range n.sensitive {
			if !enumerationProtects(tables, hid, target) {
				ok = false
				break
			}
		}
		if ok && (cost < bestCost || (cost == bestCost && lexSmaller(chosen, best))) {
			bestCost = cost
			best = append([]Cell(nil), chosen...)
		}
		for idx := k; idx < len(candidates); idx++ {
			cell := candidates[idx]
			chosen = append(chosen, cell)
			walk(idx+1, cost+n.cost[cell.Row][cell.Col])
			chosen = chosen[:len(chosen)-1]
		}
	}
	walk(0, 0)
	return best
}

// bruteForceExists 仅判断是否存在任意可行的补充抑制集合。
func bruteForceExists(p *Problem, tables [][][]int) bool {
	n, err := normalize(p)
	if err != nil {
		return false
	}
	isSensitive := map[Cell]bool{}
	for _, cell := range n.sensitive {
		isSensitive[cell] = true
	}
	var candidates []Cell
	for i := 0; i < n.rows; i++ {
		for j := 0; j < n.cols; j++ {
			cell := Cell{i, j}
			if !isSensitive[cell] {
				candidates = append(candidates, cell)
			}
		}
	}
	var chosen []Cell
	found := false
	var walk func(k int)
	walk = func(k int) {
		if found {
			return
		}
		hid := buildHidden(n.rows, n.cols, n.sensitive, chosen)
		ok := true
		for _, target := range n.sensitive {
			if !enumerationProtects(tables, hid, target) {
				ok = false
				break
			}
		}
		if ok {
			found = true
			return
		}
		for idx := k; idx < len(candidates); idx++ {
			chosen = append(chosen, candidates[idx])
			walk(idx + 1)
			chosen = chosen[:len(chosen)-1]
		}
	}
	walk(0)
	return found
}

// TestFuzzAgainstEnumeration 用随机小问题对拍流/环判据与穷举预言机。
func TestFuzzAgainstEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iter := 0; iter < 300; iter++ {
		p := randomProblem(rng)
		plan, err := Solve(p)
		tables, enumErr := Enumerate(p, 0)
		if enumErr == ErrDomainTooLarge {
			t.Fatalf("随机用例值域超出穷举上限: %+v", p)
		}
		if err != nil {
			if !isExpectedFailure(err) {
				t.Fatalf("意外错误: %v", err)
			}
			if isUnprotectable(err) && bruteForceExists(p, tables) {
				t.Fatalf("误报不可保护: %+v", p)
			}
			continue
		}
		oracle := bruteForceFromTables(t, p, tables)
		if oracle == nil {
			t.Fatalf("求解器给出方案但预言机找不到: %+v", p)
		}
		if !cellsEqual(plan.Extra, oracle) {
			t.Fatalf("迭代 %d 方案不一致: Solve=%v oracle=%v", iter, plan.Extra, oracle)
		}
		if err := Verify(p, plan, 0); err != nil {
			t.Fatalf("Verify 失败: %v", err)
		}
	}
}

func isExpectedFailure(err error) bool {
	return isUnprotectable(err) || isInfeasible(err)
}

func isUnprotectable(err error) bool {
	for err != nil {
		if err == ErrUnprotectable {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func isInfeasible(err error) bool {
	for err != nil {
		if err == ErrInfeasible {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func randomProblem(rng *rand.Rand) *Problem {
	dims := []int{2, 3, 4}
	r := dims[rng.Intn(len(dims))]
	c := dims[rng.Intn(len(dims))]

	base := make([][]int, r)
	rowSums := make([]int, r)
	colSums := make([]int, c)
	for i := 0; i < r; i++ {
		base[i] = make([]int, c)
		for j := 0; j < c; j++ {
			base[i][j] = rng.Intn(3)
			rowSums[i] += base[i][j]
			colSums[j] += base[i][j]
		}
	}
	lo, up := make([][]int, r), make([][]int, r)
	cost := make([][]int, r)
	for i := 0; i < r; i++ {
		lo[i] = make([]int, c)
		up[i] = make([]int, c)
		cost[i] = make([]int, c)
		for j := 0; j < c; j++ {
			up[i][j] = base[i][j] + rng.Intn(3)
			cost[i][j] = 1 + rng.Intn(4)
		}
	}
	sensitiveCount := 1 + rng.Intn(2)
	seen := map[Cell]bool{}
	var sensitive []Cell
	for len(sensitive) < sensitiveCount {
		cell := Cell{rng.Intn(r), rng.Intn(c)}
		if !seen[cell] {
			seen[cell] = true
			sensitive = append(sensitive, cell)
		}
	}
	return &Problem{
		Lower:     lo,
		Upper:     up,
		RowSums:   rowSums,
		ColSums:   colSums,
		Sensitive: sensitive,
		Cost:      cost,
	}
}
