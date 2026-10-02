package fiberdbg

import (
	"strings"
	"testing"
)

// 睡眠与手动 Tick 唤醒。
func TestSleepAndManualTick(t *testing.T) {
	sch := NewScheduler()
	f := sch.Spawn("sleeper", func(f *Fiber) {
		f.Sleep(3)
	})
	dbg := NewDebugger(sch)

	if !dbg.Step() || f.State() != StateSleeping {
		// Step 返回的是断点命中，这里只关心状态
	}
	if f.State() != StateSleeping {
		t.Fatalf("expected Sleeping, got %s", f.State())
	}
	if sch.HasReady() {
		t.Fatal("ready queue should be empty while the only fiber sleeps")
	}

	dbg.Tick()
	dbg.Tick()
	if sch.HasReady() {
		t.Fatal("fiber should still be sleeping after 2 ticks")
	}
	dbg.Tick()
	if !sch.HasReady() {
		t.Fatal("fiber should be awake after 3 ticks")
	}
	if sch.Clock() != 3 {
		t.Fatalf("clock = %d, want 3", sch.Clock())
	}

	sch.Step()
	if f.State() != StateDone {
		t.Fatalf("expected Done, got %s", f.State())
	}
}

// 暂停全局时钟后 Tick 不生效。
func TestPauseClock(t *testing.T) {
	sch := NewScheduler()
	f := sch.Spawn("sleeper", func(f *Fiber) { f.Sleep(1) })
	dbg := NewDebugger(sch)
	sch.Step()

	dbg.PauseClock()
	for i := 0; i < 5; i++ {
		dbg.Tick()
	}
	if sch.Clock() != 0 {
		t.Fatalf("clock advanced while paused: %d", sch.Clock())
	}
	if f.State() != StateSleeping {
		t.Fatalf("fiber should remain Sleeping, got %s", f.State())
	}

	dbg.ResumeClock()
	dbg.Tick()
	if f.State() != StateReady {
		t.Fatalf("fiber should be Ready after resume+tick, got %s", f.State())
	}
}

// Channel 阻塞接收后被发送方唤醒。
func TestChannelHandshake(t *testing.T) {
	sch := NewScheduler()
	ch := NewChannel("data", 0)
	got := make(chan interface{}, 1)

	sch.Spawn("receiver", func(f *Fiber) {
		got <- f.Recv(ch)
	})
	sch.Spawn("sender", func(f *Fiber) {
		f.Send(ch, 42)
	})

	sch.Run()

	if v := <-got; v != 42 {
		t.Fatalf("received %v, want 42", v)
	}
	for _, fs := range sch.Snapshot() {
		if fs.State != StateDone {
			t.Fatalf("fiber %s not done: %s", fs.Name, fs.State)
		}
	}
}

// 互斥锁竞争：第二个 Fiber 等待，解锁后被唤醒。
func TestMutexContention(t *testing.T) {
	sch := NewScheduler()
	mu := NewMutex("resource")
	order := make(chan string, 2)

	sch.Spawn("first", func(f *Fiber) {
		f.Lock(mu)
		f.Yield() // 让 second 有机会抢锁并阻塞
		order <- "first"
		f.Unlock(mu)
	})
	second := sch.Spawn("second", func(f *Fiber) {
		f.Lock(mu)
		order <- "second"
		f.Unlock(mu)
	})

	sch.Step() // first: Lock 成功
	sch.Step() // first: Yield
	sch.Step() // second: Lock -> WaitLock
	if second.State() != StateWaitLock {
		t.Fatalf("second should wait for lock, got %s", second.State())
	}
	if !strings.Contains(second.Reason(), "resource") {
		t.Fatalf("reason should mention lock name: %q", second.Reason())
	}

	sch.Run()
	if <-order != "first" || <-order != "second" {
		t.Fatal("lock handoff order violated")
	}
}

// 单协程单步：只推进指定 Fiber 的一条指令。
func TestStepSingleFiber(t *testing.T) {
	sch := NewScheduler()
	a := sch.Spawn("a", func(f *Fiber) { f.Yield(); f.Yield() })
	b := sch.Spawn("b", func(f *Fiber) { f.Yield(); f.Yield() })
	dbg := NewDebugger(sch)

	dbg.StepFiber(b.ID)
	if b.Instructions() != 1 || a.Instructions() != 0 {
		t.Fatalf("a=%d b=%d, want a=0 b=1", a.Instructions(), b.Instructions())
	}
	dbg.StepFiber(b.ID)
	if b.Instructions() != 2 || a.Instructions() != 0 {
		t.Fatalf("a=%d b=%d, want a=0 b=2", a.Instructions(), b.Instructions())
	}
	if dbg.StepFiber(999) {
		t.Fatal("stepping unknown fiber should report no progress")
	}
}

// 断点：在指定 Fiber 的第 N 条指令处停下。
func TestBreakpoint(t *testing.T) {
	sch := NewScheduler()
	worker := sch.Spawn("worker", func(f *Fiber) {
		f.Yield()
		f.Yield()
		f.Yield()
	})
	sch.Spawn("other", func(f *Fiber) { f.Yield() })

	dbg := NewDebugger(sch)
	dbg.AddBreakpoint(Breakpoint{FiberID: worker.ID, Instr: 3})
	dbg.Continue()

	if dbg.LastHit == nil {
		t.Fatal("breakpoint was not hit")
	}
	if worker.Instructions() != 3 {
		t.Fatalf("stopped at instr %d, want 3", worker.Instructions())
	}
	if worker.State() != StateReady {
		t.Fatalf("worker should be Ready (yielded), got %s", worker.State())
	}
}

// 验收场景：系统置于单步调试模式，逐个 Tick 手动推动调度器，
// 通过状态转换字典定位某协程在特定时隙因等待未初始化资源而假死。
func TestStuckOnUninitializedResource(t *testing.T) {
	sch := NewScheduler()
	config := NewChannel("config", 0) // 资源：配置下发通道

	// initializer 本应在 tick 2 前完成初始化并下发配置，
	// 但当前实现存在缺陷：它直接返回，资源永远未被初始化。
	sch.Spawn("initializer", func(f *Fiber) {
		f.Sleep(2)
		// BUG: 缺少 f.Send(config, loadConfig())
	})

	// worker 启动后等待配置，随后才进入主循环。
	worker := sch.Spawn("worker", func(f *Fiber) {
		f.Recv(config) // 等待资源初始化
		for {
			f.Yield()
		}
	})

	dbg := NewDebugger(sch)

	// 单步调试模式：逐指令、逐 Tick 手动推动。
	dbg.Step() // initializer: 开始睡眠
	dbg.Step() // worker: 发出 Recv -> 阻塞
	dbg.Tick() // tick 1
	dbg.Tick() // tick 2: initializer 醒来
	dbg.Step() // initializer: 直接结束（未初始化资源）

	if got := sch.Clock(); got != 2 {
		t.Fatalf("clock = %d, want 2", got)
	}

	// 检查状态转换字典：worker 在 tick 0 进入 WaitChanRecv 后再未迁移。
	history := sch.TransitionMap()[worker.ID]
	if len(history) == 0 {
		t.Fatal("no transitions recorded for worker")
	}
	last := history[len(history)-1]
	if last.To != StateWaitChanRecv {
		t.Fatalf("worker last transition -> %s, want WaitChanRecv", last.To)
	}
	if last.Tick != 0 {
		t.Fatalf("worker got stuck at tick %d, want tick 0", last.Tick)
	}
	if !strings.Contains(last.Reason, "config") {
		t.Fatalf("reason should identify the uninitialized resource: %q", last.Reason)
	}

	// 系统层面：initializer 正常结束，worker 假死，调度器无事可做。
	if sch.HasReady() || sch.HasSleeping() {
		t.Fatal("scheduler should be quiescent")
	}
	stuck := sch.Stuck()
	if len(stuck) != 1 || stuck[0].ID != worker.ID {
		t.Fatalf("stuck = %v, want only worker", stuck)
	}
	if stuck[0].State != StateWaitChanRecv {
		t.Fatalf("stuck state = %s, want WaitChanRecv", stuck[0].State)
	}

	// 无论再推进多少 Tick，假死的 worker 都不会恢复。
	for i := 0; i < 10; i++ {
		dbg.Tick()
	}
	if worker.State() != StateWaitChanRecv {
		t.Fatalf("worker recovered unexpectedly: %s", worker.State())
	}
}

// Run 在死锁时应当返回而不是空转。
func TestRunTerminatesOnDeadlock(t *testing.T) {
	sch := NewScheduler()
	ch := NewChannel("never", 0)
	sch.Spawn("stuck", func(f *Fiber) { f.Recv(ch) })

	sch.Run() // 必须返回

	stuck := sch.Stuck()
	if len(stuck) != 1 {
		t.Fatalf("stuck = %v, want 1 fiber", stuck)
	}
}
