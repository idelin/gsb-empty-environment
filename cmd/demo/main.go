// Command demo simulates an urban-canyon GPS trace with severe multipath
// drift and map-matches it onto a street grid using the HMM/Viterbi kernel.
package main

import (
	"fmt"
	"math"
	"math/rand"

	"mapmatch"
)

const (
	baseLat = 31.2304 // Shanghai
	baseLon = 121.4737
)

func xyToPoint(x, y float64) mapmatch.Point {
	return mapmatch.Point{
		Lat: baseLat + y/111320.0,
		Lon: baseLon + x/(111320.0*math.Cos(baseLat*math.Pi/180)),
	}
}

func toXY(p mapmatch.Point) (float64, float64) {
	return (p.Lon - baseLon) * 111320.0 * math.Cos(baseLat*math.Pi/180),
		(p.Lat - baseLat) * 111320.0
}

// buildGrid builds a 4x4 two-way street grid with 100 m blocks.
func buildGrid() *mapmatch.Graph {
	const n = 4
	const cell = 100.0
	var nodes []mapmatch.Point
	for iy := 0; iy < n; iy++ {
		for ix := 0; ix < n; ix++ {
			nodes = append(nodes, xyToPoint(float64(ix)*cell, float64(iy)*cell))
		}
	}
	g := mapmatch.NewGraph(nodes)
	idx := func(ix, iy int) int { return iy*n + ix }
	for iy := 0; iy < n; iy++ {
		for ix := 0; ix < n; ix++ {
			if ix+1 < n {
				g.AddUndirected(idx(ix, iy), idx(ix+1, iy))
			}
			if iy+1 < n {
				g.AddUndirected(idx(ix, iy), idx(ix, iy+1))
			}
		}
	}
	return g
}

func main() {
	g := buildGrid()
	m := mapmatch.NewMatcher(g)

	// Ground truth: east along y=100, then north along x=300 (8 m/s, 2 s
	// sampling -> 16 m spacing).
	waypoints := [][2]float64{{0, 100}, {300, 100}, {300, 300}}
	var truth [][2]float64
	for i := 0; i+1 < len(waypoints); i++ {
		ax, ay := waypoints[i][0], waypoints[i][1]
		bx, by := waypoints[i+1][0], waypoints[i+1][1]
		segLen := math.Hypot(bx-ax, by-ay)
		for d := 0.0; d < segLen; d += 16 {
			t := d / segLen
			truth = append(truth, [2]float64{ax + t*(bx-ax), ay + t*(by-ay)})
		}
	}
	truth = append(truth, waypoints[len(waypoints)-1])

	// Simulate GPS: sigma=12 m noise + multipath jumps of 50-80 m
	// perpendicular to travel every 9th sample.
	rng := rand.New(rand.NewSource(7))
	var obs []mapmatch.GPSPoint
	outlier := map[int]bool{}
	for i, pos := range truth {
		nx := rng.NormFloat64() * 12
		ny := rng.NormFloat64() * 12
		if i%9 == 4 && i+3 < len(truth) {
			outlier[i] = true
			var dirX, dirY float64
			if i+1 < len(truth) {
				dirX = truth[i+1][0] - pos[0]
				dirY = truth[i+1][1] - pos[1]
			} else {
				dirX = pos[0] - truth[i-1][0]
				dirY = pos[1] - truth[i-1][1]
			}
			norm := math.Hypot(dirX, dirY)
			sign := 1.0
			if rng.Float64() < 0.5 {
				sign = -1
			}
			jump := 50 + rng.Float64()*30
			nx += sign * -dirY / norm * jump
			ny += sign * dirX / norm * jump
		}
		p := xyToPoint(pos[0]+nx, pos[1]+ny)
		obs = append(obs, mapmatch.GPSPoint{Lat: p.Lat, Lon: p.Lon, T: float64(i) * 2})
	}

	matches := m.Match(obs)

	fmt.Println("HMM map matching demo: urban canyon with multipath drift")
	fmt.Println("=========================================================")
	fmt.Printf("%4s | %18s | %18s | %8s %8s | %s\n",
		"i", "raw (lat,lon)", "matched (lat,lon)", "rawErr", "matErr", "note")
	var rawSum, matSum float64
	for i, mt := range matches {
		tp := xyToPoint(truth[i][0], truth[i][1])
		rawErr := mapmatch.Haversine(mapmatch.Point{Lat: obs[i].Lat, Lon: obs[i].Lon}, tp)
		matErr := mapmatch.Haversine(mt.Point, tp)
		rawSum += rawErr
		matSum += matErr
		note := ""
		if outlier[i] {
			note = "MULTIPATH OUTLIER"
		}
		fmt.Printf("%4d | %9.6f,%9.6f | %9.6f,%9.6f | %7.1fm %7.1fm | %s\n",
			i, obs[i].Lat, obs[i].Lon, mt.Point.Lat, mt.Point.Lon, rawErr, matErr, note)
	}
	fmt.Println("=========================================================")
	fmt.Printf("average error: raw %.1f m -> matched %.1f m (%.0f%% reduction)\n",
		rawSum/float64(len(obs)), matSum/float64(len(obs)),
		100*(1-matSum/rawSum))
}
