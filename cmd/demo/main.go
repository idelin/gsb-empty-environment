// demo 模拟验收场景：围栏 08:00~10:00 生效，车辆 7:50 进入、
// 8:00 触发 Enter、停留触发 Dwell、9:30 离开触发 Exit，
// 中途在边界抖动验证迟滞消抖。
package main

import (
	"fmt"
	"time"

	"geofence"
)

func at(hour, min, sec int) time.Time {
	return time.Date(2026, 10, 2, hour, min, sec, 0, time.Local)
}

func main() {
	fence := &geofence.Fence{
		ID:         "yard-1",
		Polygon:    []geofence.Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		ValidFrom:  at(8, 0, 0),
		ValidTo:    at(10, 0, 0),
		Hysteresis: 0.5,
		DwellAfter: 30 * time.Minute,
	}
	mon := geofence.NewMonitor(fence)
	car := "vehicle-001"

	// 轨迹流：(时刻, 位置)
	track := []struct {
		t time.Time
		p geofence.Point
	}{
		{at(7, 50, 0), geofence.Point{5, 5}},   // 围栏未生效，静默
		{at(7, 59, 59), geofence.Point{5, 5}},  // 仍未生效
		{at(8, 0, 0), geofence.Point{5, 5}},    // 生效瞬间：Enter
		{at(8, 31, 0), geofence.Point{5, 5}},   // 停留超 30min：Dwell
		{at(9, 0, 0), geofence.Point{0.3, 5}},  // 以下 6 个点在边界抖动
		{at(9, 0, 10), geofence.Point{-0.3, 5}},
		{at(9, 0, 20), geofence.Point{0.2, 5}},
		{at(9, 0, 30), geofence.Point{-0.4, 5}},
		{at(9, 0, 40), geofence.Point{0.4, 5}},
		{at(9, 0, 50), geofence.Point{-0.2, 5}},
		{at(9, 30, 0), geofence.Point{20, 20}}, // 真正离开：Exit
	}

	fmt.Printf("围栏 %s 有效期 [%s, %s)，迟滞 %.1f，Dwell 阈值 %v\n\n",
		fence.ID, fence.ValidFrom.Format("15:04"), fence.ValidTo.Format("15:04"),
		fence.Hysteresis, fence.DwellAfter)

	for _, s := range track {
		events := mon.Process(car, s.p, s.t)
		inside := "外"
		if mon.State(car) {
			inside = "内"
		}
		fmt.Printf("%s  位置(%5.1f,%5.1f)  状态:%s", s.t.Format("15:04:05"), s.p.X, s.p.Y, inside)
		for _, e := range events {
			fmt.Printf("  >>> 告警: %s", e.Type)
		}
		fmt.Println()
	}
}
