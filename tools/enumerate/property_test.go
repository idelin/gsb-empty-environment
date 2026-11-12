package main

import (
	"math"
	"math/rand"
	"testing"
)

// Random small matrices (both rectangular orientations, with forbidden
// edges): Hungarian optimum must equal exhaustive enumeration.
func TestHungarianMatchesEnumerationRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iteration := 0; iteration < 400; iteration++ {
		rows := 1 + rng.Intn(4)
		cols := 1 + rng.Intn(4)
		costs := make([][]float64, rows)
		for row := 0; row < rows; row++ {
			costs[row] = make([]float64, cols)
			for col := 0; col < cols; col++ {
				costs[row][col] = float64(rng.Intn(200)) + rng.Float64()
				if rng.Float64() < 0.25 {
					costs[row][col] = forbiddenCost
				}
			}
		}

		enumerated := enumerateAssignments(costs)
		assignment := minCostAssignmentAnyShape(costs)

		if cols >= rows {
			// Feasibility is a Hall-condition property; let enumeration be
			// the oracle by finding the first assignment covering every row.
			var fullBest *Enumerated
			for i := range enumerated {
				candidate := &enumerated[i]
				complete := true
				for _, col := range candidate.RowToCol {
					if col < 0 {
						complete = false
					}
				}
				if complete {
					fullBest = candidate
					break
				}
			}
			feasible := fullBest != nil
			if feasible != assignment.Feasible {
				t.Fatalf("iteration %d: feasibility mismatch want %v got %v", iteration, feasible, assignment.Feasible)
			}
			if !feasible {
				continue
			}
			if math.Abs(assignment.Cost-fullBest.Cost) > 1e-9 {
				t.Fatalf("iteration %d: hungarian %v != enumeration %v", iteration, assignment.Cost, fullBest.Cost)
			}
		} else {
			// Rows outnumber columns: the solver matches every column to a
			// distinct row; take the minimum enumeration that uses all cols.
			var bestPartial *Enumerated
			for i := range enumerated {
				candidate := &enumerated[i]
				used := map[int]bool{}
				matchedCols := 0
				for _, col := range candidate.RowToCol {
					if col >= 0 && !used[col] {
						used[col] = true
						matchedCols++
					}
				}
				if matchedCols == cols {
					bestPartial = candidate
					break
				}
			}
			if bestPartial == nil {
				continue
			}
			if math.Abs(assignment.Cost-bestPartial.Cost) > 1e-9 {
				t.Fatalf("iteration %d (partial): hungarian %v != enumeration %v", iteration, assignment.Cost, bestPartial.Cost)
			}
		}
	}
}

// Second-best via edge-blocking reruns must match enumeration.
func TestSecondBestMatchesEnumerationRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	for iteration := 0; iteration < 200; iteration++ {
		dim := 2 + rng.Intn(3)
		costs := make([][]float64, dim)
		for row := 0; row < dim; row++ {
			costs[row] = make([]float64, dim)
			for col := 0; col < dim; col++ {
				costs[row][col] = float64(rng.Intn(100)) + rng.Float64()
			}
		}
		best, second := bestAndSecond(costs)
		if best == nil || second == nil {
			continue
		}
		assignment := minCostAssignment(costs)
		rerun := math.Inf(1)
		for row := range costs {
			alt := assignmentWithoutEdge(costs, row, assignment.RowToCol[row])
			if alt < rerun && alt > assignment.Cost+1e-9 {
				rerun = alt
			}
		}
		if math.Abs(rerun-second.Cost) > 1e-9 {
			t.Fatalf("iteration %d: rerun %v != enumeration second %v", iteration, rerun, second.Cost)
		}
	}
}
