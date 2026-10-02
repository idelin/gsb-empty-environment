// demo 模拟验收场景：8:00-10:00 生效的围栏，车辆轨迹流入，
// 实时输出 Enter / Dwell / Exit 事件，并演示迟滞带消除边界抖动。
package main

import (
	"fmt"
	"time"

	"geofence"
)

func at(hh, mm int) time.Time {
	return time.Date(2026, 10, 3, hh, mm, 0, 0, time.Local)
}

func report(events []geofence.Event) {
	for _, e := range events {
		fmt.Printf("  >>> [%s] 事件=%-5s 实体=%s 位置=(%.1f, %.1f)\n",
			e.Time.Format("15:04"), e.Type, e.EntityID, e.Position.X, e.Position.Y)
	}
}

func main() {
	fence := &geofence.Fence{
		Polygon: geofence.Polygon{
			{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 100}, {X: 0, Y: 100},
		},
		ActiveFrom:  at(8, 0),
		ActiveUntil: at(10, 0),
		Hysteresis:  2,
		DwellAfter:  30 * time.Minute,
	}
	mon := geofence.NewMonitor(fence)

	type sample struct {
		t  time.Time
		pt geofence.Point
	}
	track := []sample{
		{at(7, 50), geofence.Point{X: 50, Y: 50}},  // 围栏未生效，已在内部：无事件
		{at(8, 0), geofence.Point{X: 50, Y: 50}},   // 围栏生效：精准 Enter
		{at(8, 30), geofence.Point{X: 50, Y: 50}},  // 停留满 30 分钟：Dwell
		{at(9, 0), geofence.Point{X: 10, Y: 50}},   // 向边界移动
		{at(9, 5), geofence.Point{X: 1.5, Y: 50}},  // 边界内侧抖动
		{at(9, 6), geofence.Point{X: -1.5, Y: 50}}, // 边界外侧抖动（迟滞带内，无事件）
		{at(9, 7), geofence.Point{X: 1.0, Y: 50}},  // 又回到内侧（迟滞带内，无事件）
		{at(9, 8), geofence.Point{X: -1.0, Y: 50}}, // 继续抖动（迟滞带内，无事件）
		{at(9, 30), geofence.Point{X: 150, Y: 50}}, // 真正远离：Exit
	}

	fmt.Println("== 轨迹流实时判定（围栏 08:00-10:00 生效，迟滞带 ±2，Dwell 阈值 30min）==")
	for _, s := range track {
		fmt.Printf("[%s] 采样位置=(%.1f, %.1f) 当前状态=%s\n",
			s.t.Format("15:04"), s.pt.X, s.pt.Y, mon.State("car-1"))
		report(mon.Update("car-1", s.pt, s.t))
	}
	fmt.Printf("== 结束，car-1 最终状态=%s ==\n", mon.State("car-1"))
}
