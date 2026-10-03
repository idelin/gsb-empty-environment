// Command hexdemo demonstrates the hexagonal hierarchical index:
// encoding a GPS coordinate, topological k-ring retrieval, the
// offline integrity inspector, and the invariant asserter.
package main

import (
	"fmt"

	"hexgrid"
)

func main() {
	// --- Acceptance: encode a GPS coordinate at resolution 8. ---
	lat, lng := 39.9042, 116.4074 // Beijing
	const res = 8
	cell, err := hexgrid.LatLngToCell(lat, lng, res)
	if err != nil {
		panic(err)
	}
	fmt.Printf("GPS (%.4f, %.4f) @ res %d -> cell %s\n", lat, lng, res, cell)
	clat, clng := cell.CenterLatLng()
	fmt.Printf("cell center: (%.6f, %.6f), face %d\n", clat, clng, cell.Face())

	// --- Acceptance: k=3 topological ring, closed and hole-free. ---
	const k = 3
	ring := hexgrid.KRing(cell, k)
	fmt.Printf("k=%d ring: %d cells (closed-form expectation %d)\n", k, len(ring), hexgrid.ExpectedKRingSize(k))
	for _, v := range hexgrid.AssertKRing(cell, k) {
		fmt.Println("  INVARIANT VIOLATION:", v)
	}
	fmt.Println("  ring invariants: unique, contiguous, closed, no holes")

	// --- Hierarchy: parent/child scaling. ---
	parent, _ := cell.Parent()
	children, _ := parent.Children()
	fmt.Printf("parent @ res %d: %s, children: %d, contains original cell: %v\n",
		parent.Resolution(), parent, len(children), contains(children, cell))

	// --- Offline integrity inspection. ---
	u4, _ := hexgrid.Universe(4)
	u3, _ := hexgrid.Universe(3)
	clean := hexgrid.NewSnapshot(map[int][]hexgrid.Cell{3: u3, 4: u4})
	rep := hexgrid.Inspect(clean, hexgrid.InspectOptions{})
	fmt.Printf("inspect clean universe (res 3+4, %d cells): ok=%v, components=%d, skew max/mean=%.2f\n",
		len(u3)+len(u4), rep.OK(), rep.Components, float64(rep.Skew.Max)/rep.Skew.Mean)

	// Corrupted snapshot: drop one parent (dangling refs), cut the
	// shell around an island (orphan loop), and inflate one payload
	// (data skew).
	broken := hexgrid.NewSnapshot(map[int][]hexgrid.Cell{3: u3, 4: u4})
	delete(broken.Levels[3], u3[0])
	// Pick an interior reachable cell as the island center.
	var center hexgrid.Cell
	for _, c := range u4 {
		if !c.IsReachable() {
			continue
		}
		interior := true
		for _, nb := range hexgrid.Neighbors(c) {
			if nb.Face() != c.Face() {
				interior = false
				break
			}
		}
		if interior {
			center = c
			break
		}
	}
	island := hexgrid.KRing(center, 1)
	inIsland := make(map[hexgrid.Cell]bool, len(island))
	for _, c := range island {
		inIsland[c] = true
	}
	for _, c := range hexgrid.KRing(center, 2) {
		if !inIsland[c] {
			delete(broken.Levels[4], c)
		}
	}
	broken.Weights = map[hexgrid.Cell]int64{center: 10000}
	brep := hexgrid.Inspect(broken, hexgrid.InspectOptions{})
	fmt.Printf("inspect corrupted snapshot: ok=%v, findings=%d\n", brep.OK(), len(brep.Findings))
	for _, f := range brep.Findings {
		if len(f.Detail) > 0 {
			fmt.Printf("  [%s] cell %s: %s\n", f.Kind, f.Cell, f.Detail)
		}
	}

	// --- Universe audit. ---
	urep, _ := hexgrid.VerifyUniverse(4)
	fmt.Printf("verify universe res 4: cells=%d reachable=%d ok=%v\n", urep.Cells, urep.Reachable, urep.OK())
}

func contains(cs [4]hexgrid.Cell, want hexgrid.Cell) bool {
	for _, c := range cs {
		if c == want {
			return true
		}
	}
	return false
}
