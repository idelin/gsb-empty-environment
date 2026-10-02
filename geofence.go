// Package geofence 实现多边形时空地理围栏进出事件判定机。
//
// 特性：
//  - 围栏带时间有效期（ValidFrom ~ ValidTo），仅在有效期内产生事件；
//  - 围栏多边形支持随时间动态缩放（ScaleFn）；
//  - 基于带符号距离的迟滞（hysteresis）边界，消除临界抖动造成的毛刺事件；
//  - 实时输出 Enter / Dwell / Exit 状态变迁事件。
package geofence

import (
	"math"
	"time"
)

// Point 平面坐标点（经纬度或投影坐标均可，单位需与 Hysteresis 一致）。
type Point struct {
	X, Y float64
}

// EventType 事件类型。
type EventType string

const (
	EventEnter EventType = "Enter"
	EventDwell EventType = "Dwell"
	EventExit  EventType = "Exit"
)

// Event 一次状态变迁事件。
type Event struct {
	EntityID string
	FenceID  string
	Type     EventType
	Time     time.Time
	Point    Point
}

// Fence 时空多边形围栏。
type Fence struct {
	ID        string
	Polygon   []Point   // 多边形顶点，按顺序（顺时针/逆时针均可），不要求闭合
	ValidFrom time.Time // 生效起始时刻（含）
	ValidTo   time.Time // 失效时刻（不含）

	// Hysteresis 迟滞宽度（与坐标同单位）。
	// 进入需深入边界内超过 Hysteresis，离开需越出边界超过 Hysteresis，
	// 在 [-Hysteresis, +Hysteresis] 迟滞带内保持原状态不变，消除边界抖动。
	Hysteresis float64

	// DwellAfter 停留判定阈值：进入后持续停留超过该时长触发一次 Dwell。
	// 为 0 表示不启用 Dwell 判定。
	DwellAfter time.Duration

	// ScaleFn 可选：返回 t 时刻多边形相对质心的缩放系数（1 为原始形状）。
	// 为 nil 时恒为 1，即形状不随时间变化。
	ScaleFn func(t time.Time) float64
}

// Active 报告围栏在时刻 t 是否生效。
func (f *Fence) Active(t time.Time) bool {
	return !t.Before(f.ValidFrom) && t.Before(f.ValidTo)
}

// shapeAt 返回 t 时刻生效的多边形（应用动态缩放）。
func (f *Fence) shapeAt(t time.Time) []Point {
	s := 1.0
	if f.ScaleFn != nil {
		s = f.ScaleFn(t)
	}
	if s == 1.0 {
		return f.Polygon
	}
	cx, cy := centroid(f.Polygon)
	out := make([]Point, len(f.Polygon))
	for i, p := range f.Polygon {
		out[i] = Point{X: cx + (p.X-cx)*s, Y: cy + (p.Y-cy)*s}
	}
	return out
}

func centroid(poly []Point) (float64, float64) {
	var sx, sy float64
	for _, p := range poly {
		sx += p.X
		sy += p.Y
	}
	n := float64(len(poly))
	return sx / n, sy / n
}

// pointInPolygon 射线法判定点是否在多边形内部（边界上视为内部）。
func pointInPolygon(p Point, poly []Point) bool {
	n := len(poly)
	if n < 3 {
		return false
	}
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		pi, pj := poly[i], poly[j]
		if onSegment(p, pj, pi) {
			return true
		}
		if (pi.Y > p.Y) != (pj.Y > p.Y) {
			x := (pj.X-pi.X)*(p.Y-pi.Y)/(pj.Y-pi.Y) + pi.X
			if p.X < x {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

func onSegment(p, a, b Point) bool {
	const eps = 1e-9
	cross := (b.X-a.X)*(p.Y-a.Y) - (b.Y-a.Y)*(p.X-a.X)
	if math.Abs(cross) > eps {
		return false
	}
	dot := (p.X-a.X)*(b.X-a.X) + (p.Y-a.Y)*(b.Y-a.Y)
	if dot < 0 {
		return false
	}
	lenSq := (b.X-a.X)*(b.X-a.X) + (b.Y-a.Y)*(b.Y-a.Y)
	return dot <= lenSq
}

// distToBoundary 点到多边形边界的最短距离。
func distToBoundary(p Point, poly []Point) float64 {
	n := len(poly)
	min := math.Inf(1)
	j := n - 1
	for i := 0; i < n; i++ {
		d := distToSegment(p, poly[j], poly[i])
		if d < min {
			min = d
		}
		j = i
	}
	return min
}

func distToSegment(p, a, b Point) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	lenSq := dx*dx + dy*dy
	if lenSq == 0 {
		return math.Hypot(p.X-a.X, p.Y-a.Y)
	}
	t := ((p.X-a.X)*dx + (p.Y-a.Y)*dy) / lenSq
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return math.Hypot(p.X-(a.X+t*dx), p.Y-(a.Y+t*dy))
}

// signedDistance 带符号距离：多边形内部为正，外部为负，绝对值为到边界的最短距离。
func signedDistance(p Point, poly []Point) float64 {
	d := distToBoundary(p, poly)
	if pointInPolygon(p, poly) {
		return d
	}
	return -d
}

// entityState 单个实体在某围栏上的状态机。
type entityState int

const (
	stateOutside entityState = iota
	stateInside
)

type track struct {
	state        entityState
	enteredAt    time.Time
	dwellEmitted bool
}

// Monitor 围栏监控器：接收实体轨迹点流，输出 Enter/Dwell/Exit 事件。
type Monitor struct {
	fence  *Fence
	tracks map[string]*track
}

// NewMonitor 创建针对单个围栏的监控器。
func NewMonitor(f *Fence) *Monitor {
	return &Monitor{fence: f, tracks: make(map[string]*track)}
}

// Process 处理实体 entityID 在时刻 t 的轨迹点 p，返回本次产生的事件（可能为空）。
//
// 状态机（迟滞）：
//   Outside --(生效 且 带符号距离 > +H)--> Inside  触发 Enter
//   Inside  --(带符号距离 < -H)--> Outside          触发 Exit
//   迟滞带 [-H, +H] 内状态保持不变，杜绝边界抖动穿透。
// 围栏失效期间不产生任何事件，实体状态重置为 Outside；
// 围栏重新生效时若实体已在内部，则在该时刻触发 Enter。
func (m *Monitor) Process(entityID string, p Point, t time.Time) []Event {
	tr, ok := m.tracks[entityID]
	if !ok {
		tr = &track{state: stateOutside}
		m.tracks[entityID] = tr
	}

	if !m.fence.Active(t) {
		// 围栏未生效：静默复位，不产生事件。
		tr.state = stateOutside
		tr.dwellEmitted = false
		return nil
	}

	var events []Event
	d := signedDistance(p, m.fence.shapeAt(t))
	h := m.fence.Hysteresis

	switch tr.state {
	case stateOutside:
		if d > h {
			tr.state = stateInside
			tr.enteredAt = t
			tr.dwellEmitted = false
			events = append(events, Event{EntityID: entityID, FenceID: m.fence.ID, Type: EventEnter, Time: t, Point: p})
		}
	case stateInside:
		if d < -h {
			tr.state = stateOutside
			tr.dwellEmitted = false
			events = append(events, Event{EntityID: entityID, FenceID: m.fence.ID, Type: EventExit, Time: t, Point: p})
		} else if m.fence.DwellAfter > 0 && !tr.dwellEmitted && !t.Before(tr.enteredAt.Add(m.fence.DwellAfter)) {
			tr.dwellEmitted = true
			events = append(events, Event{EntityID: entityID, FenceID: m.fence.ID, Type: EventDwell, Time: t, Point: p})
		}
	}
	return events
}

// State 返回实体当前是否在围栏内（用于查询/测试）。
func (m *Monitor) State(entityID string) (inside bool) {
	if tr, ok := m.tracks[entityID]; ok {
		return tr.state == stateInside
	}
	return false
}
