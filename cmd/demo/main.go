// Command demo runs a 500-unit air-traffic simulation against the
// spatiotemporal grid engine and prints the warning beacons.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"stgrid"
)

func buildScenario() []stgrid.Entity {
	ents := make([]stgrid.Entity, 0, 500)
	// A and B fly head-on: 1050 m apart, closing 100 m/s, so they break the
	// 50 m minimum separation exactly 10 s after the start.
	ents = append(ents,
		stgrid.Entity{ID: "A", Pos: stgrid.Vec3{X: 0, Y: 0, Z: 0}, Vel: stgrid.Vec3{X: 50}},
		stgrid.Entity{ID: "B", Pos: stgrid.Vec3{X: 1050}, Vel: stgrid.Vec3{X: -50}},
	)
	// 498 more units on a well-separated lattice with small random drift.
	rng := rand.New(rand.NewSource(42))
	for x := 0; x < 8 && len(ents) < 500; x++ {
		for y := 0; y < 8 && len(ents) < 500; y++ {
			for z := 0; z < 8 && len(ents) < 500; z++ {
				ents = append(ents, stgrid.Entity{
					ID:  fmt.Sprintf("U%03d", len(ents)),
					Pos: stgrid.Vec3{X: float64(x)*2000 - 8000, Y: float64(y)*2000 - 8000, Z: float64(z)*2000 + 5000},
					Vel: stgrid.Vec3{X: rng.Float64()*40 - 20, Y: rng.Float64()*40 - 20, Z: rng.Float64()*40 - 20},
				})
			}
		}
	}
	return ents
}

func main() {
	eng := stgrid.NewEngine(
		50,  // minimum safe separation (m)
		1,   // time slot duration (s)
		10,  // prediction horizon: 10 slots = 10 s
		5,   // warning lead time (s)
		120, // max entity speed (m/s), sizes the hash cells
	)
	ents := buildScenario()
	fmt.Printf("spatiotemporal grid engine: %d units, min-sep %.0f m, horizon %.0f s, warn lead %.0f s, cell %.0f m\n\n",
		len(ents), eng.MinSep, eng.Horizon(), eng.WarnLead, eng.CellSize)

	start := time.Now()
	for tick := 0; tick < 10; tick++ {
		now := float64(tick)
		beacons := eng.Beacons(ents, now)
		for _, b := range beacons {
			fmt.Printf("[BEACON t=%4.1fs] %s <-> %s | current distance %7.1f m | breach of %.0f m min-sep in %4.1f s | predicted closest approach %5.1f m\n",
				b.Time, b.A, b.B, b.CurrentDistance, eng.MinSep, b.TimeToBreach, b.MinDistance)
		}
		if len(beacons) == 0 {
			fmt.Printf("[t=%4.1fs] no imminent breaches within %.0f s\n", now, eng.WarnLead)
		}
		for i := range ents {
			ents[i].Pos = ents[i].Pos.Add(ents[i].Vel.Scale(1))
		}
	}
	elapsed := time.Since(start)
	fmt.Printf("\nsimulated 10 s in %v\n", elapsed)
	fmt.Printf("broad phase: %d candidate pairs tested vs %d brute-force pairs (%.1fx reduction), %d spatiotemporal cells per scan\n",
		eng.LastCandidates, eng.LastBruteForce,
		float64(eng.LastBruteForce)/float64(eng.LastCandidates), eng.LastCells)
}
