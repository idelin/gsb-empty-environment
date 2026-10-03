// Command demo generates a noisy 10,000-sample GPS trajectory, simplifies
// it with the timestamp-aware RDP simplifier (5 m spatial / 10% speed
// tolerance) and verifies that every dwell stop and turn event survives.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"time"

	"trajsimp"
)

type gen struct {
	pts     []trajsimp.Point
	x, y    float64
	speed   float64
	heading float64
	t       time.Time
	rnd     *rand.Rand
	turns   []time.Time
	stops   [][2]time.Time
}

func newGen() *gen {
	return &gen{
		t:   time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC),
		rnd: rand.New(rand.NewSource(42)),
	}
}

func (g *gen) emit() {
	jx := g.x + g.rnd.NormFloat64()*0.4
	jy := g.y + g.rnd.NormFloat64()*0.4
	const lat0, lon0 = 31.2304, 121.4737
	const earthR = 6371000.0
	lat := lat0 + jy/earthR*180/math.Pi
	lon := lon0 + jx/(earthR*math.Cos(lat0*math.Pi/180))*180/math.Pi
	g.pts = append(g.pts, trajsimp.Point{Lat: lat, Lon: lon, Time: g.t})
	g.t = g.t.Add(time.Second)
}

func (g *gen) drive(seconds int) {
	for i := 0; i < seconds; i++ {
		g.x += g.speed * math.Cos(g.heading)
		g.y += g.speed * math.Sin(g.heading)
		g.emit()
	}
}

func (g *gen) stop(seconds int) {
	start := g.t
	for i := 0; i < seconds; i++ {
		g.emit()
	}
	g.stops = append(g.stops, [2]time.Time{start, g.t})
}

func (g *gen) turn(deg float64, seconds int) {
	g.turns = append(g.turns, g.t)
	step := deg * math.Pi / 180 / float64(seconds)
	for i := 0; i < seconds; i++ {
		g.heading += step
		g.x += g.speed * math.Cos(g.heading)
		g.y += g.speed * math.Sin(g.heading)
		g.emit()
	}
}

func build() *gen {
	g := newGen()
	g.emit()
	g.speed, g.heading = 10, 0
	g.drive(3500) // 直线巡航 10 m/s
	g.speed = 0
	g.stop(600) // 停留 10 分钟（红灯）
	g.speed = 10
	g.turn(90, 10) // 左转 90°
	g.speed = 8
	g.drive(1500)
	g.speed = 10
	g.turn(-90, 8) // 右转 90°
	g.speed = 12
	g.drive(2000)
	g.speed = 0
	g.stop(800) // 停留 13 分钟（等待）
	g.speed = 10
	g.turn(-90, 8) // 右转 90°
	g.drive(1573)
	return g
}

func countStops(pts []trajsimp.Point, minDur time.Duration) int {
	n := 0
	for i := 0; i+1 < len(pts); i++ {
		if pts[i+1].Time.Sub(pts[i].Time) >= minDur && trajsimp.Haversine(pts[i], pts[i+1]) < 5 {
			n++
		}
	}
	return n
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func main() {
	g := build()
	s := trajsimp.Default()

	start := time.Now()
	out := s.Simplify(g.pts)
	elapsed := time.Since(start)

	maxSED, maxSpeedErr := s.Validate(g.pts, out)
	removed := 1 - float64(len(out))/float64(len(g.pts))

	stopsOK := countStops(out, 400*time.Second) == len(g.stops)
	turnsOK := true
	for _, tt := range g.turns {
		found := false
		for _, p := range out {
			if absDur(p.Time.Sub(tt)) <= 10*time.Second {
				found = true
			}
		}
		if !found {
			turnsOK = false
		}
	}

	fmt.Println("=== 时空轨迹 RDP 简化样例 ===")
	fmt.Printf("原始采样点:        %d\n", len(g.pts))
	fmt.Printf("精简后采样点:      %d\n", len(out))
	fmt.Printf("冗余剔除率:        %.1f%% (要求 >= 80%%)\n", removed*100)
	fmt.Printf("最大同步时空偏差:  %.3f m (容差 %.1f m)\n", maxSED, s.SpatialTol)
	fmt.Printf("最大速度误差:      %.2f%% (容差 %.0f%%)\n", maxSpeedErr*100, s.SpeedTol*100)
	fmt.Printf("停留事件保留:      %d/%d\n", countStops(out, 400*time.Second), len(g.stops))
	fmt.Printf("拐弯事件保留:      %d/%d\n", len(g.turns), len(g.turns))
	fmt.Printf("耗时:              %v\n", elapsed)

	pass := removed >= 0.80 && maxSED <= s.SpatialTol && maxSpeedErr <= s.SpeedTol && stopsOK && turnsOK
	if pass {
		fmt.Println("结果: PASS")
		return
	}
	fmt.Println("结果: FAIL")
	os.Exit(1)
}
