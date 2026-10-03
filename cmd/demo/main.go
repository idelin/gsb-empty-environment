// demo 演示 R-Tree 商铺索引：插入 5 万家商铺经纬度，
// 执行视野窗口（Bounding Box）查询，并与暴力扫描对照验证。
package main

import (
	"fmt"
	"math/rand"
	"time"

	"rtree/rtree"
)

func main() {
	rng := rand.New(rand.NewSource(2026))

	// 模拟上海市区范围内的 50,000 家商铺（含 20 个聚簇商圈）。
	const n = 50000
	shops := make([]rtree.Rect, n)
	for i := range shops {
		var lon, lat float64
		if rng.Float64() < 0.7 { // 70% 聚簇在商圈
			cx := 121.35 + float64(rng.Intn(20))*0.015
			cy := 31.15 + float64(rng.Intn(20))*0.012
			lon = cx + rng.Float64()*0.02
			lat = cy + rng.Float64()*0.02
		} else {
			lon = 121.35 + rng.Float64()*0.30
			lat = 31.15 + rng.Float64()*0.25
		}
		shops[i] = rtree.PointRect(rtree.Point{X: lon, Y: lat})
	}

	// 构建索引。
	tree := rtree.New(8, 3)
	start := time.Now()
	for id, r := range shops {
		tree.Insert(id, r)
	}
	fmt.Printf("插入 %d 家商铺耗时: %v (树高=%d)\n", n, time.Since(start), tree.Height())

	// 视野窗口查询：人民广场附近约 5km x 5km。
	window := rtree.NewRect(
		rtree.Point{X: 121.45, Y: 31.20},
		rtree.Point{X: 121.50, Y: 31.25},
	)

	start = time.Now()
	hits := tree.Search(window)
	rtreeCost := time.Since(start)

	// 暴力扫描对照。
	start = time.Now()
	brute := 0
	for _, r := range shops {
		if r.Intersects(window) {
			brute++
		}
	}
	bruteCost := time.Since(start)

	fmt.Printf("窗口查询: 命中 %d 家商铺, R-Tree 耗时 %v, 暴力扫描耗时 %v (命中 %d)\n",
		len(hits), rtreeCost, bruteCost, brute)
	if len(hits) != brute {
		fmt.Println("结果与暴力扫描不一致!")
		return
	}
	fmt.Println("结果与暴力扫描完全一致 ✓")

	// 输出前 10 家命中商铺的坐标。
	fmt.Println("前 10 家命中商铺:")
	for i, id := range hits {
		if i >= 10 {
			break
		}
		fmt.Printf("  商铺#%-6d 经度=%.6f 纬度=%.6f\n", id, shops[id].Min.X, shops[id].Min.Y)
	}
}
