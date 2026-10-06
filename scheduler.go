package sim

import (
	"container/heap"
	"fmt"
)

// Job 是租户提交的作业：占用 Cost 额度，运行 Duration 时间，
// 运行期间持有一个全局槽位和一个租户槽位，完成时才归还。
type Job struct {
	ID       int
	Cost     float64
	Duration float64
	ArriveAt float64
	StartAt  float64
	FinishAt float64
	Started  bool
	Done     bool
}

// Tenant 是调度对象。额度 Credit 只在队列非空时按权重逐轮累加；
// 退出（Exit）时额度清零、待运行作业被丢弃，运行中的作业继续到完成。
type Tenant struct {
	Name        string
	Weight      float64
	MaxConc     int
	Credit      float64
	Active      bool
	Queue       []*Job
	Running     int
	Served      float64 // 已完成作业的成本总和
	PeakRunning int
}

type eventKind int

const (
	evArrival eventKind = iota
	evCompletion
	evTick
	evExit
	evJoin
)

func kindName(k eventKind) string {
	switch k {
	case evArrival:
		return "arrival"
	case evCompletion:
		return "completion"
	case evTick:
		return "tick"
	case evExit:
		return "exit"
	case evJoin:
		return "join"
	}
	return "?"
}

type event struct {
	time   float64
	seq    int
	kind   eventKind
	tenant *Tenant
	job    *Job
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Sim 是离散事件模拟器。每个 Tick 为一“轮”：非空租户累加 Weight 额度，
// 随后按轮转顺序尝试启动作业（额度 >= 作业成本 且 槽位充足）。
type Sim struct {
	Now       float64
	Horizon   float64
	Tick      float64
	GlobalMax int

	globalRun int
	rotation  []*Tenant
	tenants   map[string]*Tenant
	events    eventHeap
	seq       int
	jobSeq    int
	rr        int

	PeakGlobal int
	Hook       func(s *Sim, kind string, t *Tenant)
	Violations []string
}

func New(horizon, tick float64, globalMax int) *Sim {
	s := &Sim{Horizon: horizon, Tick: tick, GlobalMax: globalMax, tenants: map[string]*Tenant{}}
	s.push(0, evTick, nil, nil)
	return s
}

func (s *Sim) push(at float64, kind eventKind, t *Tenant, j *Job) {
	s.seq++
	heap.Push(&s.events, event{time: at, seq: s.seq, kind: kind, tenant: t, job: j})
}

func (s *Sim) AddTenant(name string, weight float64, maxConc int, joinAt float64) *Tenant {
	t := &Tenant{Name: name, Weight: weight, MaxConc: maxConc}
	s.tenants[name] = t
	s.push(joinAt, evJoin, t, nil)
	return t
}

func (s *Sim) ExitTenant(name string, at float64) {
	s.push(at, evExit, s.tenants[name], nil)
}

// RejoinTenant 让已退出的租户重新加入轮转，额度从零开始。
func (s *Sim) RejoinTenant(name string, at float64) {
	s.push(at, evJoin, s.tenants[name], nil)
}

func (s *Sim) Submit(tenant string, cost, duration, at float64) {
	s.jobSeq++
	j := &Job{ID: s.jobSeq, Cost: cost, Duration: duration, ArriveAt: at}
	s.push(at, evArrival, s.tenants[tenant], j)
}

func (s *Sim) Tenant(name string) *Tenant { return s.tenants[name] }

func (s *Sim) InRotation(t *Tenant) bool {
	for _, r := range s.rotation {
		if r == t {
			return true
		}
	}
	return false
}

func (s *Sim) Run() {
	for len(s.events) > 0 {
		e := heap.Pop(&s.events).(event)
		if e.time > s.Horizon {
			s.Now = s.Horizon
			break
		}
		s.Now = e.time
		switch e.kind {
		case evArrival:
			e.tenant.Queue = append(e.tenant.Queue, e.job)
		case evCompletion:
			e.tenant.Running--
			s.globalRun--
			e.tenant.Served += e.job.Cost
			e.job.Done = true
		case evTick:
			for _, t := range s.rotation {
				if len(t.Queue) > 0 { // 空租户不积攒额度
					t.Credit += t.Weight
				}
			}
			if s.Now+s.Tick <= s.Horizon {
				s.push(s.Now+s.Tick, evTick, nil, nil)
			}
		case evExit:
			s.removeFromRotation(e.tenant)
			e.tenant.Active = false
			e.tenant.Credit = 0 // 退出清零
			e.tenant.Queue = nil
		case evJoin:
			e.tenant.Active = true
			e.tenant.Credit = 0
			s.rotation = append(s.rotation, e.tenant)
		}
		s.dispatch()
		s.checkInvariants()
		if s.Hook != nil {
			s.Hook(s, kindName(e.kind), e.tenant)
		}
	}
}

func (s *Sim) removeFromRotation(t *Tenant) {
	for i, r := range s.rotation {
		if r == t {
			s.rotation = append(s.rotation[:i], s.rotation[i+1:]...)
			if i < s.rr {
				s.rr--
			}
			if len(s.rotation) > 0 && s.rr >= len(s.rotation) {
				s.rr = 0
			}
			return
		}
	}
}

func (s *Sim) dispatch() {
	n := len(s.rotation)
	if n == 0 {
		return
	}
	for i := 0; i < n; i++ {
		idx := (s.rr + i) % n
		t := s.rotation[idx]
		started := false
		for len(t.Queue) > 0 && t.Running < t.MaxConc && s.globalRun < s.GlobalMax {
			j := t.Queue[0]
			if j.Cost > t.Credit {
				break
			}
			t.Credit -= j.Cost
			t.Queue = t.Queue[1:]
			t.Running++
			s.globalRun++
			j.Started = true
			j.StartAt = s.Now
			j.FinishAt = s.Now + j.Duration
			s.push(j.FinishAt, evCompletion, t, j)
			started = true
		}
		if t.Running > t.PeakRunning {
			t.PeakRunning = t.Running
		}
		if started {
			s.rr = (idx + 1) % n
		}
	}
	if s.globalRun > s.PeakGlobal {
		s.PeakGlobal = s.globalRun
	}
}

func (s *Sim) checkInvariants() {
	if s.globalRun < 0 || s.globalRun > s.GlobalMax {
		s.Violations = append(s.Violations,
			fmt.Sprintf("t=%.1f global running %d out of [0,%d]", s.Now, s.globalRun, s.GlobalMax))
	}
	for _, t := range s.tenants {
		if t.Running < 0 || t.Running > t.MaxConc {
			s.Violations = append(s.Violations,
				fmt.Sprintf("t=%.1f tenant %s running %d out of [0,%d]", s.Now, t.Name, t.Running, t.MaxConc))
		}
		if t.Credit < -1e-9 {
			s.Violations = append(s.Violations,
				fmt.Sprintf("t=%.1f tenant %s credit %f < 0", s.Now, t.Name, t.Credit))
		}
	}
}
