package hpastar

import (
	"math"
	"math/rand"
	"testing"
)

// TestRandomMapsVsFullAStar is the core validation: across many randomized
// maps and queries, hierarchical reachability must match full A* exactly and
// the refined path cost must stay within 1.1x of the optimum (in fact the
// implementation is exact, so the ratio is 1.0).
func TestRandomMapsVsFullAStar(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	const mapCount, queriesPerMap = 24, 24

	for m := 0; m < mapCount; m++ {
		w := 24 + rng.Intn(33)
		h := 24 + rng.Intn(33)
		cs := 6 + rng.Intn(7)
		rate := 0.05 + rng.Float64()*0.25
		g := randomGrid(rng, w, h, rate)
		hier := NewHierarchy(g, cs)

		for q := 0; q < queriesPerMap; q++ {
			start := randomWalkableCell(rng, g)
			goal := randomWalkableCell(rng, g)

			hpPath, hpCost, _, hpOK := hier.FindPath(start, goal)
			fullPath, fullCost, fullOK := FullAStar(g, start, goal)

			if hpOK != fullOK {
				t.Fatalf("map %d query %d reachability mismatch: hpa=%v full=%v (%s rate=%.2f cs=%d)",
					m, q, hpOK, fullOK, gridLabel(w, h), rate, cs)
			}
			if !fullOK {
				continue
			}
			validatePath(t, g, hpPath, start, goal, hpCost)
			if hpCost > fullCost*1.1+1e-9 {
				t.Fatalf("path exceeds 1.1x optimum: hpa=%.6f full=%.6f ratio=%.4f",
					hpCost, fullCost, hpCost/fullCost)
			}
			if math.Abs(hpCost-fullCost) > 1e-9 {
				t.Fatalf("non-exact cost: hpa=%.6f full=%.6f diff=%.2e path=%v full=%v",
					hpCost, fullCost, hpCost-fullCost, hpPath, fullPath)
			}
		}
	}
}

// TestDynamicObstacleUpdates flips random cells between blocked and walkable,
// rebuilds only affected clusters, and verifies the live hierarchy keeps
// matching a freshly built hierarchy and full A*.
func TestDynamicObstacleUpdates(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	g := randomGrid(rng, 48, 40, 0.18)
	const cs = 8
	hier := NewHierarchy(g, cs)

	for round := 0; round < 24; round++ {
		changes := map[Cell]bool{}
		flips := 1 + rng.Intn(5)
		for i := 0; i < flips; i++ {
			cell := Cell{X: 1 + rng.Intn(g.W-2), Y: 1 + rng.Intn(g.H-2)}
			changes[cell] = !g.Blocked(cell.X, cell.Y)
		}
		before := hier.RebuildCount()
		if err := hier.UpdateObstacles(changes); err != nil {
			t.Fatal(err)
		}
		rebuilt := hier.RebuildCount() - before

		// At most the flipped clusters and their 3x3 neighbourhood are touched.
		upperBound := len(changedClustersWithNeighbours(g, changes, cs))
		if rebuilt > upperBound {
			t.Fatalf("round %d rebuilt %d clusters, expected <= %d", round, rebuilt, upperBound)
		}

		fresh := NewHierarchy(g, cs)
		for q := 0; q < 16; q++ {
			start := randomWalkableCell(rng, g)
			goal := randomWalkableCell(rng, g)

			hpPath, hpCost, _, hpOK := hier.FindPath(start, goal)
			_, freshCost, _, freshOK := fresh.FindPath(start, goal)
			_, fullCost, fullOK := FullAStar(g, start, goal)

			if hpOK != fullOK || freshOK != fullOK {
				t.Fatalf("round %d query %d reachability mismatch: hpa=%v fresh=%v full=%v",
					round, q, hpOK, freshOK, fullOK)
			}
			if !fullOK {
				continue
			}
			validatePath(t, g, hpPath, start, goal, hpCost)
			if hpCost > fullCost*1.1+1e-9 || math.Abs(hpCost-freshCost) > 1e-9 {
				t.Fatalf("round %d query %d cost mismatch: hpa=%.6f fresh=%.6f full=%.6f",
					round, q, hpCost, freshCost, fullCost)
			}
		}
	}
}

// TestSearchWorkComparison demonstrates that on large, cluttered maps the
// hierarchical search expands substantially fewer nodes than flat A*.
// (On nearly empty maps full A* is already cheap while every HPA* query
// still pays for two local insertion searches, so the benefit appears with
// realistic obstacle density and long-range queries.)
func TestSearchWorkComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	g := randomGrid(rng, 128, 128, 0.25)
	hier := NewHierarchy(g, 10)

	var hpaExpanded, fullExpanded, compared int
	for q := 0; q < 40; q++ {
		start := randomWalkableCell(rng, g)
		goal := randomWalkableCell(rng, g)
		_, _, hStats, hpOK := hier.FindPath(start, goal)
		_, _, fStats, fullOK := FullAStarWithStats(g, start, goal)
		if !hpOK || !fullOK {
			continue
		}
		hpaExpanded += hStats.Expanded
		fullExpanded += fStats.Expanded
		compared++
	}
	if compared == 0 {
		t.Fatal("no comparable queries")
	}
	ratio := float64(hpaExpanded) / float64(fullExpanded)
	t.Logf("queries=%d avg hpa expanded=%.1f full expanded=%.1f ratio=%.3f",
		compared, float64(hpaExpanded)/float64(compared),
		float64(fullExpanded)/float64(compared), ratio)
	if ratio >= 0.6 {
		t.Fatalf("hierarchical search expanded %.1f%% of full A* nodes, expected < 60%%", ratio*100)
	}
}

func gridLabel(w, h int) string {
	return "grid " + itoa(w) + "x" + itoa(h)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func changedClustersWithNeighbours(g *Grid, changes map[Cell]bool, cs int) map[[2]int]bool {
	out := map[[2]int]bool{}
	cols := (g.W + cs - 1) / cs
	rows := (g.H + cs - 1) / cs
	for cell := range changes {
		cx, cy := cell.X/cs, cell.Y/cs
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				ncx, ncy := cx+dx, cy+dy
				if ncx >= 0 && ncx < cols && ncy >= 0 && ncy < rows {
					out[[2]int{ncx, ncy}] = true
				}
			}
		}
	}
	return out
}
