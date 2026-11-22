package disclosure

import (
	"fmt"
	"sort"
)

// validate compiles and validates the request. It returns a non-nil error only
// for malformed input; an actual table that violates an always-published
// constraint is a valid review result and is reported separately by Review.
func validate(req *Request) (*compiled, error) {
	if req == nil {
		return nil, fmt.Errorf("disclosure: nil request")
	}
	grid := req.Table.Cells
	if len(grid) == 0 || len(grid) > 4 {
		return nil, fmt.Errorf("disclosure: table must have 1..4 rows, got %d", len(grid))
	}
	cols := len(grid[0])
	if cols == 0 || cols > 4 {
		return nil, fmt.Errorf("disclosure: table must have 1..4 columns, got %d", cols)
	}
	n := len(grid) * cols
	cellMin := make([]int64, n)
	cellMax := make([]int64, n)
	actual := make([]int64, n)
	for r := range grid {
		if len(grid[r]) != cols {
			return nil, fmt.Errorf("disclosure: ragged table: row %d has %d cells, want %d", r, len(grid[r]), cols)
		}
		for c := range grid[r] {
			cell := grid[r][c]
			if cell.Min < 0 {
				return nil, fmt.Errorf("disclosure: cell (%d,%d) has negative lower bound %d", r, c, cell.Min)
			}
			if cell.Max < cell.Min {
				return nil, fmt.Errorf("disclosure: cell (%d,%d) bounds inverted: [%d,%d]", r, c, cell.Min, cell.Max)
			}
			if cell.Actual < cell.Min || cell.Actual > cell.Max {
				return nil, fmt.Errorf("disclosure: actual value %d of cell (%d,%d) is outside [%d,%d]", cell.Actual, r, c, cell.Min, cell.Max)
			}
			idx := r*cols + c
			cellMin[idx] = cell.Min
			cellMax[idx] = cell.Max
			actual[idx] = cell.Actual
		}
	}

	if req.Budget < 0 {
		return nil, fmt.Errorf("disclosure: negative budget %d", req.Budget)
	}

	seenGroups := make(map[string]bool)
	for _, g := range req.Groups {
		if g.ID == "" {
			return nil, fmt.Errorf("disclosure: group with empty ID")
		}
		if seenGroups[g.ID] {
			return nil, fmt.Errorf("disclosure: duplicate group ID %q", g.ID)
		}
		if g.Cost < 0 {
			return nil, fmt.Errorf("disclosure: group %q has negative cost %d", g.ID, g.Cost)
		}
		seenGroups[g.ID] = true
	}

	seenConstraints := make(map[string]bool)
	for i, con := range req.Constraints {
		label := con.ID
		if label == "" {
			label = fmt.Sprintf("#%d", i)
		}
		if con.ID != "" {
			if seenConstraints[con.ID] {
				return nil, fmt.Errorf("disclosure: duplicate constraint ID %q", con.ID)
			}
			seenConstraints[con.ID] = true
		}
		if len(con.Terms) == 0 {
			return nil, fmt.Errorf("disclosure: constraint %q has no terms", label)
		}
		for _, term := range con.Terms {
			if term.Row < 0 || term.Row >= len(grid) || term.Col < 0 || term.Col >= cols {
				return nil, fmt.Errorf("disclosure: constraint %q references cell (%d,%d) outside the table", label, term.Row, term.Col)
			}
			if term.Coeff == 0 {
				return nil, fmt.Errorf("disclosure: constraint %q has a zero coefficient on cell (%d,%d)", label, term.Row, term.Col)
			}
		}
		if con.GroupID != "" && !seenGroups[con.GroupID] {
			return nil, fmt.Errorf("disclosure: constraint %q references unknown group %q", label, con.GroupID)
		}
	}

	if len(req.Sensitive) == 0 {
		return nil, fmt.Errorf("disclosure: at least one sensitive cell is required")
	}
	seenSensitive := make(map[[2]int]bool)
	for _, sc := range req.Sensitive {
		if sc.Row < 0 || sc.Row >= len(grid) || sc.Col < 0 || sc.Col >= cols {
			return nil, fmt.Errorf("disclosure: sensitive cell (%d,%d) is outside the table", sc.Row, sc.Col)
		}
		key := [2]int{sc.Row, sc.Col}
		if seenSensitive[key] {
			return nil, fmt.Errorf("disclosure: sensitive cell (%d,%d) listed twice", sc.Row, sc.Col)
		}
		seenSensitive[key] = true
		if sc.K < 1 {
			return nil, fmt.Errorf("disclosure: sensitive cell (%d,%d) needs K >= 1, got %d", sc.Row, sc.Col, sc.K)
		}
	}

	groups := make([]SuppressionGroup, len(req.Groups))
	copy(groups, req.Groups)
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	groupIndex := make(map[string]int, len(groups))
	groupCost := make([]int64, len(groups))
	for i, g := range groups {
		groupIndex[g.ID] = i
		groupCost[i] = g.Cost
	}

	m := len(req.Constraints)
	constraintOf := make([]int, m)
	termRows := make([][]int, m)
	termCols := make([][]int, m)
	termCoeffs := make([][]int64, m)
	termRHS := make([]int64, m)
	for i, con := range req.Constraints {
		if con.GroupID == "" {
			constraintOf[i] = -1
		} else {
			constraintOf[i] = groupIndex[con.GroupID]
		}
		rows := make([]int, len(con.Terms))
		colsIdx := make([]int, len(con.Terms))
		coeffs := make([]int64, len(con.Terms))
		for j, term := range con.Terms {
			rows[j] = term.Row
			colsIdx[j] = term.Col
			coeffs[j] = term.Coeff
		}
		termRows[i] = rows
		termCols[i] = colsIdx
		termCoeffs[i] = coeffs
		termRHS[i] = con.RHS
	}

	sensIdx := make([]int, len(req.Sensitive))
	for i, sc := range req.Sensitive {
		sensIdx[i] = sc.Row*cols + sc.Col
	}

	enumCap := req.MaxEnumNodes
	if enumCap <= 0 {
		enumCap = defaultEnumCap
	}

	return &compiled{
		req:          req,
		rows:         len(grid),
		cols:         cols,
		cellMin:      cellMin,
		cellMax:      cellMax,
		actual:       actual,
		groups:       groups,
		groupIndex:   groupIndex,
		groupCost:    groupCost,
		constraints:  req.Constraints,
		constraintOf: constraintOf,
		termRows:     termRows,
		termCols:     termCols,
		termCoeffs:   termCoeffs,
		termRHS:      termRHS,
		sensitive:    req.Sensitive,
		sensIdx:      sensIdx,
		enumCap:      enumCap,
	}, nil
}

// checkActual evaluates the always-published constraints against the actual
// table. The returned constraint ID is empty when the table is consistent.
func (c *compiled) checkActual() (string, string) {
	for i := range c.constraints {
		if c.constraintOf[i] != -1 {
			continue
		}
		sum, ok := safeSum(c, i, c.actual)
		if !ok {
			return c.constraints[i].ID, explainActual(c, i, c.actual, sum, true)
		}
		if sum != c.termRHS[i] {
			return c.constraints[i].ID, explainActual(c, i, c.actual, sum, false)
		}
	}
	return "", ""
}

func safeSum(c *compiled, constraint int, vals []int64) (int64, bool) {
	var sum int64
	ok := true
	for j, coeff := range c.termCoeffs[constraint] {
		idx := c.termRows[constraint][j]*c.cols + c.termCols[constraint][j]
		s, good := mulAdd(sum, coeff, vals[idx])
		if !good {
			ok = false
		}
		sum = s
	}
	return sum, ok
}

// mulAdd returns acc + coeff*x, detecting int64 overflow.
func mulAdd(acc, coeff, x int64) (int64, bool) {
	product, ok := safeMul(coeff, x)
	if !ok {
		return 0, false
	}
	return safeAdd(acc, product)
}

func safeMul(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	r := a * b
	if r/a != b {
		return 0, false
	}
	return r, true
}

func safeAdd(a, b int64) (int64, bool) {
	r := a + b
	if (r^a) < 0 && (r^b) < 0 {
		return 0, false
	}
	return r, true
}

func explainActual(c *compiled, i int, vals []int64, sum int64, overflow bool) string {
	id := c.constraints[i].ID
	if id == "" {
		id = fmt.Sprintf("constraint #%d", i)
	}
	if overflow {
		return fmt.Sprintf("actual table violates %s: weighted sum overflows int64 instead of equalizing RHS %d", id, c.termRHS[i])
	}
	return fmt.Sprintf("actual table violates %s: weighted sum is %d, RHS is %d", id, sum, c.termRHS[i])
}
