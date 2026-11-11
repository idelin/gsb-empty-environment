package hpastar

import (
	"math"
	"math/rand"
	"testing"
)

// randomGrid builds a grid whose cells are independently blocked with the
// given probability. A border of walkable cells is kept so every map has a
// chance of long-range connectivity.
func randomGrid(rng *rand.Rand, w, h int, blockedRate float64) *Grid {
	g := NewGrid(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x == 0 || y == 0 || x == w-1 || y == h-1 {
				continue
			}
			if rng.Float64() < blockedRate {
				g.SetBlocked(x, y, true)
			}
		}
	}
	return g
}

func randomWalkableCell(rng *rand.Rand, g *Grid) Cell {
	for {
		c := Cell{X: rng.Intn(g.W), Y: rng.Intn(g.H)}
		if !g.Blocked(c.X, c.Y) {
			return c
		}
	}
}

// validatePath checks that the path starts and ends correctly, every step is
// a legal non-corner-cutting move, and the accumulated cost matches.
func validatePath(t *testing.T, g *Grid, path []Cell, start, goal Cell, wantCost float64) {
	t.Helper()
	if len(path) < 2 {
		t.Fatalf("path too short: %v", path)
	}
	if path[0] != start {
		t.Fatalf("path starts at %v, want %v", path[0], start)
	}
	if path[len(path)-1] != goal {
		t.Fatalf("path ends at %v, want %v", path[len(path)-1], goal)
	}
	var got float64
	seen := map[Cell]bool{path[0]: true}
	for i := 1; i < len(path); i++ {
		prev, cur := path[i-1], path[i]
		if g.Blocked(cur.X, cur.Y) {
			t.Fatalf("path enters blocked cell %v", cur)
		}
		dx, dy := absInt(cur.X-prev.X), absInt(cur.Y-prev.Y)
		if dx > 1 || dy > 1 || dx+dy == 0 {
			t.Fatalf("illegal step %v -> %v", prev, cur)
		}
		if dx == 1 && dy == 1 {
			if g.Blocked(prev.X, cur.Y) && g.Blocked(cur.X, prev.Y) {
				t.Fatalf("diagonal step cuts a corner: %v -> %v", prev, cur)
			}
		}
		got += moveCost(prev, cur)
		seen[cur] = true
	}
	if math.Abs(got-wantCost) > 1e-9 {
		t.Fatalf("path cost %.6f disagrees with reported cost %.6f", got, wantCost)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestOpenGridSanity(t *testing.T) {
	g := NewGrid(32, 24)
	hier := NewHierarchy(g, 8)

	start := Cell{X: 2, Y: 3}
	goal := Cell{X: 30, Y: 21}
	hpPath, hpCost, _, ok := hier.FindPath(start, goal)
	if !ok {
		t.Fatal("expected path on open grid")
	}
	validatePath(t, g, hpPath, start, goal, hpCost)

	_, fullCost, fullOK := FullAStar(g, start, goal)
	if !fullOK {
		t.Fatal("full A* failed on open grid")
	}
	if math.Abs(hpCost-fullCost) > 1e-9 {
		t.Fatalf("HPA cost %.6f != full A* cost %.6f", hpCost, fullCost)
	}

	// Blocked endpoints must fail.
	g.SetBlocked(5, 5, true)
	if _, _, _, ok := hier.FindPath(Cell{5, 5}, goal); ok {
		t.Fatal("path from blocked cell should fail")
	}
}

func TestBlockedAndReachability(t *testing.T) {
	// A solid wall splits the grid into two disconnected halves.
	g := NewGrid(24, 16)
	for y := 0; y < g.H; y++ {
		g.SetBlocked(12, y, true)
	}
	hier := NewHierarchy(g, 8)

	if _, _, _, ok := hier.FindPath(Cell{1, 1}, Cell{20, 1}); ok {
		t.Fatal("crossing a solid wall should be impossible")
	}
	changes := map[Cell]bool{{X: 12, Y: 8}: false}
	if err := hier.UpdateObstacles(changes); err != nil {
		t.Fatal(err)
	}
	path, cost, _, ok := hier.FindPath(Cell{1, 1}, Cell{20, 1})
	if !ok {
		t.Fatal("opening a door should restore connectivity")
	}
	validatePath(t, g, path, Cell{1, 1}, Cell{20, 1}, cost)
	_, fullCost, fullOK := FullAStar(g, Cell{1, 1}, Cell{20, 1})
	if !fullOK || math.Abs(cost-fullCost) > 1e-9 {
		t.Fatalf("post-update cost mismatch: ok=%v cost=%.6f full=%.6f", fullOK, cost, fullCost)
	}
}
