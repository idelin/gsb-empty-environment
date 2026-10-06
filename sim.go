package main

import "fmt"

// Job 是租户提交的作业。Cost 同时表示：
//  1. 调度时从租户额度中扣除的额度单位；
//  2. 占用全局/租户并发槽位的 tick 数（完成才归还槽位）。
type Job struct {
	ID     int
	Tenant int
	Cost   int
	Arrive int
	Start  int
	Finish int
	Tag    string
}

type runningJob struct {
	job       *Job
	remaining int
}

// Tenant 调度状态。
type Tenant struct {
	Name    string
	Weight  int // 每轮（tick）累加的额度
	Credit  int // 当前额度；空租户不积攒（被清零）
	Cap     int // 租户并发上限
	Active  bool
	Queue   []*Job
	Running []*runningJob

	Served   int // 已完成作业的成本总和（服务量）
	Finished int // 已完成作业数
	Dropped  int // 退出时被丢弃的排队作业数
}

func (t *Tenant) backlogged() bool { return t.Active && len(t.Queue) > 0 }

// Sim 离散时间模拟器。
type Sim struct {
	tick      int
	tenants   []*Tenant
	globalCap int
	running   int // 全局已占用槽位
	rr        int // 轮转起点

	arrivals map[int][]*Job // tick -> 到达作业
	exits    map[int][]int  // tick -> 退出租户 id
	joins    map[int][]int  // tick -> 重新加入租户 id

	completed []*Job
	jobSeq    int

	// 不变量监控
	maxRunning    int
	capViolations int
}

func NewSim(globalCap int) *Sim {
	return &Sim{
		globalCap: globalCap,
		arrivals:  make(map[int][]*Job),
		exits:     make(map[int][]int),
		joins:     make(map[int][]int),
	}
}

func (s *Sim) AddTenant(name string, weight, cap int) *Tenant {
	t := &Tenant{Name: name, Weight: weight, Cap: cap, Active: true}
	s.tenants = append(s.tenants, t)
	return t
}

func (s *Sim) Submit(at, tenant, cost int, tag string) {
	s.jobSeq++
	s.arrivals[at] = append(s.arrivals[at], &Job{
		ID: s.jobSeq, Tenant: tenant, Cost: cost, Arrive: at, Tag: tag,
	})
}

func (s *Sim) Exit(at, tenant int)  { s.exits[at] = append(s.exits[at], tenant) }
func (s *Sim) Rejoin(at, tenant int) { s.joins[at] = append(s.joins[at], tenant) }

// dispatchRound 执行一轮轮转：有积压的租户累加权重额度，
// 空租户额度清零；在额度与双级并发上限允许时派发队首作业。
func (s *Sim) dispatchRound() {
	n := len(s.tenants)
	for i := 0; i < n; i++ {
		idx := (s.rr + i) % n
		t := s.tenants[idx]
		if !t.backlogged() {
			t.Credit = 0 // 空租户不积攒额度
			continue
		}
		t.Credit += t.Weight
		for len(t.Queue) > 0 &&
			t.Queue[0].Cost <= t.Credit &&
			len(t.Running) < t.Cap &&
			s.running < s.globalCap {
			j := t.Queue[0]
			t.Queue = t.Queue[1:]
			t.Credit -= j.Cost
			j.Start = s.tick
			t.Running = append(t.Running, &runningJob{job: j, remaining: j.Cost})
			s.running++
		}
		if len(t.Queue) == 0 {
			t.Credit = 0 // 队列排空即清零，空租户不持有额度
		}
	}
	s.rr = (s.rr + 1) % n
}

// advance 推进一个 tick：完成作业归还槽位。
func (s *Sim) advance() {
	for _, t := range s.tenants {
		kept := t.Running[:0]
		for _, rj := range t.Running {
			rj.remaining--
			if rj.remaining == 0 {
				rj.job.Finish = s.tick
				t.Served += rj.job.Cost
				t.Finished++
				s.running--
				s.completed = append(s.completed, rj.job)
			} else {
				kept = append(kept, rj)
			}
		}
		t.Running = kept
	}
}

func (s *Sim) applyEvents() {
	for _, id := range s.joins[s.tick] {
		t := s.tenants[id]
		t.Active = true
		t.Credit = 0 // 重新加入从零开始
	}
	for _, id := range s.exits[s.tick] {
		t := s.tenants[id]
		t.Active = false
		t.Credit = 0 // 退出清零
		t.Dropped += len(t.Queue)
		t.Queue = nil // 排队作业被丢弃；运行中作业继续到完成
	}
	for _, j := range s.arrivals[s.tick] {
		t := s.tenants[j.Tenant]
		if t.Active {
			t.Queue = append(t.Queue, j)
		}
	}
}

func (s *Sim) Step() {
	s.applyEvents()
	s.dispatchRound()
	s.checkInvariants()
	s.advance()
	s.tick++
}

func (s *Sim) checkInvariants() {
	if s.running > s.globalCap {
		s.capViolations++
	}
	if s.running > s.maxRunning {
		s.maxRunning = s.running
	}
	for _, t := range s.tenants {
		if len(t.Running) > t.Cap {
			s.capViolations++
		}
		if !t.backlogged() && t.Credit != 0 {
			s.capViolations++ // 空租户额度必须为零
		}
	}
}

func (s *Sim) Run(ticks int) {
	for i := 0; i < ticks; i++ {
		s.Step()
	}
}

func (s *Sim) FindJob(tag string) *Job {
	for _, j := range s.completed {
		if j.Tag == tag {
			return j
		}
	}
	return nil
}

func (s *Sim) Report() {
	fmt.Printf("  ticks=%d 全局峰值并发=%d/%d 不变量违例=%d\n",
		s.tick, s.maxRunning, s.globalCap, s.capViolations)
	for _, t := range s.tenants {
		fmt.Printf("  %-6s 权重=%d 服务量=%d 完成=%d 丢弃=%d 在跑=%d 在队=%d 额度=%d\n",
			t.Name, t.Weight, t.Served, t.Finished, t.Dropped,
			len(t.Running), len(t.Queue), t.Credit)
	}
}
