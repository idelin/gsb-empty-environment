package session

import (
	"container/heap"
	"fmt"
)

// MsgContext 描述一条待发送的复制消息，供故障注入钩子判定。
type MsgContext struct {
	Seq  int    // 全局消息序号（确定性）
	From string // 发送节点
	To   string // 接收节点
	Key  string // 复制的键
}

// event 是虚拟时间事件。
type event struct {
	time   int64
	seq    int
	cancel *bool
	act    func()
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

// Simulator 是单进程离散事件模拟器：
// 通信（消息延迟/丢失）、故障（节点崩溃/重启）与时间（虚拟时钟）全部由它控制。
type Simulator struct {
	Now   int64
	seq   int
	msgSq int
	queue eventHeap
	nodes map[string]*Node
	// Fault 是故障注入钩子：返回 drop=true 丢弃消息，或改写延迟。
	Fault func(mc MsgContext) (drop bool, delay int64)
	// Trace 记录事件轨迹，作为安全/活性证据。
	Trace []string
}

// NewSimulator 创建空模拟器。
func NewSimulator() *Simulator {
	return &Simulator{nodes: map[string]*Node{}}
}

// After 在 delay 个虚拟时间单位后执行 act，返回可取消标记。
func (s *Simulator) After(delay int64, act func()) (cancel *bool) {
	c := false
	heap.Push(&s.queue, event{time: s.Now + delay, seq: s.seq, cancel: &c, act: act})
	s.seq++
	return &c
}

// Step 执行队首事件；无事件时返回 false。
func (s *Simulator) Step() bool {
	if len(s.queue) == 0 {
		return false
	}
	ev := heap.Pop(&s.queue).(event)
	s.Now = ev.time
	if !*ev.cancel {
		ev.act()
	}
	return true
}

// RunUntil 推进模拟直到 done 为真、事件耗尽或超过 limit。
func (s *Simulator) RunUntil(done func() bool, limit int64) {
	for !done() {
		if len(s.queue) == 0 {
			return
		}
		if s.queue[0].time > limit {
			s.Now = limit
			return
		}
		s.Step()
	}
}

func (s *Simulator) logf(format string, args ...interface{}) {
	s.Trace = append(s.Trace, fmt.Sprintf("[t=%4d] %s", s.Now, fmt.Sprintf(format, args...)))
}

// Node 返回指定 ID 的节点。
func (s *Simulator) Node(id string) *Node { return s.nodes[id] }
