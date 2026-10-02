// Command demo simulates a GPS trace drifting in a city canyon and matches it
// back onto the road network with the HMM map matcher.
package main

import (
	"fmt"
	"math"

	"mapmatch"
)

var ref = mapmatch.Point{Lat: 31.2304, Lon: 121.4737} // downtown Shanghai

func xy(x, y float64) mapmatch.Point {
	return mapmatch.OffsetPoint(ref, x, y)
}

func main() {
	// Road network: main road (y=0) and a parallel street (y=60m),
	// 1km each, joined by cross streets at x=0, 500, 1000.
	var nodes []mapmatch.Point
	for i := 0; i <= 10; i++ {
		nodes = append(nodes, xy(float64(i*100), 0))
	}
	for i := 0; i <= 10; i++ {
		nodes = append(nodes, xy(float64(i*100), 60))
	}
	g := mapmatch.NewGraph(nodes)
	for i := 0; i < 10; i++ {
		g.AddEdge("Main", i, i+1, []mapmatch.Point{nodes[i], nodes[i+1]})
	}
	for i := 11; i < 21; i++ {
		g.AddEdge("Parallel", i, i+1, []mapmatch.Point{nodes[i], nodes[i+1]})
	}
	for _, x := range []int{0, 5, 10} {
		g.AddEdge("Cross", x, x+11, []mapmatch.Point{nodes[x], nodes[x+11]})
	}

	// Simulated drive east along the main road at 10 m/s, GPS fix every 5s.
	// Multipath in the "canyon" throws some fixes ~55m toward the parallel
	// street; ordinary noise is ~10m.
	drift := map[int]bool{6: true, 7: true, 8: true, 13: true}
	var obs, truth []mapmatch.Point
	var ts []float64
	for i := 0; i < 19; i++ {
		x := 50 + float64(i)*50
		truth = append(truth, xy(x, 0))
		ts = append(ts, float64(i)*5)
		nx := 8 * math.Sin(float64(i)*2.3)
		ny := 9 * math.Cos(float64(i)*1.7)
		if drift[i] {
			ny += 55
		}
		obs = append(obs, xy(x+nx, ny))
	}

	m := mapmatch.NewMatcher(g)
	m.Sigma = 25 // city-canyon GPS error is large
	m.Beta = 20
	m.MaxSpeed = 30
	m.CandidateRadius = 80
	results := m.Match(obs, ts)

	fmt.Printf("%-4s %-8s %-21s %-21s %-8s %9s %9s\n",
		"#", "t(s)", "raw GPS (lat,lon)", "matched (lat,lon)", "edge", "rawErr(m)", "matchErr(m)")
	var rawSum, matchSum float64
	for _, r := range results {
		rawErr := mapmatch.Distance(r.Obs, truth[r.Index])
		matchErr := mapmatch.Distance(r.Cand.Point, truth[r.Index])
		rawSum += rawErr
		matchSum += matchErr
		fmt.Printf("%-4d %-8.0f (%.6f, %.6f) (%.6f, %.6f) %-8s %9.1f %9.1f\n",
			r.Index, ts[r.Index],
			r.Obs.Lat, r.Obs.Lon,
			r.Cand.Point.Lat, r.Cand.Point.Lon,
			r.Cand.Edge.Name, rawErr, matchErr)
	}
	n := float64(len(results))
	fmt.Printf("\nmean error: raw GPS %.1f m  ->  matched %.1f m\n", rawSum/n, matchSum/n)
}
