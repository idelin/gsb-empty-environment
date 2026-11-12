package main

import "sort"

type Enumerated struct {
	RowToCol []int
	Cost     float64
}

// enumerateAssignments exhaustively lists one-to-one assignments,
// including partial ones when rows outnumber columns.
func enumerateAssignments(costs [][]float64) []Enumerated {
	rowCount := len(costs)
	if rowCount == 0 {
		return []Enumerated{{RowToCol: []int{}, Cost: 0}}
	}
	colCount := len(costs[0])
	results := []Enumerated{}
	rowToCol := make([]int, rowCount)
	for i := range rowToCol {
		rowToCol[i] = -1
	}
	usedCols := make([]bool, colCount)

	var visit func(row int, accumulated float64)
	visit = func(row int, accumulated float64) {
		if row == rowCount {
			snapshot := append([]int(nil), rowToCol...)
			results = append(results, Enumerated{snapshot, accumulated})
			return
		}
		if colCount < rowCount {
			visit(row+1, accumulated)
		}
		for col := 0; col < colCount; col++ {
			if usedCols[col] || costs[row][col] >= forbiddenCost/2 {
				continue
			}
			usedCols[col] = true
			rowToCol[row] = col
			visit(row+1, accumulated+costs[row][col])
			rowToCol[row] = -1
			usedCols[col] = false
		}
	}
	visit(0, 0)
	sort.Slice(results, func(i, j int) bool { return results[i].Cost < results[j].Cost })
	return results
}

func bestAndSecond(costs [][]float64) (best, second *Enumerated) {
	all := enumerateAssignments(costs)
	if len(all) == 0 {
		return nil, nil
	}
	best = &all[0]
	for i := range all {
		candidate := &all[i]
		different := false
		for row, col := range candidate.RowToCol {
			if col != best.RowToCol[row] {
				different = true
				break
			}
		}
		if candidate.Cost > best.Cost || different {
			second = candidate
			return
		}
	}
	return best, nil
}
