// Package session 实现可撤销的会话超时合并窗口。
//
// 相邻事件时间间隔不超过 gap 的事件被合并进同一个会话窗口。
// 迟到事件可以把原本独立的两个窗口连接起来；事件删除也可能使窗口分裂。
// 每次变更都会输出 retract/upsert 变更流；水位线推进后，
// 超出最大延迟范围的状态会被丢弃。
package session

import "sort"

// ChangeType 表示变更流中一条记录的类型。
type ChangeType int

const (
	// Retract 撤销一个此前已发出的窗口。
	Retract ChangeType = iota
	// Upsert 发出（或更新）一个窗口。
	Upsert
)

func (c ChangeType) String() string {
	if c == Retract {
		return "RETRACT"
	}
	return "UPSERT"
}

// Window 是一个会话窗口，[Start, End] 为窗口内最早/最晚事件时间。
type Window struct {
	Start int64
	End   int64
}

// Change 是窗口变更流中的一条记录。
type Change struct {
	Type   ChangeType
	Window Window
}

// Merger 维护事件集合并据此计算会话窗口，输出 retract/upsert 变更流。
type Merger struct {
	gap         int64
	maxLateness int64
	watermark   int64
	events      map[int64]int // 事件时间 -> 出现次数（支持重复事件与删除）
	windows     []Window      // 当前已发出的窗口（有序、不重叠）
}

// NewMerger 创建合并器。gap 为会话超时间隔，maxLateness 为允许的最大迟到。
func NewMerger(gap, maxLateness int64) *Merger {
	return &Merger{
		gap:         gap,
		maxLateness: maxLateness,
		watermark:   -1 << 62,
		events:      make(map[int64]int),
	}
}

// Watermark 返回当前水位线。
func (m *Merger) Watermark() int64 { return m.watermark }

// Windows 返回当前生效的窗口快照。
func (m *Merger) Windows() []Window {
	out := make([]Window, len(m.windows))
	copy(out, m.windows)
	return out
}

// EventCount 返回当前保留的事件状态数量（用于观察状态清理）。
func (m *Merger) EventCount() int {
	n := 0
	for _, c := range m.events {
		n += c
	}
	return n
}

// tooLate 判断事件是否超出水位线允许的最大延迟范围。
func (m *Merger) tooLate(t int64) bool {
	return t < m.watermark-m.maxLateness
}

// Add 处理一个到达的事件，返回窗口变更流。
// 超出最大延迟范围的迟到事件被直接丢弃。
func (m *Merger) Add(t int64) []Change {
	if m.tooLate(t) {
		return nil
	}
	m.events[t]++
	return m.recompute()
}

// Remove 删除一个事件，返回窗口变更流（窗口可能因此分裂）。
func (m *Merger) Remove(t int64) []Change {
	if m.events[t] == 0 {
		return nil
	}
	m.events[t]--
	if m.events[t] == 0 {
		delete(m.events, t)
	}
	return m.recompute()
}

// AdvanceWatermark 推进水位线（单调不减），并丢弃超出最大延迟范围的事件状态。
// 状态清理可能引起窗口变化，相应变更一并返回。
func (m *Merger) AdvanceWatermark(wm int64) []Change {
	if wm > m.watermark {
		m.watermark = wm
	}
	cutoff := m.watermark - m.maxLateness
	changed := false
	for t := range m.events {
		if t < cutoff {
			delete(m.events, t)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return m.recompute()
}

// recompute 由当前事件集合重新计算全部窗口，与已发出窗口做 diff，
// 输出 retract（消失的窗口）与 upsert（新出现的窗口）。
func (m *Merger) recompute() []Change {
	next := computeWindows(m.sortedTimes(), m.gap)
	var changes []Change
	for _, w := range m.windows {
		if !containsWindow(next, w) {
			changes = append(changes, Change{Type: Retract, Window: w})
		}
	}
	for _, w := range next {
		if !containsWindow(m.windows, w) {
			changes = append(changes, Change{Type: Upsert, Window: w})
		}
	}
	m.windows = next
	return changes
}

func (m *Merger) sortedTimes() []int64 {
	times := make([]int64, 0, len(m.events))
	for t := range m.events {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	return times
}

// computeWindows 把有序事件时间按 gap 聚合成会话窗口。
func computeWindows(times []int64, gap int64) []Window {
	var windows []Window
	for _, t := range times {
		if n := len(windows); n > 0 && t-windows[n-1].End <= gap {
			windows[n-1].End = t
		} else {
			windows = append(windows, Window{Start: t, End: t})
		}
	}
	return windows
}

func containsWindow(ws []Window, w Window) bool {
	for _, x := range ws {
		if x == w {
			return true
		}
	}
	return false
}
