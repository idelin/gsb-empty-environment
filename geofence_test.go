package geofence

import (
	"testing"
	"time"
)

// 测试基准日：2026-10-02，围栏有效期 08:00 ~ 10:00。
func at(hour, min, sec int) time.Time {
	return time.Date(2026, 10, 2, hour, min, sec, 0, time.Local)
}

// 单位正方形围栏：(0,0)-(10,0)-(10,10)-(0,10)，迟滞 0.5。
func squareFence() *Fence {
	return &Fence{
		ID:         "yard-1",
		Polygon:    []Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		ValidFrom:  at(8, 0, 0),
		ValidTo:    at(10, 0, 0),
		Hysteresis: 0.5,
		DwellAfter: 30 * time.Minute,
	}
}

func eventTypes(events []Event) []EventType {
	var out []EventType
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

// 验收场景：7:50 进入（围栏未生效，无事件）→ 8:00 精准触发 Enter →
// 停留超阈值触发 Dwell → 9:30 离开触发 Exit。
func TestAcceptanceScenario(t *testing.T) {
	m := NewMonitor(squareFence())
	car := "vehicle-001"
	inside := Point{5, 5}

	// 7:50 车辆已在多边形内部，但围栏未生效，不应产生任何事件。
	if evs := m.Process(car, inside, at(7, 50, 0)); len(evs) != 0 {
		t.Fatalf("07:50 围栏未生效，期望无事件，实际 %v", eventTypes(evs))
	}
	if m.State(car) {
		t.Fatal("07:50 状态应为 Outside")
	}

	// 7:59:59 仍未生效。
	if evs := m.Process(car, inside, at(7, 59, 59)); len(evs) != 0 {
		t.Fatalf("07:59:59 期望无事件，实际 %v", eventTypes(evs))
	}

	// 8:00 整围栏生效，车辆恰在内部，应精准触发 Enter。
	evs := m.Process(car, inside, at(8, 0, 0))
	if len(evs) != 1 || evs[0].Type != EventEnter {
		t.Fatalf("08:00 期望 [Enter]，实际 %v", eventTypes(evs))
	}
	if !evs[0].Time.Equal(at(8, 0, 0)) {
		t.Fatalf("Enter 触发时刻应为 08:00:00，实际 %v", evs[0].Time)
	}

	// 8:31 停留超过 30 分钟阈值，触发一次 Dwell。
	evs = m.Process(car, inside, at(8, 31, 0))
	if len(evs) != 1 || evs[0].Type != EventDwell {
		t.Fatalf("08:31 期望 [Dwell]，实际 %v", eventTypes(evs))
	}

	// Dwell 只触发一次。
	if evs := m.Process(car, inside, at(8, 45, 0)); len(evs) != 0 {
		t.Fatalf("08:45 期望无重复 Dwell，实际 %v", eventTypes(evs))
	}

	// 9:30 车辆离开（远离边界），触发 Exit。
	evs = m.Process(car, Point{20, 20}, at(9, 30, 0))
	if len(evs) != 1 || evs[0].Type != EventExit {
		t.Fatalf("09:30 期望 [Exit]，实际 %v", eventTypes(evs))
	}
}

// 迟滞边界：在临界边界附近反复微幅移动，不应产生毛刺 Enter/Exit。
func TestHysteresisSuppressesJitter(t *testing.T) {
	m := NewMonitor(squareFence())
	car := "vehicle-002"

	// 8:05 从深处进入，触发 Enter。
	if evs := m.Process(car, Point{5, 5}, at(8, 5, 0)); len(evs) != 1 || evs[0].Type != EventEnter {
		t.Fatalf("期望 Enter，实际 %v", eventTypes(evs))
	}

	// 之后在边界 x=0 附近以 ±0.3 幅度抖动（迟滞宽度 0.5），
	// 点在内侧 0.3 / 外侧 0.3 之间反复横跳，均落在迟滞带内，不应触发 Exit。
	jitter := []Point{{0.3, 5}, {-0.3, 5}, {0.2, 5}, {-0.4, 5}, {0.4, 5}, {-0.2, 5}}
	for i, p := range jitter {
		ts := at(8, 6, i*10)
		if evs := m.Process(car, p, ts); len(evs) != 0 {
			t.Fatalf("抖动点 %v 期望无事件，实际 %v", p, eventTypes(evs))
		}
	}
	if !m.State(car) {
		t.Fatal("抖动期间状态应保持 Inside")
	}

	// 真正远离边界（外侧 2.0 > 迟滞 0.5）才触发 Exit。
	if evs := m.Process(car, Point{-2, 5}, at(8, 10, 0)); len(evs) != 1 || evs[0].Type != EventExit {
		t.Fatalf("期望 Exit，实际 %v", eventTypes(evs))
	}

	// 同理，从外侧贴近边界抖动（迟滞带内）不应触发 Enter。
	jitterOut := []Point{{-0.3, 5}, {0.3, 5}, {-0.1, 5}, {0.4, 5}, {-0.4, 5}}
	for i, p := range jitterOut {
		if evs := m.Process(car, p, at(8, 11, i*10)); len(evs) != 0 {
			t.Fatalf("外侧抖动点 %v 期望无事件，实际 %v", p, eventTypes(evs))
		}
	}
	// 深入内部才触发 Enter。
	if evs := m.Process(car, Point{2, 5}, at(8, 12, 0)); len(evs) != 1 || evs[0].Type != EventEnter {
		t.Fatalf("期望 Enter，实际 %v", eventTypes(evs))
	}
}

// 无迟滞（Hysteresis=0）时边界往返会逐次触发事件，反衬迟滞的必要性。
func TestZeroHysteresisFlapping(t *testing.T) {
	f := squareFence()
	f.Hysteresis = 0
	f.DwellAfter = 0
	m := NewMonitor(f)
	car := "vehicle-003"

	var got []EventType
	got = append(got, eventTypes(m.Process(car, Point{1, 5}, at(8, 1, 0)))...)   // Enter
	got = append(got, eventTypes(m.Process(car, Point{-1, 5}, at(8, 1, 10)))...) // Exit
	got = append(got, eventTypes(m.Process(car, Point{1, 5}, at(8, 1, 20)))...)  // Enter
	want := []EventType{EventEnter, EventExit, EventEnter}
	if len(got) != len(want) {
		t.Fatalf("期望 %v，实际 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("期望 %v，实际 %v", want, got)
		}
	}
}

// 围栏失效期间状态复位；10:00 后不再产生事件。
func TestFenceExpiryResetsState(t *testing.T) {
	m := NewMonitor(squareFence())
	car := "vehicle-004"

	m.Process(car, Point{5, 5}, at(8, 0, 0)) // Enter
	if !m.State(car) {
		t.Fatal("8:00 应为 Inside")
	}
	// 10:00 围栏失效：静默复位，无 Exit 事件。
	if evs := m.Process(car, Point{5, 5}, at(10, 0, 0)); len(evs) != 0 {
		t.Fatalf("失效时刻期望无事件，实际 %v", eventTypes(evs))
	}
	if m.State(car) {
		t.Fatal("失效后状态应复位为 Outside")
	}
	// 失效期间在内部移动无任何事件。
	if evs := m.Process(car, Point{3, 3}, at(10, 30, 0)); len(evs) != 0 {
		t.Fatalf("失效期间期望无事件，实际 %v", eventTypes(evs))
	}
}

// 动态缩放：多边形随时间收缩，同一坐标点在缩放后变为外部。
func TestDynamicScaling(t *testing.T) {
	f := squareFence()
	f.DwellAfter = 0
	// 8:00 时缩放系数 1，线性收缩到 9:00 时为 0.2。
	f.ScaleFn = func(ts time.Time) float64 {
		frac := float64(ts.Sub(at(8, 0, 0))) / float64(time.Hour)
		s := 1.0 - 0.8*frac
		if s < 0.2 {
			s = 0.2
		}
		return s
	}
	m := NewMonitor(f)
	car := "vehicle-005"

	// 8:00 点 (4.5,5) 在原始多边形内（距边界 4.5 > 0.5），触发 Enter。
	if evs := m.Process(car, Point{4.5, 5}, at(8, 0, 0)); len(evs) != 1 || evs[0].Type != EventEnter {
		t.Fatalf("期望 Enter，实际 %v", eventTypes(evs))
	}
	// 9:00 缩放至 0.2，多边形收缩为 (4,4)-(6,6)，点 (4.5,5) 仍在内，无事件。
	if evs := m.Process(car, Point{4.5, 5}, at(9, 0, 0)); len(evs) != 0 {
		t.Fatalf("缩放后仍在内部，期望无事件，实际 %v", eventTypes(evs))
	}
	// 同一时刻点 (1,1) 已在收缩后的多边形之外。
	if m.State(car) != true {
		t.Fatal("车辆应仍处于 Inside")
	}
	if evs := m.Process(car, Point{1, 1}, at(9, 0, 1)); len(evs) != 1 || evs[0].Type != EventExit {
		t.Fatalf("收缩后 (1,1) 在外部，期望 Exit，实际 %v", eventTypes(evs))
	}
}

// 基础几何：射线法包含判定与带符号距离。
func TestGeometry(t *testing.T) {
	poly := []Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	cases := []struct {
		p      Point
		inside bool
	}{
		{Point{5, 5}, true},
		{Point{0, 5}, true},   // 边界上视为内部
		{Point{-1, 5}, false},
		{Point{11, 5}, false},
		{Point{5, -1}, false},
		{Point{10, 10}, true}, // 顶点
	}
	for _, c := range cases {
		if got := pointInPolygon(c.p, poly); got != c.inside {
			t.Errorf("pointInPolygon(%v) = %v, want %v", c.p, got, c.inside)
		}
	}
	if d := signedDistance(Point{5, 5}, poly); d != 5 {
		t.Errorf("signedDistance 内部点 = %v, want 5", d)
	}
	if d := signedDistance(Point{-3, 5}, poly); d != -3 {
		t.Errorf("signedDistance 外部点 = %v, want -3", d)
	}
}
