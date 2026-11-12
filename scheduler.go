package netsec

import (
	"container/heap"
	"errors"
)

// Time 是调度器使用的虚拟时钟：离散、单调、非负，以“tick”为单位。
// tick=0 是调度器创建时的初始时刻；时间只能显式向前推进，绝不倒流。
type Time uint64

// Kind 区分两类事件：
//   - TimeoutEvent 是带截止时刻的定时事件（超时动作）；
//   - NormalEvent 是零耗时消息事件，停留在当前时刻，不消耗任何时间。
type Kind uint8

const (
	// NormalEvent 是零耗时消息事件，在当前 tick 上派发，不推进时钟。
	NormalEvent Kind = iota
	// TimeoutEvent 是截止时刻到达后派发的超时事件。
	TimeoutEvent
)

// Event 是调度器中的一条可派发事件。
type Event struct {
	// Seq 是单调递增的事件编号，用于打破同一时刻、同一类型之间的平局，
	// 保证派发顺序可复现（FIFO）。
	Seq uint64
	// Name 是供宿主模型解释的事件标识。
	Name string
	// Kind 决定派发优先级（TimeoutEvent 优先于 NormalEvent）。
	Kind Kind
	// At 是 TimeoutEvent 的截止时刻；NormalEvent 的 At 等于入队时的时钟。
	At Time
}

// 调度器内部使用的堆条目，在 Event 之外额外携带存活标志以支持惰性取消。
type entry struct {
	Event
	alive bool
	index int // container/heap 维护的堆下标
}

// timerHeap 同时承载超时事件与零耗时事件。排序键依次为：
//  1. 时刻（小者先派发）；
//  2. 类型（同一时刻 TimeoutEvent 先于 NormalEvent）；
//  3. 入队序号（同刻同类按 FIFO 派发）。
type timerHeap []*entry

func (h timerHeap) Len() int { return len(h) }

func (h timerHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.At != b.At {
		return a.At < b.At
	}
	if a.Kind != b.Kind {
		return a.Kind == TimeoutEvent
	}
	return a.Seq < b.Seq
}

func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *timerHeap) Push(x any) {
	e := x.(*entry)
	e.index = len(*h)
	*h = append(*h, e)
}

func (h *timerHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	*h = old[:n-1]
	return e
}

// Handler 由验证宿主实现，负责解释派发出来的事件并驱动模型转移。
type Handler interface {
	Handle(ev Event)
}

// HandlerFunc 让普通函数满足 Handler 接口。
type HandlerFunc func(ev Event)

// Handle 实现 Handler。
func (f HandlerFunc) Handle(ev Event) { f(ev) }

// ErrLivelock 在连续零耗时步数耗尽 MaxZeroSteps 后返回：
// 这意味着普通事件在时钟无法前进的情况下不断自我再生产，
// 本应到期的超时被无限推迟。
var ErrLivelock = errors.New("netsec: zero-duration step budget exhausted; timeout can be postponed forever")

// Scheduler 是供 Go 验证宿主调用的计时调度器。
//
// 它把“时间推进”与“零耗时消息”严格分开：
//   - Post 只在当前时刻插入 NormalEvent，绝不改变时钟；
//   - Advance/AdvanceToNext 是唯一推进时钟的入口；
//   - 时钟到达某截止点时，TimeoutEvent 总是先于同刻的 NormalEvent 派发。
type Scheduler struct {
	clock        Time
	seq          uint64
	heap         timerHeap
	handler      Handler
	maxZeroSteps uint64
	zeroUsed     uint64
	log          []Event
}

// NewScheduler 创建调度器。maxZeroSteps 是相邻两次成功时间推进之间
// 允许连续派发的零耗时事件上限（显式展开边界）。
func NewScheduler(handler Handler, maxZeroSteps uint64) *Scheduler {
	s := &Scheduler{
		handler:      handler,
		maxZeroSteps: maxZeroSteps,
	}
	heap.Init(&s.heap)
	return s
}

// Clock 返回当前虚拟时刻。
func (s *Scheduler) Clock() Time { return s.clock }

// MaxZeroSteps 返回连续零耗时步数的展开边界。
func (s *Scheduler) MaxZeroSteps() uint64 { return s.maxZeroSteps }

// ZeroUsed 返回自上次成功推进时钟以来已派发的零耗时事件数。
func (s *Scheduler) ZeroUsed() uint64 { return s.zeroUsed }

// After 安排一个在 deadline 到期的 TimeoutEvent，返回事件序号。
// deadline 不得早于当前时刻（超时不能安排在过去）。
func (s *Scheduler) After(name string, deadline Time) uint64 {
	if deadline < s.clock {
		panic("netsec: timeout scheduled in the past")
	}
	return s.push(name, TimeoutEvent, deadline)
}

// Post 在当前时刻安排一个零耗时的 NormalEvent，返回事件序号。
// 该事件不消耗任何时间。
func (s *Scheduler) Post(name string) uint64 {
	return s.push(name, NormalEvent, s.clock)
}

// PostAt 安排一个在 at 时刻派发的零耗时 NormalEvent，返回事件序号。
// 它仍然是零耗时消息（派发时不推进、不消耗时钟），只是入队时刻被锚定
// 到 at；at 不得早于当前时刻。同刻派发时 TimeoutEvent 依然优先于它。
// 该入口主要供验证宿主构造“截止同刻存在竞争普通事件”的场景。
func (s *Scheduler) PostAt(name string, at Time) uint64 {
	if at < s.clock {
		panic("netsec: normal event scheduled in the past")
	}
	return s.push(name, NormalEvent, at)
}

func (s *Scheduler) push(name string, kind Kind, at Time) uint64 {
	s.seq++
	e := &entry{
		Event: Event{Seq: s.seq, Name: name, Kind: kind, At: at},
		alive: true,
	}
	heap.Push(&s.heap, e)
	return s.seq
}

// Cancel 惰性取消一个尚未派发的事件；事件不存在或已派发时为空操作。
func (s *Scheduler) Cancel(seq uint64) {
	for _, e := range s.heap {
		if e.Seq == seq && e.alive {
			e.alive = false
			return
		}
	}
}

// Pending 返回当前所有存活、未派发事件的快照，顺序与派发顺序一致。
func (s *Scheduler) Pending() []Event {
	out := make([]Event, 0, len(s.heap))
	for _, e := range s.heap {
		if e.alive {
			out = append(out, e.Event)
		}
	}
	return out
}

// Idle 报告是否已无存活事件。
func (s *Scheduler) Idle() bool {
	s.dropCancelled()
	return s.heap.Len() == 0
}

// Log 返回按派发顺序记录的事件日志快照。
func (s *Scheduler) Log() []Event {
	out := make([]Event, len(s.log))
	copy(out, s.log)
	return out
}

func (s *Scheduler) dropCancelled() {
	kept := s.heap[:0]
	for _, e := range s.heap {
		if e.alive {
			kept = append(kept, e)
		}
	}
	s.heap = s.heap[:0]
	s.heap = append(s.heap, kept...)
	heap.Init(&s.heap)
}

// Step 派发恰好一条可派发事件并返回它。
//
// 派发规则：
//   - TimeoutEvent 只有在 clock >= At 时才可派发；
//   - NormalEvent 在当前时刻即可派发；
//   - 同刻 TimeoutEvent 优先于 NormalEvent。
//
// 若没有可派发事件，ok 为 false。若连续零耗时步数超过 MaxZeroSteps，
// 返回 ErrLivelock，且不派发该事件。
func (s *Scheduler) Step() (ev Event, ok bool, err error) {
	s.dropCancelled()
	if s.heap.Len() == 0 {
		return Event{}, false, nil
	}
	e := s.heap[0]
	if e.Kind == TimeoutEvent && e.At > s.clock {
		// 最近的超时尚未到期，当前又没有零耗时事件可派发。
		return Event{}, false, nil
	}
	heap.Pop(&s.heap)
	if e.Kind == NormalEvent {
		if s.zeroUsed >= s.maxZeroSteps {
			// 把事件放回，保持调度器状态可被宿主检查/恢复。
			heap.Push(&s.heap, e)
			return Event{}, false, ErrLivelock
		}
		s.zeroUsed++
	} else {
		// 一次成功的超时派发意味着时间确实走到了新的截止点，
		// 零耗时步数计数随之复位。
		s.zeroUsed = 0
	}
	s.log = append(s.log, e.Event)
	s.handler.Handle(e.Event)
	return e.Event, true, nil
}

// drain 派发到当前时刻静止：没有可立即派发的事件为止。
// 遇到 ErrLivelock 直接向上返回。
func (s *Scheduler) drain() error {
	for {
		_, ok, err := s.Step()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
}

// Advance 把虚拟时钟向前推进 delta 个 tick，并在推进后把所有
// At <= 新时刻的事件派发至静止。时间推进先于派发，因此截止点一到，
// TimeoutEvent 必然先于同刻的 NormalEvent。零耗时计数在成功推进后复位。
func (s *Scheduler) Advance(delta uint64) error {
	s.clock += Time(delta)
	s.zeroUsed = 0
	return s.drain()
}

// AdvanceToNext 直接跳到下一个存活 TimeoutEvent 的截止时刻并派发至静止。
// 若不存在存活的超时事件则返回 false 且不改变时钟。
func (s *Scheduler) AdvanceToNext() (reached Time, ok bool, err error) {
	s.dropCancelled()
	var target Time
	found := false
	for _, e := range s.heap {
		if e.Kind == TimeoutEvent {
			if !found || e.At < target {
				target = e.At
				found = true
			}
		}
	}
	if !found {
		return s.clock, false, nil
	}
	if target > s.clock {
		s.clock = target
		s.zeroUsed = 0
	}
	err = s.drain()
	return s.clock, true, err
}
