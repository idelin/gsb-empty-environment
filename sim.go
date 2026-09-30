package main

import (
	"container/heap"
	"fmt"
)

// SimConfig 是离散事件模型参数，时间一律为整数 tick。
type SimConfig struct {
	ServiceTicks  int // 每个包的规则匹配处理时长
	LinkDelay     int // 链路传播时延
	BytesPerTick  int // 链路发送速率（字节/tick），决定串行化时延
	QueueCapacity int // 输入队列容量，0 表示不限
}

// 事件种类。
const (
	evArrival = iota
	evServiceDone
	evTxDone
	evDelivered
)

type event struct {
	tick int
	seq  int
	kind int
	pkt  Packet
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
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

// LogEntry 是一条仿真日志。
type LogEntry struct {
	Tick     int
	Event    string
	PacketID int
	Detail   string
}

func (e LogEntry) String() string {
	return fmt.Sprintf("tick=%-4d pkt#%-3d %-10s %s", e.Tick, e.PacketID, e.Event, e.Detail)
}

// Stats 是仿真统计。
type Stats struct {
	Hits      []int // 每条规则命中次数
	Default   int   // 未命中任何规则（默认拒绝）次数
	Delivered int
	Dropped   int
	MaxQueue  int
}

// Sim 是单节点离散事件仿真器：到达 -> 输入队列 -> 匹配处理 -> 链路 -> 交付。
// 全部在内存中运行，不产生任何真实网络流量。
type Sim struct {
	cfg       SimConfig
	rules     []Rule
	events    *eventHeap
	now       int
	seq       int
	queue     []Packet
	busy      bool
	cur       Packet
	curRule   int
	linkQueue []Packet
	linkBusy  bool
	Log       []LogEntry
	Stats     Stats
}

func NewSim(cfg SimConfig, rules []Rule) *Sim {
	if cfg.ServiceTicks < 1 {
		cfg.ServiceTicks = 1
	}
	if cfg.BytesPerTick < 1 {
		cfg.BytesPerTick = 1
	}
	h := &eventHeap{}
	heap.Init(h)
	return &Sim{cfg: cfg, rules: rules, events: h, Stats: Stats{Hits: make([]int, len(rules))}}
}

func (s *Sim) log(ev string, pkt Packet, detail string) {
	s.Log = append(s.Log, LogEntry{Tick: s.now, Event: ev, PacketID: pkt.ID, Detail: detail})
}

func (s *Sim) schedule(kind, tick int, pkt Packet) {
	heap.Push(s.events, event{tick: tick, seq: s.seq, kind: kind, pkt: pkt})
	s.seq++
}

// Run 把所有包按 Arrival tick 注入模型并跑到事件耗尽，返回统计。
func (s *Sim) Run(pkts []Packet) Stats {
	for _, p := range pkts {
		s.schedule(evArrival, p.Arrival, p)
	}
	for s.events.Len() > 0 {
		e := heap.Pop(s.events).(event)
		s.now = e.tick
		switch e.kind {
		case evArrival:
			s.onArrival(e.pkt)
		case evServiceDone:
			s.onServiceDone()
		case evTxDone:
			s.onTxDone(e.pkt)
		case evDelivered:
			s.onDelivered(e.pkt)
		}
	}
	return s.Stats
}

func (s *Sim) onArrival(pkt Packet) {
	if s.cfg.QueueCapacity > 0 && len(s.queue) >= s.cfg.QueueCapacity {
		s.Stats.Dropped++
		s.log("drop", pkt, "输入队列已满")
		return
	}
	s.queue = append(s.queue, pkt)
	if len(s.queue) > s.Stats.MaxQueue {
		s.Stats.MaxQueue = len(s.queue)
	}
	s.log("enqueue", pkt, fmt.Sprintf("队列长度=%d", len(s.queue)))
	s.tryService()
}

func (s *Sim) tryService() {
	if s.busy || len(s.queue) == 0 {
		return
	}
	s.cur = s.queue[0]
	s.queue = s.queue[1:]
	s.busy = true
	s.curRule = Match(s.rules, s.cur)
	if s.curRule >= 0 {
		s.Stats.Hits[s.curRule]++
		s.log("match", s.cur, fmt.Sprintf("命中规则 #%d(%s) -> %s", s.curRule, s.rules[s.curRule].Name, s.rules[s.curRule].Action))
	} else {
		s.Stats.Default++
		s.log("match", s.cur, "未命中任何规则 -> 默认 deny")
	}
	s.schedule(evServiceDone, s.now+s.cfg.ServiceTicks, s.cur)
}

func (s *Sim) onServiceDone() {
	s.busy = false
	if s.curRule >= 0 && s.rules[s.curRule].Action == ActionAllow {
		s.linkQueue = append(s.linkQueue, s.cur)
		s.tryTx()
	} else {
		s.Stats.Dropped++
		s.log("drop", s.cur, "规则动作 deny")
	}
	s.tryService()
}

func (s *Sim) tryTx() {
	if s.linkBusy || len(s.linkQueue) == 0 {
		return
	}
	pkt := s.linkQueue[0]
	s.linkQueue = s.linkQueue[1:]
	s.linkBusy = true
	tx := (pkt.SizeBytes + s.cfg.BytesPerTick - 1) / s.cfg.BytesPerTick
	if tx < 1 {
		tx = 1
	}
	s.log("tx-start", pkt, fmt.Sprintf("串行化 %d tick", tx))
	s.schedule(evTxDone, s.now+tx, pkt)
}

func (s *Sim) onTxDone(pkt Packet) {
	s.linkBusy = false
	s.schedule(evDelivered, s.now+s.cfg.LinkDelay, pkt)
	s.tryTx()
}

func (s *Sim) onDelivered(pkt Packet) {
	s.Stats.Delivered++
	s.log("delivered", pkt, "到达对端（模型内）")
}
