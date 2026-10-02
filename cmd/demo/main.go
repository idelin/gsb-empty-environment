// Command demo simulates streaming one million taxi passenger pickup points
// into the spatio-temporal density cube, then answers:
//
//	"上周五晚高峰全市人流密度最高的前10个网格中心经纬度"
//
// Run with: go run cmd/demo/main.go
package main

import (
	"fmt"
	"math/rand"
	"runtime"
	"time"

	"density/density"
)

func main() {
	// Grid: 250m tiles anchored at south-west Shanghai.
	grid := density.NewGrid(31.0, 121.3, 250)
	cube := density.NewCube(grid)

	// "Last Friday" evening peak: 2026-09-25 (Friday), 17:00–20:00 local.
	friday := time.Date(2026, 9, 25, 17, 0, 0, 0, time.Local)
	windowEnd := friday.Add(3 * time.Hour)

	// Planted congestion hotspots (major Shanghai hubs).
	hotspots := [][2]float64{
		{31.2304, 121.4737}, // People's Square
		{31.2397, 121.4998}, // Lujiazui
		{31.1943, 121.4351}, // Xujiahui
		{31.2215, 121.5440}, // Century Avenue
	}

	rng := rand.New(rand.NewSource(20260925))
	const total = 1000000

	start := time.Now()
	for i := 0; i < total; i++ {
		var lat, lng float64
		if rng.Float64() < 0.6 {
			hs := hotspots[rng.Intn(len(hotspots))]
			lat = hs[0] + rng.NormFloat64()*0.004
			lng = hs[1] + rng.NormFloat64()*0.004
		} else {
			lat = 31.05 + rng.Float64()*0.35
			lng = 121.32 + rng.Float64()*0.35
		}
		// Random timestamp within the Friday evening peak window.
		ts := friday.Add(time.Duration(rng.Intn(3*3600)) * time.Second)
		cube.Add(lat, lng, ts, 1)
	}
	ingest := time.Since(start)

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	fmt.Printf("Ingested %d trajectory points in %v (%.0f pts/s)\n",
		total, ingest, float64(total)/ingest.Seconds())
	fmt.Printf("Active spatio-temporal cells: %d (heap in use: %.1f MB)\n\n",
		cube.Len(), float64(mem.HeapInuse)/1024/1024)

	start = time.Now()
	top := cube.TopK(friday, windowEnd, 10)
	query := time.Since(start)

	fmt.Printf("Query: 上周五晚高峰 (%s ~ %s) 人流密度 Top-10 网格 (took %v)\n",
		friday.Format("15:04"), windowEnd.Format("15:04"), query)
	fmt.Printf("%-4s %-12s %-12s %s\n", "Rank", "Lat", "Lng", "Weight")
	for i, h := range top {
		fmt.Printf("%-4d %-12.5f %-12.5f %.0f\n", i+1, h.Lat, h.Lng, h.Weight)
	}
}
