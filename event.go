package sim

import "container/heap"

// Event 是调度在整数 tick 上执行的离散事件。
type Event struct {
	Tick  int
	Order int // 同 tick 内按入队顺序执行，保证确定性
	Fn    func()
}

type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].Tick != h[j].Tick {
		return h[i].Tick < h[j].Tick
	}
	return h[i].Order < h[j].Order
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(Event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	ev := old[n-1]
	*h = old[:n-1]
	return ev
}

// Scheduler 是整数 tick 的离散事件调度器，不产生任何真实网络流量。
type Scheduler struct {
	now     int
	counter int
	h       eventHeap
}

func NewScheduler() *Scheduler { return &Scheduler{} }

func (s *Scheduler) Now() int { return s.now }

// At 在指定 tick 调度事件；早于当前时间的事件被钳制到当前 tick。
func (s *Scheduler) At(tick int, fn func()) {
	if tick < s.now {
		tick = s.now
	}
	heap.Push(&s.h, Event{Tick: tick, Order: s.counter, Fn: fn})
	s.counter++
}

// After 在 delay 个 tick 之后调度事件。
func (s *Scheduler) After(delay int, fn func()) { s.At(s.now+delay, fn) }

// Pending 返回尚未执行的事件数。
func (s *Scheduler) Pending() int { return len(s.h) }

// Run 依次执行事件，直到队列空或超过 maxTicks。
func (s *Scheduler) Run(maxTicks int) {
	for len(s.h) > 0 {
		ev := heap.Pop(&s.h).(Event)
		if ev.Tick > maxTicks {
			break
		}
		s.now = ev.Tick
		ev.Fn()
	}
}
