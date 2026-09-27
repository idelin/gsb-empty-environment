package main

import "fmt"

// Packet 是仿真中的一个包，只存在于内存事件模型中，不发真实网络流量。
type Packet struct {
	FlowID uint64
	Seq    int
}

// Flow 以确定性速率产生包：每 tick 累加 Rate，整数部分即本 tick 发包数。
type Flow struct {
	ID   uint64
	Rate float64 // 包/tick
	acc  float64
	seq  int
}

// link 是路径的发送侧：FIFO 队列 + 每 tick 容量（由权重导出）。
type link struct {
	pathID   string
	capacity int // 包/tick
	queue    []Packet
	sent     int
	backlog  int // 历史最大积压
}

// Sim 是离散事件仿真器：时间以整数 tick 推进。
// 每个 tick 分两阶段：1) 流产生包并按 Assigner 入队；2) 各链路按容量出队发送。
type Sim struct {
	assigner *Assigner
	flows    []*Flow
	links    map[string]*link
	order    []string // 固定遍历顺序，保证确定性
	tick     int
	sentPath map[uint64]string // 记录每个包实际发送路径（按流校验同路径）
}

// NewSim 用路径权重推导链路容量：capacity = round(weight * packetsPerWeight)。
func NewSim(a *Assigner, paths []Path, flows []Flow, packetsPerWeight float64) *Sim {
	s := &Sim{
		assigner: a,
		links:    make(map[string]*link),
		sentPath: make(map[uint64]string),
	}
	for _, p := range paths {
		if p.Weight <= 0 {
			continue
		}
		cap := int(p.Weight*packetsPerWeight + 0.5)
		s.links[p.ID] = &link{pathID: p.ID, capacity: cap}
		s.order = append(s.order, p.ID)
	}
	for i := range flows {
		f := flows[i]
		s.flows = append(s.flows, &f)
	}
	return s
}

// Step 推进一个 tick，返回本 tick 各路径发送量。
func (s *Sim) Step() map[string]int {
	// 阶段1：流产生包，按稳定分配入队（同流同路径）
	for _, f := range s.flows {
		f.acc += f.Rate
		n := int(f.acc)
		f.acc -= float64(n)
		pathID := s.assigner.Assign(f.ID)
		lk := s.links[pathID]
		if lk == nil {
			continue // 无可用路径，包丢弃（demo 中不发生）
		}
		for ; n > 0; n-- {
			lk.queue = append(lk.queue, Packet{FlowID: f.ID, Seq: f.seq})
			f.seq++
		}
	}
	// 阶段2：链路按容量发送
	sent := make(map[string]int, len(s.links))
	for _, id := range s.order {
		lk := s.links[id]
		n := lk.capacity
		if n > len(lk.queue) {
			n = len(lk.queue)
		}
		for _, pkt := range lk.queue[:n] {
			if prev, ok := s.sentPath[pkt.FlowID]; ok && prev != id {
				panic(fmt.Sprintf("flow %d 的包被换路径: %s -> %s", pkt.FlowID, prev, id))
			}
			s.sentPath[pkt.FlowID] = id
		}
		lk.queue = lk.queue[n:]
		if len(lk.queue) > lk.backlog {
			lk.backlog = len(lk.queue)
		}
		lk.sent += n
		sent[id] = n
	}
	s.tick++
	return sent
}

// Run 推进 ticks 个 tick。
func (s *Sim) Run(ticks int) {
	for i := 0; i < ticks; i++ {
		s.Step()
	}
}

// Stats 汇总每条路径的负载。
type PathStat struct {
	PathID   string
	Capacity int
	Sent     int
	Backlog  int
	Share    float64 // 发送量占比
}

func (s *Sim) Stats() []PathStat {
	total := 0
	for _, lk := range s.links {
		total += lk.sent
	}
	out := make([]PathStat, 0, len(s.order))
	for _, id := range s.order {
		lk := s.links[id]
		st := PathStat{PathID: id, Capacity: lk.capacity, Sent: lk.sent, Backlog: lk.backlog}
		if total > 0 {
			st.Share = float64(lk.sent) / float64(total)
		}
		out = append(out, st)
	}
	return out
}
