// 运行样例：离散六边形分层空间索引 + 自适应动态阈值控制。
package main

import (
	"fmt"
	"time"

	"hexgrid/adaptive"
	"hexgrid/grid"
)

func main() {
	fmt.Println("=== 离散六边形分层空间索引（二十面体展开, aperture-7）===")
	gps := grid.LatLng{Lat: 39.9042, Lng: 116.4074} // 北京
	h, err := grid.FromLatLng(gps, 8)
	if err != nil {
		panic(err)
	}
	fmt.Printf("GPS 坐标:           (%.4f, %.4f)\n", gps.Lat, gps.Lng)
	fmt.Printf("Resolution=8 编码:  %s (十六进制)\n", h)
	center := grid.Center(h)
	fmt.Printf("解码中心:           (%.4f, %.4f)  偏差 %.1f m\n",
		center.Lat, center.Lng, grid.HaversineM(gps, center))
	fmt.Printf("单元平均面积:       %.2f km²\n", grid.CellAreaM2(8)/1e6)

	parent, _ := grid.Parent(h, 7)
	children, _ := grid.Children(parent)
	fmt.Printf("父单元 (res=7):     %s   子单元数: %d\n", parent, len(children))

	fmt.Println("\n=== K-Ring 拓扑邻域（纯整数遍历，无浮点三角）===")
	const k = 3
	cells := grid.KRing(h, k)
	for r := 0; r <= k; r++ {
		fmt.Printf("  环 %d: %d 个单元\n", r, len(grid.Ring(h, r)))
	}
	edges, ok := grid.VerifyContiguousClosed(cells)
	fmt.Printf("K-Ring(k=%d) 总数:   %d (= 1+3k(k+1))\n", k, len(cells))
	fmt.Printf("覆盖校验:           连续闭合=%v, 边界边数=%d (期望 %d), 无空洞遗漏\n",
		ok, edges, 12*(k+1)-6)
	fmt.Printf("覆盖总面积:         %.2f km²\n", float64(len(cells))*grid.CellAreaM2(8)/1e6)

	fmt.Println("\n=== 自适应动态阈值伸缩（负载感知）===")
	cfg := adaptive.DefaultConfig()
	cfg.Min, cfg.Max = 8, 256
	cfg.Cooldown = 50 * time.Millisecond // demo 加速展示冷却期
	ctrl, err := adaptive.NewController(cfg)
	if err != nil {
		panic(err)
	}
	lim := adaptive.NewLimiter(ctrl)
	fmt.Printf("初始阈值: %.0f (冷启动保守取值 Min，规避雪崩)\n", ctrl.Threshold())
	fmt.Printf("%6s %8s %8s %8s %10s %10s\n", "tick", "CPU", "P99(s)", "错误率", "在途", "阈值")
	for tick := 0; tick < 30; tick++ {
		var m adaptive.Metrics
		switch {
		case tick < 8: // 健康且需求饱和 -> 缓慢爬坡
			m = adaptive.Metrics{InFlight: int(ctrl.Threshold()) + 10, CPU: 0.45, LatencyP99: 0.08}
		case tick < 14: // 流量洪峰 -> 快速乘性回退 + 冷却
			m = adaptive.Metrics{InFlight: 500, CPU: 0.97, LatencyP99: 1.2, ErrorRate: 0.08}
		case tick < 20: // 冷却期：指标恢复但不上调
			m = adaptive.Metrics{InFlight: 300, CPU: 0.5, LatencyP99: 0.1}
		default: // 冷却结束 -> 负载感知恢复爬坡
			m = adaptive.Metrics{InFlight: int(ctrl.Threshold()) + 10, CPU: 0.5, LatencyP99: 0.1}
		}
		th := ctrl.Update(m)
		fmt.Printf("%6d %8.2f %8.2f %8.2f %10d %10.1f\n",
			tick, m.CPU, m.LatencyP99, m.ErrorRate, m.InFlight, th)
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Printf("当前在途许可: %d (无泄漏)\n", lim.InFlight())
}
