package redsim

import (
	"math"
	"sort"
)

// Link 是多队列共享链路：固定速率（字节/tick），DRR 按字节公平调度。
type Link struct {
	Rate    int // 字节 / tick
	quantum int
	deficit []int
	charged []bool // 本轮访问是否已加过 quantum
	cur     int    // 当前正在服务的队列（DRR 内可连续发多个包）
}

// NewLink 创建共享链路；quantum 为 DRR 每轮每队列的字节配额（通常取 MTU）。
func NewLink(rate, quantum, nQueues int) *Link {
	return &Link{Rate: rate, quantum: quantum, deficit: make([]int, nQueues), charged: make([]bool, nQueues)}
}

// Arrival 是一个预定到达事件。
type Arrival struct {
	Tick int64
	Pkt  Packet
}

// DepartRecord 记录一次离链事件，供分窗口吞吐统计（不掩盖长期饥饿）。
type DepartRecord struct {
	Tick  int64
	Queue int
	Len   int
}

// Sim 是整数 tick 的离散事件模拟器，不发任何真实网络流量。
type Sim struct {
	Now       int64
	Queues    []*Queue
	Link      *Link
	Drops     []Decision     // 每次丢弃及其依据
	DepartLog []DepartRecord // 每次离链记录

	deriver   Deriver
	arrivals  []Arrival
	nextArr   int
	eventSeq  uint64
	departAt  int64
	departQ   int
	hasDepart bool
}

// NewSim 构建模拟器。arrivals 会被按 (tick, 输入顺序) 稳定排序，
// 因此事件身份（eventSeq）与到达脚本一一对应，重放/分块不改变抽样。
func NewSim(seed uint64, queues []*Queue, link *Link, arrivals []Arrival) *Sim {
	sorted := make([]Arrival, len(arrivals))
	copy(sorted, arrivals)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Tick < sorted[j].Tick })
	return &Sim{Queues: queues, Link: link, deriver: NewDeriver(seed), arrivals: sorted}
}

// Run 推进到 until（含）。可多次调用实现分块运行，结果与一次跑完相同。
func (s *Sim) Run(until int64) {
	for {
		nextArrTick := int64(math.MaxInt64)
		if s.nextArr < len(s.arrivals) {
			nextArrTick = s.arrivals[s.nextArr].Tick
		}
		if s.hasDepart && s.departAt <= until && s.departAt <= nextArrTick {
			s.Now = s.departAt
			s.processDeparture()
			continue
		}
		if nextArrTick <= until {
			s.Now = nextArrTick
			s.processArrival(s.arrivals[s.nextArr])
			s.nextArr++
			continue
		}
		break
	}
	s.Now = until
}

func (s *Sim) processArrival(a Arrival) {
	q := s.Queues[a.Pkt.Queue]
	eventID := s.eventSeq
	s.eventSeq++
	src := s.deriver.For("drop", eventID)
	d := q.RED.Decide(s.Now, eventID, q.ID, q.Len(), a.Pkt.Len, src)
	q.Stats.Arrived++
	q.Stats.BytesArrived += int64(a.Pkt.Len)
	if d.Dropped {
		q.Stats.Dropped++
		q.Stats.BytesDropped += int64(a.Pkt.Len)
		s.Drops = append(s.Drops, d)
		return
	}
	q.Push(a.Pkt)
	if !s.hasDepart {
		s.schedule()
	}
}

func (s *Sim) processDeparture() {
	q := s.Queues[s.departQ]
	p, ok := q.Pop(s.Now)
	if ok {
		q.Stats.Departed++
		q.Stats.BytesOut += int64(p.Len)
		s.DepartLog = append(s.DepartLog, DepartRecord{Tick: s.Now, Queue: q.ID, Len: p.Len})
	}
	s.hasDepart = false
	s.schedule()
}

// schedule 用 DRR 选出下一个发包：每轮访问给队列加 quantum 字节配额，
// 配额够就连发，不够则留存到下一轮；按字节公平，避免长期饥饿。
func (s *Sim) schedule() {
	n := len(s.Queues)
	for {
		allEmpty := true
		for i := 0; i < n; i++ {
			qi := s.Link.cur
			q := s.Queues[qi]
			head, ok := q.Head()
			if !ok {
				s.Link.deficit[qi] = 0
				s.Link.charged[qi] = false
				s.Link.cur = (qi + 1) % n
				continue
			}
			allEmpty = false
			if !s.Link.charged[qi] {
				s.Link.deficit[qi] += s.Link.quantum
				s.Link.charged[qi] = true
			}
			if head.Len <= s.Link.deficit[qi] {
				s.Link.deficit[qi] -= head.Len
				ticks := (head.Len + s.Link.Rate - 1) / s.Link.Rate
				if ticks < 1 {
					ticks = 1
				}
				s.departAt = s.Now + int64(ticks)
				s.departQ = qi
				s.hasDepart = true
				return // 停留在 qi：配额内可继续发下一个包
			}
			// 队首超过剩余配额：结束本轮访问，deficit 留存
			s.Link.charged[qi] = false
			s.Link.cur = (qi + 1) % n
		}
		if allEmpty {
			s.hasDepart = false
			return
		}
	}
}

// CheckByteConservation 复核字节守恒：到达 == 丢弃 + 离链 + 仍在队列。
func (s *Sim) CheckByteConservation() bool {
	var arrived, dropped, out, queued int64
	for _, q := range s.Queues {
		arrived += q.Stats.BytesArrived
		dropped += q.Stats.BytesDropped
		out += q.Stats.BytesOut
		queued += q.QueuedBytes()
	}
	return arrived == dropped+out+queued
}

// WindowedBytes 按窗口统计每队列离链字节数，用于观察长期饥饿。
func (s *Sim) WindowedBytes(window int64) [][]int64 {
	if len(s.DepartLog) == 0 || window <= 0 {
		return nil
	}
	nWin := int(s.DepartLog[len(s.DepartLog)-1].Tick/window) + 1
	out := make([][]int64, len(s.Queues))
	for i := range out {
		out[i] = make([]int64, nWin)
	}
	for _, d := range s.DepartLog {
		out[d.Queue][int(d.Tick/window)] += int64(d.Len)
	}
	return out
}
