package main

import "container/heap"

// SimEvent 是一个计划在虚拟时刻 at 执行的事件。
type SimEvent struct {
	at        int64
	seq       int
	cancelled bool
	fn        func()
}

// Cancel 取消一个尚未执行的事件。
func (e *SimEvent) Cancel() { e.cancelled = true }

type eventHeap []*SimEvent

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(*SimEvent)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Sim 是单进程事件模拟器：虚拟时钟 + 优先队列。
// 所有通信延迟、超时、故障都由它调度，因此完全确定、可复现。
type Sim struct {
	now   int64
	seq   int
	queue eventHeap
}

func NewSim() *Sim { return &Sim{} }

// Now 返回当前虚拟时间。
func (s *Sim) Now() int64 { return s.now }

// After 在 d 个虚拟时间单位后执行 fn。
func (s *Sim) After(d int64, fn func()) *SimEvent {
	e := &SimEvent{at: s.now + d, seq: s.seq, fn: fn}
	s.seq++
	heap.Push(&s.queue, e)
	return e
}

// Run 处理所有事件直到队列清空。
func (s *Sim) Run() {
	for len(s.queue) > 0 {
		e := heap.Pop(&s.queue).(*SimEvent)
		if e.cancelled {
			continue
		}
		s.now = e.at
		e.fn()
	}
}

// Pending 返回队列中尚未执行的事件数（含已取消）。
func (s *Sim) Pending() int { return len(s.queue) }
