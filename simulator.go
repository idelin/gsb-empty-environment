package sessionkv

import "container/heap"

type event struct {
	time int64
	seq  int64
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
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Link 是 from->to 的单向链路；Up=false 表示网络分区，消息被丢弃。
type Link struct {
	Up    bool
	Delay int64
}

// Simulator 是单进程离散事件模拟器：
// 通信延迟、故障注入与虚拟时间全部在此受控，不依赖真实时钟与网络。
type Simulator struct {
	now   int64
	seq   int64
	queue eventHeap
	links map[[2]string]*Link
}

// NewSimulator 创建虚拟时间为 0 的模拟器。
func NewSimulator() *Simulator {
	return &Simulator{links: map[[2]string]*Link{}}
}

// Now 返回当前虚拟时间。
func (s *Simulator) Now() int64 { return s.now }

// After 在 delay 个虚拟时间单位后执行 fn。
func (s *Simulator) After(delay int64, fn func()) {
	s.seq++
	heap.Push(&s.queue, event{time: s.now + delay, seq: s.seq, fn: fn})
}

// SetLink 配置 from->to 链路。
func (s *Simulator) SetLink(from, to string, up bool, delay int64) {
	s.links[[2]string{from, to}] = &Link{Up: up, Delay: delay}
}

// Send 按链路状态投递消息；链路断开时消息丢弃（异步复制，不重试）。
func (s *Simulator) Send(from, to string, deliver func()) {
	link := s.links[[2]string{from, to}]
	if link == nil || !link.Up {
		return
	}
	s.After(link.Delay, deliver)
}

// Run 执行所有已排程事件（包括执行中新排程的事件），直到队列清空。
func (s *Simulator) Run() {
	for len(s.queue) > 0 {
		e := heap.Pop(&s.queue).(event)
		s.now = e.time
		e.fn()
	}
}
