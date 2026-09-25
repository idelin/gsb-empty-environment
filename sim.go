package main

import (
	"container/heap"
	"fmt"
)

// 单进程事件模拟器: 所有通信、故障与时间推进都由它控制。
// 时间是虚拟的(整数毫秒), 事件按 (时间, 序号) 全序执行, 完全确定性。

type event struct {
	time int64
	seq  int
	fn   func()
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

type Sim struct {
	now int64
	seq int
	q   eventHeap
	log []string
}

func NewSim() *Sim { return &Sim{} }

// Now 返回虚拟时钟当前时刻(ms)。
func (s *Sim) Now() int64 { return s.now }

// After 在 d 毫秒后执行 fn。
func (s *Sim) After(d int64, fn func()) {
	heap.Push(&s.q, event{time: s.now + d, seq: s.seq, fn: fn})
	s.seq++
}

// Logf 追加一条带时间戳的证据日志。
func (s *Sim) Logf(format string, args ...interface{}) {
	s.log = append(s.log, fmt.Sprintf("[t=%5dms] %s", s.now, fmt.Sprintf(format, args...)))
}

// Run 依次执行所有事件直到队列清空。
func (s *Sim) Run() {
	for len(s.q) > 0 {
		e := heap.Pop(&s.q).(event)
		s.now = e.time
		e.fn()
	}
}

// Log 返回完整事件日志(安全/活性证据的原始材料)。
func (s *Sim) Log() []string { return s.log }
