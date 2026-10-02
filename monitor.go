package geofence

import "time"

// State 表示实体相对围栏的归属状态。
type State int

const (
	// StateOutside 实体在围栏之外（或围栏未生效）。
	StateOutside State = iota
	// StateInside 实体在围栏之内。
	StateInside
)

func (s State) String() string {
	if s == StateInside {
		return "Inside"
	}
	return "Outside"
}

// EventType 表示围栏事件类型。
type EventType int

const (
	// EventEnter 实体进入围栏。
	EventEnter EventType = iota
	// EventDwell 实体在围栏内停留超过阈值时长。
	EventDwell
	// EventExit 实体离开围栏（含围栏失效时的强制离开）。
	EventExit
)

func (e EventType) String() string {
	switch e {
	case EventEnter:
		return "Enter"
	case EventDwell:
		return "Dwell"
	case EventExit:
		return "Exit"
	}
	return "Unknown"
}

// Event 表示一次围栏状态变迁事件。
type Event struct {
	EntityID string
	Type     EventType
	Time     time.Time
	Position Point
}

// entityState 记录单个实体的内部跟踪状态。
type entityState struct {
	state        State
	enteredAt    time.Time
	dwellEmitted bool
}

// Monitor 基于围栏与迟滞带，对多个实体的轨迹流做实时事件判定。
type Monitor struct {
	fence   *Fence
	entries map[string]*entityState
}

// NewMonitor 创建一个针对指定围栏的监控器。
func NewMonitor(fence *Fence) *Monitor {
	return &Monitor{
		fence:   fence,
		entries: make(map[string]*entityState),
	}
}

// State 返回实体当前状态（未知实体视为 Outside）。
func (m *Monitor) State(entityID string) State {
	if es, ok := m.entries[entityID]; ok {
		return es.state
	}
	return StateOutside
}

// Update 输入实体在 t 时刻的位置，返回本次采样触发的全部事件（通常 0 或 1 个）。
//
// 判定规则：
//   - 围栏未生效：不产生 Enter/Dwell；若实体处于 Inside 则补发 Exit 并复位；
//   - Outside -> Inside：符号距离 > +Hysteresis（须深入越过迟滞带）；
//   - Inside -> Outside：符号距离 < -Hysteresis（须彻底越出迟滞带）；
//   - 迟滞带内的采样保持原状态，杜绝边界抖动穿透；
//   - Inside 且停留时长 >= DwellAfter（且未上报过）：触发一次 Dwell。
func (m *Monitor) Update(entityID string, pt Point, t time.Time) []Event {
	es, ok := m.entries[entityID]
	if !ok {
		es = &entityState{state: StateOutside}
		m.entries[entityID] = es
	}

	if !m.fence.ActiveAt(t) {
		if es.state == StateInside {
			es.state = StateOutside
			es.dwellEmitted = false
			return []Event{{EntityID: entityID, Type: EventExit, Time: t, Position: pt}}
		}
		return nil
	}

	d := m.fence.SignedDistanceAt(pt, t)
	var events []Event

	switch es.state {
	case StateOutside:
		if d > m.fence.Hysteresis {
			es.state = StateInside
			es.enteredAt = t
			es.dwellEmitted = false
			events = append(events, Event{EntityID: entityID, Type: EventEnter, Time: t, Position: pt})
		}
	case StateInside:
		if d < -m.fence.Hysteresis {
			es.state = StateOutside
			es.dwellEmitted = false
			events = append(events, Event{EntityID: entityID, Type: EventExit, Time: t, Position: pt})
		} else if m.fence.DwellAfter > 0 && !es.dwellEmitted && !t.Before(es.enteredAt.Add(m.fence.DwellAfter)) {
			es.dwellEmitted = true
			events = append(events, Event{EntityID: entityID, Type: EventDwell, Time: t, Position: pt})
		}
	}
	return events
}
