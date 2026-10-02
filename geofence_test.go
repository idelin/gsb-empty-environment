package geofence

import (
	"testing"
	"time"
)

// day 返回测试基准日（2026-10-03）当天 hh:mm 的本地时刻。
func day(hh, mm int) time.Time {
	return time.Date(2026, 10, 3, hh, mm, 0, 0, time.Local)
}

// squareFence 构造验收场景围栏：100x100 正方形，8:00-10:00 生效，
// 迟滞带半宽 2，停留阈值 30 分钟。
func squareFence() *Fence {
	return &Fence{
		Polygon: Polygon{
			{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 100}, {X: 0, Y: 100},
		},
		ActiveFrom:  day(8, 0),
		ActiveUntil: day(10, 0),
		Hysteresis:  2,
		DwellAfter:  30 * time.Minute,
	}
}

func eventTypes(events []Event) []EventType {
	types := make([]EventType, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

func requireEvents(t *testing.T, events []Event, want ...EventType) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("期望 %d 个事件 %v，实际 %d 个 %v", len(want), want, len(events), eventTypes(events))
	}
	for i, w := range want {
		if events[i].Type != w {
			t.Fatalf("事件 %d 期望 %v，实际 %v", i, w, events[i].Type)
		}
	}
}

// TestPolygonContains 验证射线法点包含与符号距离符号。
func TestPolygonContains(t *testing.T) {
	poly := Polygon{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10}}
	cases := []struct {
		pt     Point
		inside bool
	}{
		{Point{X: 5, Y: 5}, true},
		{Point{X: 0.5, Y: 9.5}, true},
		{Point{X: -1, Y: 5}, false},
		{Point{X: 5, Y: 11}, false},
		{Point{X: 20, Y: 20}, false},
	}
	for _, c := range cases {
		if got := poly.Contains(c.pt); got != c.inside {
			t.Errorf("Contains(%v) = %v，期望 %v", c.pt, got, c.inside)
		}
		if d := poly.SignedDistance(c.pt); (d > 0) != c.inside {
			t.Errorf("SignedDistance(%v) = %v，符号与内外判定不一致", c.pt, d)
		}
	}
	if d := poly.SignedDistance(Point{X: 5, Y: 5}); d != 5 {
		t.Errorf("中心点符号距离期望 5，实际 %v", d)
	}
}

// TestAcceptanceScenario 验收边界：
// 围栏 8:00-10:00 有效；车辆 7:50 已在多边形内部；8:00 精准触发 Enter；
// 9:30 离开触发 Exit。
func TestAcceptanceScenario(t *testing.T) {
	mon := NewMonitor(squareFence())
	inside := Point{X: 50, Y: 50}

	// 7:50 车辆已在多边形内部，但围栏未生效，不得产生任何事件。
	if ev := mon.Update("car-1", inside, day(7, 50)); len(ev) != 0 {
		t.Fatalf("7:50 围栏未生效，不应产生事件，实际 %v", eventTypes(ev))
	}
	if mon.State("car-1") != StateOutside {
		t.Fatal("7:50 状态应为 Outside")
	}

	// 8:00 围栏生效瞬间，车辆已在内部，精准触发 Enter。
	ev := mon.Update("car-1", inside, day(8, 0))
	requireEvents(t, ev, EventEnter)
	if ev[0].Time != day(8, 0) {
		t.Fatalf("Enter 触发时刻应为 8:00，实际 %v", ev[0].Time)
	}

	// 9:30 车辆驶出围栏（远超迟滞带），触发 Exit。
	ev = mon.Update("car-1", Point{X: 150, Y: 50}, day(9, 30))
	requireEvents(t, ev, EventExit)
}

// TestNoEventBeforeActivation 围栏生效前在内部移动不应产生任何事件。
func TestNoEventBeforeActivation(t *testing.T) {
	mon := NewMonitor(squareFence())
	for _, tm := range []time.Time{day(7, 0), day(7, 30), day(7, 59)} {
		if ev := mon.Update("car-1", Point{X: 50, Y: 50}, tm); len(ev) != 0 {
			t.Fatalf("%v 围栏未生效，不应产生事件，实际 %v", tm, eventTypes(ev))
		}
	}
}

// TestHysteresisEliminatesJitter 临界边界反复微幅移动时，
// 迟滞带应消除毛刺：跨越边界但未越过对侧迟滞线，不产生任何事件。
func TestHysteresisEliminatesJitter(t *testing.T) {
	mon := NewMonitor(squareFence())

	// 先深入内部建立 Inside 状态（距边界 10 > 迟滞带 2）。
	requireEvents(t, mon.Update("car-1", Point{X: 10, Y: 50}, day(8, 5)), EventEnter)

	// 在边界 x=0 附近 ±1.5 范围内反复抖动（迟滞带为 ±2）：
	// 点交替落在边界内侧 1.5 与外侧 1.5，始终未越过 -2 迟滞线。
	jitter := []Point{
		{X: 1.5, Y: 50}, {X: -1.5, Y: 50},
		{X: 1.0, Y: 50}, {X: -1.0, Y: 50},
		{X: 1.5, Y: 50}, {X: -1.5, Y: 50},
		{X: 0.0, Y: 50}, {X: -1.9, Y: 50},
	}
	for i, pt := range jitter {
		if ev := mon.Update("car-1", pt, day(8, 6).Add(time.Duration(i)*time.Second)); len(ev) != 0 {
			t.Fatalf("第 %d 次抖动采样 %v 产生毛刺事件 %v", i, pt, eventTypes(ev))
		}
	}
	if mon.State("car-1") != StateInside {
		t.Fatal("抖动期间状态应保持 Inside")
	}

	// 真正远离边界（超出迟滞带）才触发 Exit，且仅一次。
	requireEvents(t, mon.Update("car-1", Point{X: -5, Y: 50}, day(8, 20)), EventExit)

	// 出围栏后同样在边界外侧抖动，不应产生毛刺 Enter。
	for i, x := range []float64{-1.5, -0.5, -1.9, -1.0} {
		if ev := mon.Update("car-1", Point{X: x, Y: 50}, day(8, 21).Add(time.Duration(i)*time.Second)); len(ev) != 0 {
			t.Fatalf("外侧抖动 x=%v 产生毛刺事件 %v", x, eventTypes(ev))
		}
	}
	// 深入越过 +2 迟滞线才重新 Enter。
	requireEvents(t, mon.Update("car-1", Point{X: 3, Y: 50}, day(8, 25)), EventEnter)
}

// TestDwellEvent 停留超过阈值触发一次 Dwell，且不重复触发。
func TestDwellEvent(t *testing.T) {
	mon := NewMonitor(squareFence())
	inside := Point{X: 50, Y: 50}

	requireEvents(t, mon.Update("car-1", inside, day(8, 0)), EventEnter)

	// 停留 29 分钟，未达 30 分钟阈值。
	if ev := mon.Update("car-1", inside, day(8, 29)); len(ev) != 0 {
		t.Fatalf("停留 29 分钟不应触发 Dwell，实际 %v", eventTypes(ev))
	}
	// 停留满 30 分钟，触发 Dwell。
	requireEvents(t, mon.Update("car-1", inside, day(8, 30)), EventDwell)
	// 继续停留不重复触发。
	if ev := mon.Update("car-1", inside, day(8, 45)); len(ev) != 0 {
		t.Fatalf("Dwell 不应重复触发，实际 %v", eventTypes(ev))
	}
}

// TestExitOnDeactivation 围栏 10:00 失效时，仍在内部的实体应收到 Exit。
func TestExitOnDeactivation(t *testing.T) {
	mon := NewMonitor(squareFence())
	inside := Point{X: 50, Y: 50}

	requireEvents(t, mon.Update("car-1", inside, day(8, 0)), EventEnter)
	requireEvents(t, mon.Update("car-1", inside, day(10, 0)), EventExit)

	// 失效后不再产生任何事件。
	if ev := mon.Update("car-1", inside, day(10, 30)); len(ev) != 0 {
		t.Fatalf("围栏失效后不应产生事件，实际 %v", eventTypes(ev))
	}
}

// TestDynamicScaling 围栏形状随时间缩放：缩放后原内部点可能落到界外。
func TestDynamicScaling(t *testing.T) {
	fence := squareFence()
	// 8:00 时缩放因子 1.0，线性收缩到 10:00 的 0.5。
	fence.ScaleFunc = func(tm time.Time) float64 {
		frac := float64(tm.Sub(fence.ActiveFrom)) / float64(fence.ActiveUntil.Sub(fence.ActiveFrom))
		return 1.0 - 0.5*frac
	}
	mon := NewMonitor(fence)

	// 8:00 点 (10,50) 距边界 10 > 迟滞带，Enter。
	requireEvents(t, mon.Update("car-1", Point{X: 10, Y: 50}, day(8, 0)), EventEnter)

	// 9:00 缩放因子 0.75，多边形收缩为 [12.5, 87.5]^2，
	// 点 (10,50) 已在界外 2.5，越过 -2 迟滞线，触发 Exit。
	requireEvents(t, mon.Update("car-1", Point{X: 10, Y: 50}, day(9, 0)), EventExit)
}

// TestMultipleEntities 多实体状态相互独立。
func TestMultipleEntities(t *testing.T) {
	mon := NewMonitor(squareFence())

	requireEvents(t, mon.Update("car-1", Point{X: 50, Y: 50}, day(8, 0)), EventEnter)
	if ev := mon.Update("car-2", Point{X: 150, Y: 50}, day(8, 0)); len(ev) != 0 {
		t.Fatalf("car-2 在界外不应产生事件，实际 %v", eventTypes(ev))
	}
	if mon.State("car-1") != StateInside || mon.State("car-2") != StateOutside {
		t.Fatal("多实体状态应相互独立")
	}
}
