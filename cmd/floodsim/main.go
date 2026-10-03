// Command floodsim runs a demo spatiotemporal flood simulation over a
// synthetic DEM: a west-to-east sloping terrain with a central depression,
// hit by a temporally varying rainstorm. It prints the per-tick evolution
// of connected water regions and road blockages, then answers historical
// queries for earlier ticks.
package main

import (
	"fmt"

	"floodsim/flood"
)

const (
	width  = 20
	height = 12
)

func buildDEM() *flood.Grid {
	dem := flood.NewGrid(width, height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// Gentle west -> east slope.
			elev := 8.0 - 0.3*float64(x)
			// Central depression (a small basin around x 8..11, y 4..7).
			if x >= 8 && x <= 11 && y >= 4 && y <= 7 {
				elev = 0.5
			}
			dem.Set(x, y, elev)
		}
	}
	return dem
}

func buildInfiltration() *flood.Grid {
	inf := flood.NewGrid(width, height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			v := 0.08 // loam
			if y < 3 {
				v = 0.2 // sandy ridge, more permeable
			}
			inf.Set(x, y, v)
		}
	}
	return inf
}

func main() {
	dem := buildDEM()
	// Temporally varying rainstorm: build-up, peak, decay (depth per tick).
	rain := []float64{0.1, 0.2, 0.4, 0.5, 0.4, 0.25, 0.15, 0.05, 0, 0, 0, 0}

	sim := flood.NewSimulator(dem, rain, flood.Config{
		FlowRate:   0.5,
		FlowPasses: 24,
		BlockDepth: 0.4,
	})
	sim.InfiltrationMap = buildInfiltration()

	roads := []flood.Road{
		{ID: "LakeRoad", Points: []flood.Point{{X: 0, Y: 5.5}, {X: 19, Y: 5.5}}}, // crosses the basin
		{ID: "HillRoad", Points: []flood.Point{{X: 0, Y: 1.5}, {X: 19, Y: 1.5}}}, // northern ridge
		{ID: "DiagRoad", Points: []flood.Point{{X: 2, Y: 11}, {X: 17, Y: 0.5}}},  // diagonal
	}

	fmt.Println("=== Spatiotemporal flood spread simulation ===")
	fmt.Printf("grid %dx%d, rain series %v\n\n", width, height, rain)
	fmt.Printf("%-5s %-10s %-8s %-30s\n", "tick", "water", "regions", "blocked roads")
	for t := 1; t <= len(rain); t++ {
		sim.Step()
		regions := sim.RegionsAt(t)
		blocked := ""
		for _, bp := range sim.BlockagesAt(t, roads) {
			blocked += fmt.Sprintf("%s[%.1f->%.1f] ", bp.RoadID, bp.Enter.X, bp.Exit.X)
		}
		fmt.Printf("%-5d %-10.2f %-8d %-30s\n", t, sim.TotalWater(t), len(regions), blocked)
	}

	last := sim.Tick()
	fmt.Printf("\n=== Connected regions at final tick %d ===\n", last)
	for _, r := range sim.RegionsAt(last) {
		fmt.Printf("region #%d: area=%d cells, boundary polygon (%d vertices): %v\n",
			r.ID, r.Area(), len(r.Boundary), r.Boundary)
	}

	fmt.Println("\n=== Historical query: breakpoints at every tick ===")
	for t := 0; t <= last; t++ {
		bps := sim.BlockagesAt(t, roads)
		if len(bps) == 0 {
			fmt.Printf("tick %2d: all roads passable\n", t)
			continue
		}
		for _, bp := range bps {
			fmt.Printf("tick %2d: %s blocked from (%.2f,%.2f) to (%.2f,%.2f), %d cells\n",
				t, bp.RoadID, bp.Enter.X, bp.Enter.Y, bp.Exit.X, bp.Exit.Y, bp.Cells)
		}
	}
}
