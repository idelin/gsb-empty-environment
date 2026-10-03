// Command demo builds a 4D (3D + time) BVH over UAV cruise trajectories and
// answers the acceptance query:
//
//	"all trajectory segments that entered the core protection zone
//	 between 14:00 and 14:10 above 100m altitude"
//
// Run with: go run ./cmd/demo
package main

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"spacetime"
)

func hhmmss(seconds float64) string {
	s := int(math.Round(seconds))
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, (s%3600)/60, s%60)
}

func main() {
	const (
		t1400 = 14*3600 + 0*60  // 14:00 in seconds since midnight
		t1410 = 14*3600 + 10*60 // 14:10
	)

	// Core protection zone: a 400m x 400m ground footprint.
	zoneMin := spacetime.Vec3{X: 2000, Y: 3000, Z: 100} // z >= 100m
	zoneMax := spacetime.Vec3{X: 2400, Y: 3400, Z: 5000}

	tree := spacetime.New(16)

	// --- Hand-crafted acceptance trajectories (times in seconds since midnight) ---
	flights := []struct {
		id    string
		pts   []spacetime.Vec3
		times []float64
	}{
		// alpha: crosses the zone at 150m during 14:03-14:05 -> MUST HIT.
		{"alpha",
			[]spacetime.Vec3{{X: 1000, Y: 3200, Z: 150}, {X: 2200, Y: 3200, Z: 150}, {X: 2600, Y: 3200, Z: 150}},
			[]float64{t1400 + 180, t1400 + 240, t1400 + 300}},
		// beta: crosses the zone but only at 60m (below 100m) -> excluded.
		{"beta",
			[]spacetime.Vec3{{X: 1000, Y: 3100, Z: 60}, {X: 2600, Y: 3100, Z: 60}},
			[]float64{t1400 + 120, t1400 + 360}},
		// gamma: at 200m but flies around the zone -> excluded.
		{"gamma",
			[]spacetime.Vec3{{X: 1000, Y: 3600, Z: 200}, {X: 2600, Y: 3600, Z: 200}},
			[]float64{t1400 + 60, t1400 + 420}},
		// delta: crosses the zone at 300m but at 13:30 -> excluded by time.
		{"delta",
			[]spacetime.Vec3{{X: 1000, Y: 3200, Z: 300}, {X: 2600, Y: 3200, Z: 300}},
			[]float64{13*3600 + 1800, 13*3600 + 2100}},
		// epsilon: clips the zone corner at 120m around 14:08 -> MUST HIT (partial segment).
		{"epsilon",
			[]spacetime.Vec3{{X: 1800, Y: 2800, Z: 120}, {X: 2600, Y: 3600, Z: 120}},
			[]float64{t1400 + 420, t1400 + 540}},
	}
	total := 0
	for _, f := range flights {
		total += tree.InsertTrajectory(f.id, f.pts, f.times)
	}

	// --- Background air traffic: 3000 random drones, 40 legs each ---
	rng := rand.New(rand.NewSource(1))
	for d := 0; d < 3000; d++ {
		id := fmt.Sprintf("uav-%04d", d)
		pos := spacetime.Vec3{X: rng.Float64() * 8000, Y: rng.Float64() * 8000, Z: 20 + rng.Float64()*380}
		ts := 13*3600 + rng.Float64()*3600 // airborne sometime during 13:00-14:00
		pts := []spacetime.Vec3{pos}
		times := []float64{ts}
		for i := 0; i < 40; i++ {
			pos = spacetime.Vec3{
				X: pos.X + (rng.Float64()-0.5)*500,
				Y: pos.Y + (rng.Float64()-0.5)*500,
				Z: math.Max(0, math.Min(500, pos.Z+(rng.Float64()-0.5)*40)),
			}
			ts += 30
			pts = append(pts, pos)
			times = append(times, ts)
		}
		total += tree.InsertTrajectory(id, pts, times)
	}
	fmt.Printf("indexed %d trajectory segments (%d drones)\n", total, 3000+len(flights))

	// Rebalance after bulk insertion for a low-overlap, query-optimized tree.
	start := time.Now()
	tree.Rebuild()
	fmt.Printf("bulk rebuild took %v\n\n", time.Since(start))

	// --- The acceptance query ---
	query := spacetime.Box(zoneMin, zoneMax, t1400, t1410)
	fmt.Printf("query: zone x[%.0f,%.0f] y[%.0f,%.0f] z>=100m, time %s-%s\n\n",
		zoneMin.X, zoneMax.X, zoneMin.Y, zoneMax.Y, hhmmss(t1400), hhmmss(t1410))

	start = time.Now()
	hits := tree.QueryExact(query)
	elapsed := time.Since(start)
	st := tree.LastQueryStats()

	fmt.Printf("found %d matching trajectory fragments in %v\n", len(hits), elapsed)
	fmt.Printf("pruning: visited %d nodes / %d leaves, scanned %d of %d segments (%.2f%%)\n\n",
		st.NodesVisited, st.LeavesVisited, st.SegmentsScanned, tree.Size(),
		100*float64(st.SegmentsScanned)/float64(tree.Size()))

	for _, h := range hits {
		s := h.Segment
		fmt.Printf("  %-7s leg#%-2d %s -> %s  enter (%.0f,%.0f,%.0fm) exit (%.0f,%.0f,%.0fm)\n",
			s.TrajectoryID, s.Index,
			hhmmss(h.EnterT), hhmmss(h.ExitT),
			h.EnterPos.X, h.EnterPos.Y, h.EnterPos.Z,
			h.ExitPos.X, h.ExitPos.Y, h.ExitPos.Z)
	}
}
