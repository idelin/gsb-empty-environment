package hpastar

import (
	"math"
	"testing"
)

// TestClusterCornerDiagonal uses an open 2x2 cluster map (no border walls,
// so every boundary cell is an entrance) and verifies that legal diagonal
// moves crossing the shared cluster corner are represented in the abstract
// graph. If diagonal inter-cluster edges were missing, the hierarchical
// search would only find Manhattan detours and lose exactness.
func TestClusterCornerDiagonal(t *testing.T) {
	const cs = 8
	g := NewGrid(16, 16)
	hier := NewHierarchy(g, cs)

	start := Cell{X: cs - 1, Y: cs - 1}
	goal := Cell{X: cs, Y: cs}
	hpPath, hpCost, _, ok := hier.FindPath(start, goal)
	if !ok {
		t.Fatal("expected a diagonal corner path")
	}
	validatePath(t, g, hpPath, start, goal, hpCost)

	_, fullCost, fullOK := FullAStar(g, start, goal)
	if !fullOK {
		t.Fatal("full A* found no path")
	}
	if math.Abs(hpCost-fullCost) > 1e-9 {
		t.Fatalf("corner diagonal not exact: hpa=%.6f full=%.6f path=%v", hpCost, fullCost, hpPath)
	}
	if hpCost > fullCost*1.1 {
		t.Fatalf("corner path exceeds 1.1x bound: ratio=%.4f", hpCost/fullCost)
	}

	// A blocked corner must make the diagonal illegal for both searches,
	// and the hierarchical result must stay exact.
	g.SetBlocked(cs, cs-1, true)
	g.SetBlocked(cs-1, cs, true)
	hier.Rebuild()
	hpPath2, hpCost2, _, hpOK2 := hier.FindPath(start, goal)
	_, fullCost2, fullOK2 := FullAStar(g, start, goal)
	if hpOK2 != fullOK2 {
		t.Fatalf("blocked-corner reachability mismatch: hpa=%v full=%v", hpOK2, fullOK2)
	}
	if fullOK2 {
		validatePath(t, g, hpPath2, start, goal, hpCost2)
		if math.Abs(hpCost2-fullCost2) > 1e-9 {
			t.Fatalf("blocked-corner cost mismatch: hpa=%.6f full=%.6f", hpCost2, fullCost2)
		}
	}
}

func TestSingleCellAndSameCluster(t *testing.T) {
	g := NewGrid(20, 20)
	hier := NewHierarchy(g, 5)

	// Zero-length query.
	path, cost, _, ok := hier.FindPath(Cell{3, 3}, Cell{3, 3})
	if !ok || cost != 0 || len(path) != 1 || path[0] != (Cell{3, 3}) {
		t.Fatalf("zero-length query returned %v cost=%v ok=%v", path, cost, ok)
	}

	// Same-cluster query with obstacles inside.
	if err := hier.UpdateObstacles(map[Cell]bool{
		{X: 6, Y: 5}: true,
		{X: 6, Y: 6}: true,
	}); err != nil {
		t.Fatal(err)
	}
	start, goal := Cell{1, 1}, Cell{9, 9}
	hpPath, hpCost, _, hpOK := hier.FindPath(start, goal)
	_, fullCost, fullOK := FullAStar(g, start, goal)
	if hpOK != fullOK {
		t.Fatalf("reachability mismatch: hpa=%v full=%v", hpOK, fullOK)
	}
	validatePath(t, g, hpPath, start, goal, hpCost)
	if math.Abs(hpCost-fullCost) > 1e-9 {
		t.Fatalf("same-cluster cost mismatch: %.6f vs %.6f", hpCost, fullCost)
	}
}
