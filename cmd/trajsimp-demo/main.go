// Command trajsimp-demo generates a noisy 10k-sample GPS trace with straight
// legs, stops and sharp turns, simplifies it, and verifies the result.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"trajsimp"
)

const metersPerDegLat = 111319.9

func metersPerDegLon(lat float64) float64 {
	return metersPerDegLat * math.Cos(lat*math.Pi/180)
}

func generate() []trajsimp.Point {
	rnd := rand.New(rand.NewSource(7))
	lon0, lat0 := 116.0, 39.0
	t0 := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	var pts []trajsimp.Point
	x, y := 0.0, 0.0
	sec := 0
	add := func(px, py float64) {
		pts = append(pts, trajsimp.Point{
			Lon: lon0 + px/metersPerDegLon(lat0),
			Lat: lat0 + py/metersPerDegLat,
			T:   t0.Add(time.Duration(sec) * time.Second),
		})
		sec++
	}
	noise := func(amp float64) (float64, float64) {
		return (rnd.Float64()*2 - 1) * amp, (rnd.Float64()*2 - 1) * amp
	}
	leg := func(headingDeg, speed float64, seconds int) {
		dx := math.Cos(headingDeg*math.Pi/180) * speed
		dy := math.Sin(headingDeg*math.Pi/180) * speed
		for s := 0; s < seconds; s++ {
			x += dx
			y += dy
			nx, ny := noise(0.5)
			add(x+nx, y+ny)
		}
	}
	stay := func(seconds int) {
		ax, ay := x, y
		for s := 0; s < seconds; s++ {
			nx, ny := noise(0.3)
			add(ax+nx, ay+ny)
		}
		x, y = ax, ay
	}

	add(x, y)
	leg(0, 10, 3000)   // 30 km east at 10 m/s
	stay(600)          // 10 min stop
	leg(90, 12, 2000)  // 24 km north at 12 m/s
	stay(400)          // 6m40s stop
	leg(45, 8, 2000)   // northeast at 8 m/s
	leg(-45, 11, 2000) // sharp 90° turn, southeast at 11 m/s
	return pts
}

func main() {
	pts := generate()
	opt := trajsimp.Options{Epsilon: 5, SpeedTolerance: 0.10}

	start := time.Now()
	idx := trajsimp.SimplifyIndices(pts, opt)
	elapsed := time.Since(start)

	maxSync, maxSpeedDev := trajsimp.Validate(pts, idx, opt)
	stays := trajsimp.DetectStays(pts, 10, time.Minute)

	fmt.Printf("输入采样点:        %d\n", len(pts))
	fmt.Printf("精简后点数:        %d (剔除 %.2f%%)\n", len(idx), (1-float64(len(idx))/float64(len(pts)))*100)
	fmt.Printf("耗时:              %v\n", elapsed)
	fmt.Printf("停留事件:          %d 段（端点全部保留）\n", len(stays))
	fmt.Printf("最大时间同步误差:  %.3f m (容差 %.0f m)\n", maxSync, opt.Epsilon)
	fmt.Printf("最大速度误差:      %.2f%% (容差 %.0f%%)\n", maxSpeedDev*100, opt.SpeedTolerance*100)
}
