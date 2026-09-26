package main

import (
	"container/heap"
	"math/rand"
	"sort"
)

// Config 控制单进程事件模拟器：通信延迟、故障注入、时间与复核策略。
// 虚拟时钟由事件堆驱动，不依赖真实时间，全部行为可由种子复现。
type Config struct {
	MinDelay       int64   // 消息最小延迟（虚拟时间单位）
	MaxDelay       int64   // 消息最大延迟
	DropRate       float64 // 消息丢失概率（故障注入）
	DupRate        float64 // 消息重复概率（故障注入）
	ForceCollision bool    // 强制所有摘要相同（碰撞注入）
	VerifyEvery    int     // 每 N 轮做一次复核（0=从不）
	SyncInterval   int64   // 同步轮间隔（虚拟时间单位）
}

// Metrics 汇总传输量，是“相同子树不传全量”“不动点”的直接证据。
type Metrics struct {
	MsgsSent      int
	DigestMsgs    int
	DigestEntries int
	RecordMsgs    int
	RecordsSent   int
	Dropped       int
	Duplicated    int
	SentKeys      map[string]int // 被传输过的键 -> 次数
}

type event struct {
	time int64
	seq  int
	msg  Msg
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

// Sim 是单进程事件模拟器：虚拟时钟 + 事件优先队列 + 故障注入网络。
type Sim struct {
	now      int64
	cfg      Config
	rng      *rand.Rand
	queue    eventHeap
	seq      int
	nodes    map[string]*Node
	timersOn bool
	Metrics  Metrics
}

func NewSim(cfg Config, seed int64) *Sim {
	return &Sim{
		cfg:   cfg,
		rng:   rand.New(rand.NewSource(seed)),
		nodes: map[string]*Node{},
		Metrics: Metrics{
			SentKeys: map[string]int{},
		},
	}
}

func (s *Sim) AddNode(n *Node) { s.nodes[n.id] = n }

// Send 发送一条消息：计入指标，按配置注入重复与丢失，并按随机延迟投递。
func (s *Sim) Send(m Msg) {
	s.Metrics.MsgsSent++
	if m.Kind == MsgDigest {
		s.Metrics.DigestMsgs++
		s.Metrics.DigestEntries += len(m.Entries)
	} else {
		s.Metrics.RecordMsgs++
		s.Metrics.RecordsSent += len(m.Records)
		for _, r := range m.Records {
			s.Metrics.SentKeys[r.Key]++
		}
	}
	s.deliver(m)
	if s.rng.Float64() < s.cfg.DupRate {
		s.Metrics.Duplicated++
		s.deliver(m)
	}
}

func (s *Sim) deliver(m Msg) {
	if s.rng.Float64() < s.cfg.DropRate {
		s.Metrics.Dropped++
		return
	}
	delay := s.cfg.MinDelay
	if s.cfg.MaxDelay > s.cfg.MinDelay {
		delay += s.rng.Int63n(s.cfg.MaxDelay - s.cfg.MinDelay + 1)
	}
	s.seq++
	heap.Push(&s.queue, event{time: s.now + delay, seq: s.seq, msg: m})
}

// StartTimers 为每个节点启动周期性同步定时器（按节点 ID 排序保证确定性）。
func (s *Sim) StartTimers() {
	s.timersOn = true
	ids := make([]string, 0, len(s.nodes))
	for id := range s.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s.scheduleTimer(s.nodes[id], s.cfg.SyncInterval)
	}
}

// StopTimers 停止周期性同步；已在途的消息仍会被投递。
func (s *Sim) StopTimers() { s.timersOn = false }

func (s *Sim) scheduleTimer(n *Node, dt int64) {
	s.seq++
	heap.Push(&s.queue, event{time: s.now + dt, seq: s.seq, fn: func() {
		n.SyncRound()
		if s.timersOn {
			s.scheduleTimer(n, s.cfg.SyncInterval)
		}
	}})
}

// Run 处理事件直到虚拟时钟到达 until。
func (s *Sim) Run(until int64) {
	for len(s.queue) > 0 {
		e := heap.Pop(&s.queue).(event)
		if e.time > until {
			heap.Push(&s.queue, e)
			return
		}
		s.now = e.time
		if e.fn != nil {
			e.fn()
		} else if n, ok := s.nodes[e.msg.To]; ok {
			n.onMsg(e.msg)
		}
	}
}

// RunUntilIdle 排空事件队列（调用前应先 StopTimers，否则定时器永不停止）。
func (s *Sim) RunUntilIdle() { s.Run(1 << 62) }

// Now 返回当前虚拟时间。
func (s *Sim) Now() int64 { return s.now }
