package suppress

import "fmt"

// enumerateLimit 是穷举核对允许收集的可行表数量安全上限。4×4、小值域下
// 通常远低于此值；超出则返回 ErrDomainTooLarge，提示宿主改用流判据。
const enumerateLimit = 50000

// Enumerate 穷举满足 Problem 上下界与行列和的全部完整可行表。
//
// 它按行优先顺序逐单元回溯，利用已填行的剩余和与已填列的剩余和剪枝。
// limit <= 0 时使用内置安全上限；可行表数量超过上限时返回
// ErrDomainTooLarge 与已收集的部分结果。该函数用于在小值域上独立核对
// Solve 的流/环判据，不作为大表的默认路径。
func Enumerate(p *Problem, limit int) ([][][]int, error) {
	n, err := normalize(p)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = enumerateLimit
	}

	cells := n.rows * n.cols
	table := make([][]int, n.rows)
	for i := range table {
		table[i] = make([]int, n.cols)
	}
	rowRemaining := append([]int(nil), n.rowSums...)
	colRemaining := append([]int(nil), n.colSums...)

	var out [][][]int
	var walk func(pos int) bool
	walk = func(pos int) bool {
		if pos == cells {
			for _, v := range rowRemaining {
				if v != 0 {
					return true
				}
			}
			for _, v := range colRemaining {
				if v != 0 {
					return true
				}
			}
			copyTable := make([][]int, n.rows)
			for i := range table {
				copyTable[i] = append([]int(nil), table[i]...)
			}
			out = append(out, copyTable)
			return len(out) < limit
		}
		i, j := pos/n.cols, pos%n.cols
		lastInRow := j == n.cols-1
		lastInCol := i == n.rows-1
		for v := n.lower[i][j]; v <= n.upper[i][j]; v++ {
			if v > rowRemaining[i] || v > colRemaining[j] {
				break
			}
			if lastInRow && v != rowRemaining[i] {
				continue
			}
			if lastInCol && v != colRemaining[j] {
				continue
			}
			table[i][j] = v
			rowRemaining[i] -= v
			colRemaining[j] -= v
			more := walk(pos + 1)
			rowRemaining[i] += v
			colRemaining[j] += v
			if !more {
				return false
			}
		}
		return true
	}
	walk(0)

	if len(out) >= limit {
		return out, ErrDomainTooLarge
	}
	return out, nil
}

// Verify 用穷举小值域的方式权威核对一份抑制方案。
//
// 它枚举全部可行表，并逐个敏感单元检查：是否存在两张在抑制集合之外取值
// 完全相同、而在该敏感单元上取值不同的可行表。全部满足时返回 nil。
// limit <= 0 时使用内置安全上限；值域过大时返回 ErrDomainTooLarge。
func Verify(p *Problem, plan *Plan, limit int) error {
	n, err := normalize(p)
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("%w: Plan 为 nil", ErrInvalidProblem)
	}
	tables, err := Enumerate(p, limit)
	if err != nil && err != ErrDomainTooLarge {
		return err
	}
	if err == ErrDomainTooLarge {
		return err
	}

	hid := buildHidden(n.rows, n.cols, n.sensitive, plan.Extra)
	expectedCost := 0
	for _, cell := range plan.Extra {
		if isSensitiveCell(n.sensitive, cell) {
			return fmt.Errorf("%w: 额外抑制单元 (%d,%d) 与敏感单元重合", ErrInvalidProblem, cell.Row, cell.Col)
		}
		expectedCost += n.cost[cell.Row][cell.Col]
	}
	if expectedCost != plan.TotalCost {
		return fmt.Errorf("%w: TotalCost=%d 与 Extra 实际成本 %d 不符", ErrInvalidProblem, plan.TotalCost, expectedCost)
	}
	for _, target := range n.sensitive {
		if !enumerationProtects(tables, hid, target) {
			return fmt.Errorf("%w: 敏感单元 (%d,%d) 在穷举可行表中只有一个可行取值",
				ErrUnprotectable, target.Row, target.Col)
		}
	}
	for _, witness := range plan.Witnesses {
		if err := validateWitness(n, hid, witness); err != nil {
			return err
		}
	}
	return nil
}

func isSensitiveCell(sensitive []Cell, cell Cell) bool {
	for _, s := range sensitive {
		if s == cell {
			return true
		}
	}
	return false
}

// validateWitness 核对两份见证各自满足边界与行列和、在 H 之外一致，且
// 敏感单元取值严格不同。
func validateWitness(n *normalized, hid [][]bool, witness WitnessSet) error {
	target := witness.Sensitive
	if !isSensitiveCell(n.sensitive, target) {
		return fmt.Errorf("%w: 见证目标 (%d,%d) 不是敏感单元", ErrInvalidProblem, target.Row, target.Col)
	}
	if !validTable(n, witness.A) || !validTable(n, witness.B) {
		return fmt.Errorf("%w: 敏感单元 (%d,%d) 的见证不满足边界或行列和", ErrInvalidProblem, target.Row, target.Col)
	}
	for i := 0; i < n.rows; i++ {
		for j := 0; j < n.cols; j++ {
			if !hid[i][j] && witness.A[i][j] != witness.B[i][j] {
				return fmt.Errorf("%w: 单元 (%d,%d) 未抑制却在两份见证中取值不同", ErrInvalidProblem, i, j)
			}
		}
	}
	if witness.A[target.Row][target.Col] >= witness.B[target.Row][target.Col] {
		return fmt.Errorf("%w: 敏感单元 (%d,%d) 的两份见证未给出严格不同的两个值",
			ErrInvalidProblem, target.Row, target.Col)
	}
	return nil
}

func validTable(n *normalized, table [][]int) bool {
	if len(table) != n.rows {
		return false
	}
	for i := 0; i < n.rows; i++ {
		if len(table[i]) != n.cols {
			return false
		}
		rowTotal := 0
		for j := 0; j < n.cols; j++ {
			v := table[i][j]
			if v < n.lower[i][j] || v > n.upper[i][j] {
				return false
			}
			rowTotal += v
		}
		if rowTotal != n.rowSums[i] {
			return false
		}
	}
	for j := 0; j < n.cols; j++ {
		colTotal := 0
		for i := 0; i < n.rows; i++ {
			colTotal += table[i][j]
		}
		if colTotal != n.colSums[j] {
			return false
		}
	}
	return true
}

// enumerationProtects 以公开单元（H 之外）取值为指纹分组，检查 target 是否
// 在某个指纹下出现至少两个不同取值。
func enumerationProtects(tables [][][]int, hid [][]bool, target Cell) bool {
	r, c := len(hid), len(hid[0])
	valuesByFingerprint := make(map[string]map[int]bool)
	for _, table := range tables {
		values := make([]int, 0, r*c)
		for i := 0; i < r; i++ {
			for j := 0; j < c; j++ {
				if !hid[i][j] {
					values = append(values, table[i][j])
				}
			}
		}
		key := string(intsToBytes(values))
		set, ok := valuesByFingerprint[key]
		if !ok {
			set = make(map[int]bool)
			valuesByFingerprint[key] = set
		}
		targetValue := table[target.Row][target.Col]
		if len(set) > 0 && !set[targetValue] {
			return true
		}
		set[targetValue] = true
	}
	return false
}

func intsToBytes(values []int) []byte {
	// 小值域下逐值编码为长度前缀加 8 字节大端整数，避免分隔符歧义。
	out := make([]byte, 0, len(values)*9)
	for _, v := range values {
		out = append(out, byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
			byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
	return out
}
