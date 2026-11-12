package main

import "math"

const forbiddenCost = 1e12

type Assignment struct {
	RowToCol []int
	Cost     float64
	Feasible bool
}

// minCostAssignment matches every row to a distinct column.
// Requires len(columns) >= len(rows).
func minCostAssignment(costs [][]float64) Assignment {
	rowCount := len(costs)
	if rowCount == 0 {
		return Assignment{RowToCol: []int{}, Cost: 0, Feasible: true}
	}
	colCount := len(costs[0])
	if colCount < rowCount {
		panic("minCostAssignment requires columns >= rows")
	}

	rowPotential := make([]float64, rowCount+1)
	colPotential := make([]float64, colCount+1)
	pairedCol := make([]int, colCount+1)
	pairedRow := make([]int, rowCount+1)

	for row := 1; row <= rowCount; row++ {
		pairedCol[0] = row
		currentCol := 0
		minValue := make([]float64, colCount+1)
		for i := range minValue {
			minValue[i] = math.Inf(1)
		}
		used := make([]bool, colCount+1)
		previousRow := make([]int, colCount+1)

		for {
			used[currentCol] = true
			currentRow := pairedCol[currentCol]
			delta := math.Inf(1)
			nextCol := 0

			for col := 1; col <= colCount; col++ {
				if used[col] {
					continue
				}
				slack := costs[currentRow-1][col-1] - rowPotential[currentRow] - colPotential[col]
				if slack < minValue[col] {
					minValue[col] = slack
					previousRow[col] = currentCol
				}
				if minValue[col] < delta {
					delta = minValue[col]
					nextCol = col
				}
			}

			for col := 0; col <= colCount; col++ {
				if used[col] {
					rowPotential[pairedCol[col]] += delta
					colPotential[col] -= delta
				} else {
					minValue[col] -= delta
				}
			}
			currentCol = nextCol
			if pairedCol[currentCol] == 0 {
				break
			}
		}

		col := currentCol
		for col != 0 {
			previous := previousRow[col]
			pairedCol[col] = pairedCol[previous]
			pairedRow[pairedCol[col]] = col
			col = previous
		}
	}

	rowToCol := make([]int, rowCount)
	for i := range rowToCol {
		rowToCol[i] = -1
	}
	total := 0.0
	feasible := true
	for row := 1; row <= rowCount; row++ {
		col := pairedRow[row]
		rowToCol[row-1] = col - 1
		edge := costs[row-1][col-1]
		total += edge
		if edge >= forbiddenCost/2 {
			feasible = false
		}
	}
	if !feasible {
		total = math.Inf(1)
	}
	return Assignment{rowToCol, total, feasible}
}

func minCostAssignmentAnyShape(costs [][]float64) Assignment {
	rowCount := len(costs)
	colCount := 0
	if rowCount > 0 {
		colCount = len(costs[0])
	}
	if rowCount <= colCount {
		return minCostAssignment(costs)
	}

	transposed := make([][]float64, colCount)
	for col := 0; col < colCount; col++ {
		transposed[col] = make([]float64, rowCount)
		for row := 0; row < rowCount; row++ {
			transposed[col][row] = costs[row][col]
		}
	}
	inner := minCostAssignment(transposed)
	rowToCol := make([]int, rowCount)
	for i := range rowToCol {
		rowToCol[i] = -1
	}
	for col := 0; col < colCount; col++ {
		originalRow := inner.RowToCol[col]
		if originalRow >= 0 {
			rowToCol[originalRow] = col
		}
	}
	return Assignment{rowToCol, inner.Cost, inner.Feasible}
}
