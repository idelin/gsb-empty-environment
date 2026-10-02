// taxiheat 演示：流式导入百万级出租车载客点，构建时空立方体，
// 并查询"上周五晚高峰全市人流密度最高的前 10 个网格中心经纬度"。
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"runtime"
	"time"

	"taxiheat"
)

// lastFriday 返回距今最近的一个周五 00:00（本地时区）。
func lastFriday(now time.Time) time.Time {
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	back := (int(d.Weekday()) - int(time.Friday) + 7) % 7
	if back == 0 {
		back = 7 // 今天就是周五则取上周五
	}
	return d.AddDate(0, 0, -back)
}

// 模拟热点区域（北京：CBD、中关村、西直门、望京、首都机场）
var hotspots = [][2]float64{
	{39.908, 116.457},
	{39.983, 116.316},
	{39.940, 116.355},
	{39.996, 116.474},
	{40.080, 116.605},
}

func main() {
	n := flag.Int("n", 1000000, "number of trajectory points to stream")
	k := flag.Int("k", 10, "top-k hotspots")
	flag.Parse()

	// 瓦片网格：覆盖北京市区 0.5°x0.3°，单元约 500m，100x60=6000 格
	grid, err := taxiheat.NewGrid(39.8, 116.2, 0.005, 100, 60)
	if err != nil {
		panic(err)
	}
	cube := taxiheat.NewCube(grid)

	rng := rand.New(rand.NewSource(20260925))
	friday := lastFriday(time.Now())
	weekStart := friday.AddDate(0, 0, -6) // 覆盖一整周

	// ---- 流式导入 ----
	start := time.Now()
	for i := 0; i < *n; i++ {
		var lat, lng float64
		if rng.Float64() < 0.6 { // 60% 的点围绕热点（高斯抖动）
			h := hotspots[rng.Intn(len(hotspots))]
			lat = h[0] + rng.NormFloat64()*0.02
			lng = h[1] + rng.NormFloat64()*0.02
		} else { // 40% 均匀背景
			lat = 39.8 + rng.Float64()*0.3
			lng = 116.2 + rng.Float64()*0.5
		}
		// 一周内随机时刻，晚高峰(17-20点)加权
		hour := rng.Intn(7 * 24)
		if rng.Float64() < 0.35 {
			hour = hour/24*24 + 17 + rng.Intn(3)
		}
		ts := weekStart.Add(time.Duration(hour)*time.Hour + time.Duration(rng.Intn(3600))*time.Second)
		cube.Add(lat, lng, ts, 1)
	}
	ingest := time.Since(start)

	added, dropped, hours, mem := cube.Stats()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Printf("== 导入 ==\n")
	fmt.Printf("落桶点数: %d, 越界丢弃: %d, 小时层数: %d\n", added, dropped, hours)
	fmt.Printf("导入耗时: %v (%.0f 点/秒)\n", ingest, float64(added)/ingest.Seconds())
	fmt.Printf("立方体内存: %.2f MB, 进程堆内存: %.2f MB\n",
		float64(mem)/1e6, float64(ms.HeapAlloc)/1e6)

	// ---- 查询：上周五晚高峰 17:00-20:00 Top-K ----
	qStart := friday.Add(17 * time.Hour)
	qEnd := friday.Add(20 * time.Hour)
	t0 := time.Now()
	hs := cube.TopK(qStart, qEnd, *k)
	elapsed := time.Since(t0)

	weekdays := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	fmt.Printf("\n== 查询：%s %s 晚高峰 17:00-20:00 Top-%d (耗时 %v) ==\n",
		qStart.Format("2006-01-02"), weekdays[qStart.Weekday()], *k, elapsed)
	fmt.Printf("%-4s %-10s %-11s %s\n", "排名", "纬度", "经度", "累计权重")
	for i, h := range hs {
		fmt.Printf("%-4d %-10.5f %-11.5f %.0f\n", i+1, h.Lat, h.Lng, h.Weight)
	}
}
