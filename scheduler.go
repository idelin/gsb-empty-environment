// Package fiberdbg 实现一个对 Fiber 调度过程进行控制反转的协作式协程调试器。
//
// 调度器不自动运行：所有推进动作（恢复某个 Fiber、推进全局时钟）都由
// 调用方（通常是 Debugger 或测试代码）显式触发，从而实现单步调试、
// 断点、暂停全局时钟以及检查每个协程的挂起原因。
package fiberdbg

import "fmt"

// State 表示一个 Fiber 的生命周期状态。
type State int

const (
	StateNew          State = iota // 已创建，尚未被调度
	StateReady                     // 就绪，等待调度器恢复
	StateRunning                   // 正在执行（仅在被恢复的瞬间）
	StateSleeping                  // 睡眠，等待全局时钟到达唤醒点
	StateWaitChanSend              // 阻塞在 Channel 发送上
	StateWaitChanRecv              // 阻塞在 Channel 接收上
	StateWaitLock                  // 阻塞在互斥锁上
	StateDone                      // 已执行完毕
)

func (s State) String() string {
	switch s {
	case StateNew:
		return "New"
	case StateReady:
		return "Ready"
	case StateRunning:
		return "Running"
	case StateSleeping:
		return "Sleeping"
	case StateWaitChanSend:
		return "WaitChanSend"
	case StateWaitChanRecv:
		return "WaitChanRecv"
	case StateWaitLock:
		return "WaitLock"
	case StateDone:
		return "Done"
	}
	return "Unknown"
}

// opKind 是 Fiber 主动让出执行权时携带的操作类型。
type opKind int

const (
	opYield opKind = iota
	opSleep
	opSend
	opRecv
	opLock
	opUnlock
	opDone
)

// request 是运行中的 Fiber 发回调度器的让权请求。
type request struct {
	fiber *Fiber
	op    opKind
	value interface{}
	n     int
	ch    *Channel
	mu    *Mutex
}

// Transition 记录一次状态迁移，构成“状态转换字典”的基本条目。
type Transition struct {
	Tick    int    // 发生迁移时的全局时钟
	FiberID int    // Fiber 编号
	Name    string // Fiber 名称
	From    State  // 迁移前状态
	To      State  // 迁移后状态
	Reason  string // 人类可读的迁移原因
}

func (t Transition) String() string {
	return fmt.Sprintf("tick=%d fiber=%d(%s) %s -> %s (%s)",
		t.Tick, t.FiberID, t.Name, t.From, t.To, t.Reason)
}

// Fiber 是一个协作式协程。每条“指令”指两次让权之间的执行片段。
type Fiber struct {
	ID   int
	Name string

	sch      *Scheduler
	resume   chan struct{}
	state    State
	reason   string
	instr    int
	wakeTick int
	inbox    interface{}
}

// State 返回 Fiber 当前状态。
func (f *Fiber) State() State { return f.state }

// Reason 返回最近一次状态迁移的原因描述。
func (f *Fiber) Reason() string { return f.reason }

// Instructions 返回 Fiber 已执行的指令数（被恢复的次数）。
func (f *Fiber) Instructions() int { return f.instr }

// call 向调度器发送让权请求并阻塞，直到再次被调度。
func (f *Fiber) call(req request) {
	req.fiber = f
	f.sch.reqCh <- req
	<-f.resume
}

// Yield 主动让出执行权，Fiber 回到就绪队列尾部。
func (f *Fiber) Yield() { f.call(request{op: opYield}) }

// Sleep 让 Fiber 睡眠 n 个全局时钟 Tick。
func (f *Fiber) Sleep(ticks int) { f.call(request{op: opSleep, n: ticks}) }

// Send 向 Channel 发送一个值，必要时阻塞。
func (f *Fiber) Send(ch *Channel, v interface{}) { f.call(request{op: opSend, ch: ch, value: v}) }

// Recv 从 Channel 接收一个值，必要时阻塞。
func (f *Fiber) Recv(ch *Channel) interface{} {
	f.call(request{op: opRecv, ch: ch})
	return f.inbox
}

// Lock 获取互斥锁，必要时阻塞。
func (f *Fiber) Lock(mu *Mutex) { f.call(request{op: opLock, mu: mu}) }

// Unlock 释放互斥锁，并唤醒队首等待者。
func (f *Fiber) Unlock(mu *Mutex) { f.call(request{op: opUnlock, mu: mu}) }

// FiberStatus 是某一时刻单个 Fiber 的快照。
type FiberStatus struct {
	ID     int
	Name   string
	State  State
	Reason string
	Instr  int
}

func (fs FiberStatus) String() string {
	return fmt.Sprintf("fiber=%d(%s) state=%s instr=%d reason=%q",
		fs.ID, fs.Name, fs.State, fs.Instr, fs.Reason)
}

// Scheduler 是协作式 Fiber 调度器。它本身没有任何后台 goroutine：
// 只有调用 Step / StepFiber / Tick / Run 时调度才会发生。
type Scheduler struct {
	fibers      []*Fiber
	ready       []*Fiber
	sleeping    []*Fiber
	reqCh       chan request
	clock       int
	last        *Fiber
	transitions []Transition
}

// NewScheduler 创建一个空调度器。
func NewScheduler() *Scheduler {
	return &Scheduler{reqCh: make(chan request)}
}

// Spawn 创建一个新 Fiber 并放入就绪队列。
func (s *Scheduler) Spawn(name string, fn func(*Fiber)) *Fiber {
	f := &Fiber{
		ID:     len(s.fibers),
		Name:   name,
		sch:    s,
		resume: make(chan struct{}),
	}
	s.fibers = append(s.fibers, f)
	s.setState(f, StateReady, "spawned")
	s.ready = append(s.ready, f)
	go func() {
		<-f.resume // 等待第一次被调度
		fn(f)
		f.sch.reqCh <- request{fiber: f, op: opDone}
	}()
	return f
}

// Clock 返回当前全局时钟。
func (s *Scheduler) Clock() int { return s.clock }

// LastRun 返回最近一次被恢复的 Fiber（可能为 nil）。
func (s *Scheduler) LastRun() *Fiber { return s.last }

// HasReady 报告就绪队列是否非空。
func (s *Scheduler) HasReady() bool { return len(s.ready) > 0 }

// HasSleeping 报告是否存在睡眠中的 Fiber。
func (s *Scheduler) HasSleeping() bool { return len(s.sleeping) > 0 }

func (s *Scheduler) setState(f *Fiber, to State, reason string) {
	s.transitions = append(s.transitions, Transition{
		Tick:    s.clock,
		FiberID: f.ID,
		Name:    f.Name,
		From:    f.state,
		To:      to,
		Reason:  reason,
	})
	f.state = to
	f.reason = reason
}

func (s *Scheduler) wake(f *Fiber, reason string) {
	s.setState(f, StateReady, reason)
	s.ready = append(s.ready, f)
}

// Step 恢复就绪队列队首的 Fiber 执行一条指令。
// 返回 false 表示就绪队列为空，调度器没有推进。
func (s *Scheduler) Step() bool {
	if len(s.ready) == 0 {
		return false
	}
	f := s.ready[0]
	s.ready = s.ready[1:]
	s.runOne(f)
	return true
}

// StepFiber 只恢复指定 Fiber 执行一条指令（单协程单步）。
// 若该 Fiber 不在就绪队列中，返回 false。
func (s *Scheduler) StepFiber(id int) bool {
	for i, f := range s.ready {
		if f.ID == id {
			s.ready = append(s.ready[:i], s.ready[i+1:]...)
			s.runOne(f)
			return true
		}
	}
	return false
}

// runOne 恢复一个 Fiber，直到它再次让权，然后处理其请求。
func (s *Scheduler) runOne(f *Fiber) {
	s.setState(f, StateRunning, "resumed")
	f.instr++
	s.last = f
	f.resume <- struct{}{}
	req := <-s.reqCh
	s.handle(req)
}

func (s *Scheduler) handle(req request) {
	f := req.fiber
	switch req.op {
	case opYield:
		s.setState(f, StateReady, "voluntary yield")
		s.ready = append(s.ready, f)
	case opSleep:
		f.wakeTick = s.clock + req.n
		s.sleeping = append(s.sleeping, f)
		s.setState(f, StateSleeping,
			fmt.Sprintf("sleeping %d tick(s), wake at tick %d", req.n, f.wakeTick))
	case opSend:
		s.handleSend(f, req.ch, req.value)
	case opRecv:
		s.handleRecv(f, req.ch)
	case opLock:
		s.handleLock(f, req.mu)
	case opUnlock:
		s.handleUnlock(f, req.mu)
	case opDone:
		s.setState(f, StateDone, "finished")
	}
}

// Tick 将全局时钟推进一格，并唤醒所有到期的睡眠 Fiber。
func (s *Scheduler) Tick() {
	s.clock++
	rest := s.sleeping[:0]
	for _, f := range s.sleeping {
		if f.wakeTick <= s.clock {
			s.wake(f, fmt.Sprintf("woke up at tick %d", s.clock))
		} else {
			rest = append(rest, f)
		}
	}
	s.sleeping = rest
}

// Run 自由运行：持续调度直到所有 Fiber 结束，或只剩无法唤醒的
// 阻塞 Fiber（死锁/假死）。运行模式下时钟会在无就绪 Fiber 时自动推进。
func (s *Scheduler) Run() {
	for {
		if s.Step() {
			continue
		}
		if s.HasSleeping() {
			s.Tick()
			continue
		}
		return
	}
}

// Transitions 返回指定 Fiber 的完整状态迁移历史。
func (s *Scheduler) Transitions(fiberID int) []Transition {
	var out []Transition
	for _, t := range s.transitions {
		if t.FiberID == fiberID {
			out = append(out, t)
		}
	}
	return out
}

// TransitionMap 返回“状态转换字典”：Fiber ID -> 迁移历史。
func (s *Scheduler) TransitionMap() map[int][]Transition {
	m := make(map[int][]Transition)
	for _, t := range s.transitions {
		m[t.FiberID] = append(m[t.FiberID], t)
	}
	return m
}

// Snapshot 返回所有 Fiber 的当前状态快照。
func (s *Scheduler) Snapshot() []FiberStatus {
	out := make([]FiberStatus, 0, len(s.fibers))
	for _, f := range s.fibers {
		out = append(out, FiberStatus{
			ID:     f.ID,
			Name:   f.Name,
			State:  f.state,
			Reason: f.reason,
			Instr:  f.instr,
		})
	}
	return out
}

// Stuck 返回所有既未结束、又不可能再被调度的 Fiber：
// 它们既不就绪也不在睡眠，只能永远等待锁或 Channel（假死）。
func (s *Scheduler) Stuck() []FiberStatus {
	var out []FiberStatus
	for _, fs := range s.Snapshot() {
		switch fs.State {
		case StateWaitChanSend, StateWaitChanRecv, StateWaitLock:
			out = append(out, fs)
		}
	}
	return out
}
