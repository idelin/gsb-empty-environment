package fiberdbg

// Breakpoint 描述一个断点：当指定 Fiber 执行完第 Instr 条指令时命中。
// FiberID 为 -1 表示匹配任意 Fiber；Instr 为 -1 表示匹配每条指令。
type Breakpoint struct {
	FiberID int
	Instr   int
}

// Debugger 在 Scheduler 之上提供控制反转的调试接口：
// 单步、按 Fiber 单步、手动推进 Tick、断点与全局时钟暂停。
type Debugger struct {
	sch         *Scheduler
	clockPaused bool
	breakpoints []Breakpoint

	// LastHit 保存最近一次命中的断点（未命中为 nil）。
	LastHit *Breakpoint
}

// NewDebugger 创建一个接管调度器的调试器。
func NewDebugger(sch *Scheduler) *Debugger {
	return &Debugger{sch: sch}
}

// Scheduler 返回被调试的调度器。
func (d *Debugger) Scheduler() *Scheduler { return d.sch }

// AddBreakpoint 注册一个断点。
func (d *Debugger) AddBreakpoint(bp Breakpoint) {
	d.breakpoints = append(d.breakpoints, bp)
}

// PauseClock 暂停全局时钟：此后 Tick 调用成为空操作，
// Continue 也不会再自动推进时钟。
func (d *Debugger) PauseClock() { d.clockPaused = true }

// ResumeClock 恢复全局时钟。
func (d *Debugger) ResumeClock() { d.clockPaused = false }

// ClockPaused 报告全局时钟是否被暂停。
func (d *Debugger) ClockPaused() bool { return d.clockPaused }

// Step 执行调度器的一步（恢复队首 Fiber 的一条指令）。
// 返回值表示这一步之后是否命中了某个断点。
func (d *Debugger) Step() bool {
	d.LastHit = nil
	if !d.sch.Step() {
		return false
	}
	return d.checkBreakpoints()
}

// StepFiber 只让指定 Fiber 执行一条指令。
// 返回值表示是否命中断点；若该 Fiber 不可调度则返回 false 且未推进。
func (d *Debugger) StepFiber(id int) bool {
	d.LastHit = nil
	if !d.sch.StepFiber(id) {
		return false
	}
	return d.checkBreakpoints()
}

func (d *Debugger) checkBreakpoints() bool {
	last := d.sch.LastRun()
	if last == nil {
		return false
	}
	for i := range d.breakpoints {
		bp := d.breakpoints[i]
		if (bp.FiberID < 0 || bp.FiberID == last.ID) &&
			(bp.Instr < 0 || bp.Instr == last.Instructions()) {
			d.LastHit = &d.breakpoints[i]
			return true
		}
	}
	return false
}

// Tick 手动推进全局时钟一格；时钟被暂停时为空操作。
func (d *Debugger) Tick() {
	if d.clockPaused {
		return
	}
	d.sch.Tick()
}

// Continue 持续执行直到命中断点，或系统静止
// （没有可调度的 Fiber，且时钟暂停或没有睡眠者）。
func (d *Debugger) Continue() {
	for {
		if d.Step() {
			return
		}
		if d.sch.HasReady() {
			continue
		}
		if d.clockPaused || !d.sch.HasSleeping() {
			return
		}
		d.sch.Tick()
	}
}
