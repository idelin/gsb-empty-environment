// Command demo exercises the hexgrid package end to end: it encodes a
// GPS coordinate at resolution 8, expands a K=3 topological ring around
// it, and runs a simulated load pattern through the adaptive controller.
package main

import (
	"fmt"
	"time"

	"hexgrid"
)

func main() {
	// --- Part 1: GPS -> resolution-8 hex cell -------------------------
	lat, lng := 39.9042, 116.4074 // Beijing
	const res = 8
	cell := hexgrid.GeoToCell(lat, lng, res)
	clat, clng := cell.Center()
	fmt.Printf("GPS (%.4f, %.4f) @ res %d\n", lat, lng, res)
	fmt.Printf("  cell        = %s\n", cell)
	fmt.Printf("  base cell   = %d (icosahedron face)\n", cell.BaseCell())
	fmt.Printf("  center      = (%.6f, %.6f)\n", clat, clng)
	fmt.Printf("  parent@6    = %s\n", cell.Parent(6))
	children, _ := cell.Parent(7).Children()
	fmt.Printf("  children of parent@7 = %d cells, first = %s\n", len(children), children[0])

	// --- Part 2: topological K-Ring (k = 3) ---------------------------
	const k = 3
	ring := hexgrid.KRing(cell, k)
	uniq := make(map[hexgrid.Cell]struct{}, len(ring))
	hist := map[int]int{}
	for _, c := range ring {
		uniq[c] = struct{}{}
		if d, ok := hexgrid.GridDistance(cell, c); ok {
			hist[d]++
		}
	}
	fmt.Printf("\nK-Ring(k=%d): %d cells (%d unique)\n", k, len(ring), len(uniq))
	for d := 0; d <= k; d++ {
		fmt.Printf("  ring distance %d: %d cells\n", d, hist[d])
	}
	fmt.Printf("  sample neighbors: %s %s %s\n", ring[1], ring[2], ring[3])

	// --- Part 3: adaptive threshold under simulated load --------------
	cfg := hexgrid.AdaptiveConfig{
		Min: 10, Max: 1000, Initial: 500,
		HighWatermark: 0.75, LowWatermark: 0.30,
		IncreaseStep: 25, DecreaseFactor: 0.7,
		Smoothing: 0.4, Cooldown: 100 * time.Millisecond,
		PressureGain: 1.5,
	}
	now := time.Unix(0, 0)
	ctrl, err := hexgrid.NewAdaptiveController(cfg, hexgrid.WithClock(func() time.Time { return now }))
	if err != nil {
		panic(err)
	}
	fmt.Println("\nAdaptive controller simulation (tick = 100ms):")
	scenario := []struct {
		name    string
		ticks   int
		metrics hexgrid.EnvMetrics
	}{
		{"overload", 5, hexgrid.EnvMetrics{CPUUtil: 1.0, MemUtil: 0.9, QueueDepth: 0.95, ErrorRate: 0.3}},
		{"steady", 4, hexgrid.EnvMetrics{CPUUtil: 0.5, QueueDepth: 0.4}},
		{"idle", 8, hexgrid.EnvMetrics{CPUUtil: 0.05, QueueDepth: 0.02}},
	}
	for _, phase := range scenario {
		for i := 0; i < phase.ticks; i++ {
			now = now.Add(100 * time.Millisecond)
			adj := ctrl.Observe(phase.metrics)
			mark := " "
			if adj.Adjusted {
				mark = "*"
			}
			fmt.Printf("  %s [%s] ema=%.3f threshold=%8.2f\n", mark, phase.name, adj.SmoothedLoad, adj.NewThreshold)
		}
	}
	fmt.Printf("final: %s\n", ctrl)
}
