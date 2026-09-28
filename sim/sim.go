// Package sim 提供整数 tick 的离散事件模型：一个最小堆事件调度器，
// 以及一条只在调度器上安排投递事件的有损/乱序链路。整个过程不产生
// 任何真实网络流量。
package sim

import (
	"container/heap"
	"errors"
)

// EventFn 是在某个 tick 触发的回调。返回值表示该回调又请求追加的
// 零个或多个事件（相对延迟必须非负）。
type EventFn func(now int64) []Event

// Event 是一个待调度事件。At 为绝对 tick；同 tick 内按入队次序
// （Seq 自动编号）依次触发。
type Event struct {
	At  int64
	Fn  EventFn
	Seq int64
}

// Scheduler 是离散事件调度器。
type Scheduler struct {
	queue  eventHeap
	now    int64
	serial int64
}

// NewScheduler 创建从 tick 0 开始的调度器。
func NewScheduler() *Scheduler {
	return &Scheduler{queue: make(eventHeap, 0)}
}

// Now 返回当前 tick。
func (s *Scheduler) Now() int64 { return s.now }

// Schedule 在绝对 tick at 安排事件；不允许安排到过去。
func (s *Scheduler) Schedule(at int64, fn EventFn) error {
	if at < s.now {
		return errors.New("sim: cannot schedule event in the past")
	}
	if fn == nil {
		return errors.New("sim: nil event function")
	}
	s.serial++
	heap.Push(&s.queue, scheduledEvent{at: at, seq: s.serial, fn: fn})
	return nil
}

// After 在当前 tick 之后 delay 个 tick 安排事件（delay 必须非负）。
func (s *Scheduler) After(delay int64, fn EventFn) error {
	if delay < 0 {
		return errors.New("sim: negative delay")
	}
	return s.Schedule(s.now+delay, fn)
}

// RunUntil 依次触发所有 At <= until 的事件并推进时钟；同一 tick 的
// 事件全部处理完后调用该 tick 的 hook（若注册），回调在执行期间
// 追加的事件只要落在 until 之前同样会被执行。返回时 Now() == until。
func (s *Scheduler) RunUntil(until int64, hook func(tick int64)) error {
	if until < s.now {
		return errors.New("sim: until is in the past")
	}
	for s.now <= until {
		// 处理当前及以前所有待触发事件（正常情况下没有“以前”的）。
		for len(s.queue) > 0 && s.queue[0].at <= s.now {
			ev := heap.Pop(&s.queue).(scheduledEvent)
			extra := ev.fn(s.now)
			for i := range extra {
				if err := s.Schedule(extra[i].At, extra[i].Fn); err != nil {
					return err
				}
			}
		}
		if hook != nil {
			hook(s.now)
		}
		if s.now == until {
			break
		}
		s.now++
	}
	s.now = until
	return nil
}

// Pending 返回尚未触发的事件数。
func (s *Scheduler) Pending() int { return s.queue.Len() }

type scheduledEvent struct {
	at  int64
	seq int64
	fn  EventFn
}

type eventHeap []scheduledEvent

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}

func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(scheduledEvent)) }

func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
