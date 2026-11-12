package netsec

import (
	"errors"
	"sync"
	"testing"
)

// fakeProcess 是可暂停的进程替身：gate 非空时 Signal 会阻塞，
// 直到测试放行，用于确定性地构造完成与控制的竞争。
type fakeProcess struct {
	pid int

	mu      sync.Mutex
	signals []Signal
	resizes [][2]uint16

	gate    chan struct{} // 非空时 Signal 阻塞等待放行
	entered chan struct{} // Signal 进入时关闭（仅一次）

	once sync.Once
}

func newFakeProcess(pid int) *fakeProcess {
	return &fakeProcess{pid: pid}
}

// paused 返回一个 Signal 会阻塞的替身，以及放行函数。
func newPausedProcess(pid int) (*fakeProcess, func()) {
	p := &fakeProcess{
		pid:     pid,
		gate:    make(chan struct{}),
		entered: make(chan struct{}),
	}
	return p, func() { close(p.gate) }
}

func (p *fakeProcess) PID() int { return p.pid }

func (p *fakeProcess) Signal(sig Signal) error {
	if p.entered != nil {
		p.once.Do(func() { close(p.entered) })
	}
	if p.gate != nil {
		<-p.gate
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.signals = append(p.signals, sig)
	return nil
}

func (p *fakeProcess) Resize(rows, cols uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resizes = append(p.resizes, [2]uint16{rows, cols})
	return nil
}

func (p *fakeProcess) gotSignals() []Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Signal(nil), p.signals...)
}

func (p *fakeProcess) gotResizes() [][2]uint16 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][2]uint16(nil), p.resizes...)
}

func fullCaps() Capability {
	return Capability{
		Signals: map[Signal]bool{
			SignalInterrupt: true,
			SignalTerminate: true,
			SignalKill:      true,
			SignalHangup:    true,
			SignalQuit:      true,
		},
		WindowResize: true,
	}
}

// 进程号复用：旧句柄指向的 pid 被新进程复用，路由必须按启动代拒绝旧句柄。
func TestPIDReuseDoesNotLeakControl(t *testing.T) {
	r := NewRouter(1, fullCaps())

	oldProc := newFakeProcess(100)
	oldHandle := r.Start(oldProc)
	if err := r.Complete(oldHandle.Generation()); err != nil {
		t.Fatalf("complete old generation: %v", err)
	}

	newProc := newFakeProcess(100) // 进程号复用
	newHandle := r.Start(newProc)

	if err := oldHandle.Send(SignalTerminate); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale handle send = %v, want ErrStaleGeneration", err)
	}
	if got := newProc.gotSignals(); len(got) != 0 {
		t.Fatalf("reused-pid process received %v from stale handle", got)
	}
	if err := newHandle.Send(SignalTerminate); err != nil {
		t.Fatalf("current handle send: %v", err)
	}
	if got := newProc.gotSignals(); len(got) != 1 || got[0] != SignalTerminate {
		t.Fatalf("current process signals = %v", got)
	}
}

// 退出后迟到信号：Complete 之后同代控制消息一律拒绝且不投递。
func TestLateSignalAfterExitRejected(t *testing.T) {
	r := NewRouter(1, fullCaps())
	proc := newFakeProcess(42)
	h := r.Start(proc)

	if err := r.Complete(h.Generation()); err != nil {
		t.Fatalf("complete: %v", err)
	}
	for _, sig := range []Signal{SignalInterrupt, SignalTerminate, SignalKill} {
		if err := h.Send(sig); !errors.Is(err, ErrProcessExited) {
			t.Fatalf("late send %v = %v, want ErrProcessExited", sig, err)
		}
	}
	if err := h.Resize(24, 80); !errors.Is(err, ErrProcessExited) {
		t.Fatalf("late resize = %v, want ErrProcessExited", err)
	}
	if got := proc.gotSignals(); len(got) != 0 {
		t.Fatalf("exited process received late signals %v", got)
	}
	if got := proc.gotResizes(); len(got) != 0 {
		t.Fatalf("exited process received late resizes %v", got)
	}
}

// 完成与控制竞争的单一顺序：在途投递未完成前 Complete 不返回；
// Complete 返回后的控制消息被拒绝。两个方向都验证。
func TestCompleteAndControlHaveSingleOrder(t *testing.T) {
	t.Run("inflight delivery finishes before complete returns", func(t *testing.T) {
		r := NewRouter(1, fullCaps())
		proc, release := newPausedProcess(7)
		h := r.Start(proc)

		sendDone := make(chan error, 1)
		go func() { sendDone <- h.Send(SignalTerminate) }()
		<-proc.entered // 投递已进入替身并暂停

		completeDone := make(chan error, 1)
		go func() { completeDone <- r.Complete(h.Generation()) }()

		select {
		case err := <-completeDone:
			t.Fatalf("Complete returned %v while a delivery was in flight", err)
		default:
		}

		release()
		if err := <-sendDone; err != nil {
			t.Fatalf("send: %v", err)
		}
		if err := <-completeDone; err != nil {
			t.Fatalf("complete: %v", err)
		}
		if got := proc.gotSignals(); len(got) != 1 || got[0] != SignalTerminate {
			t.Fatalf("signals = %v, want the in-flight terminate delivered exactly once", got)
		}
	})

	t.Run("control after complete is rejected", func(t *testing.T) {
		r := NewRouter(1, fullCaps())
		proc := newFakeProcess(8)
		h := r.Start(proc)

		if err := r.Complete(h.Generation()); err != nil {
			t.Fatalf("complete: %v", err)
		}
		if err := h.Send(SignalTerminate); !errors.Is(err, ErrProcessExited) {
			t.Fatalf("send after complete = %v, want ErrProcessExited", err)
		}
		if got := proc.gotSignals(); len(got) != 0 {
			t.Fatalf("process received signals after completion: %v", got)
		}
	})
}

// 取消不跨会话：两个通道实例各绑定同号进程，取消一会话不影响另一会话；
// 同通道内重启后，旧句柄的取消也不会命中新进程。
func TestCancelDoesNotCrossSessions(t *testing.T) {
	caps := fullCaps()
	r1 := NewRouter(1, caps)
	r2 := NewRouter(2, caps)

	proc1 := newFakeProcess(200)
	proc2 := newFakeProcess(200) // 跨会话进程号相同
	h1 := r1.Start(proc1)
	h2 := r2.Start(proc2)

	if err := h1.Cancel(); err != nil {
		t.Fatalf("cancel session 1: %v", err)
	}
	if got := proc1.gotSignals(); len(got) != 1 || got[0] != SignalKill {
		t.Fatalf("session 1 process signals = %v", got)
	}
	if got := proc2.gotSignals(); len(got) != 0 {
		t.Fatalf("cancel leaked into session 2: %v", got)
	}

	// 会话 1 内进程重启，旧句柄的取消不得命中新进程。
	if err := r1.Complete(h1.Generation()); err != nil {
		t.Fatalf("complete session 1 generation: %v", err)
	}
	proc1b := newFakeProcess(200)
	r1.Start(proc1b)
	if err := h1.Cancel(); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale cancel = %v, want ErrStaleGeneration", err)
	}
	if got := proc1b.gotSignals(); len(got) != 0 {
		t.Fatalf("stale cancel hit restarted process: %v", got)
	}

	// 会话 2 的句柄与会话 1 的路由器互不通用。
	if h2.router != r2 || h1.router != r1 {
		t.Fatal("handles must stay bound to their own channel router")
	}
}

// 只允许声明的信号集合。
func TestOnlyDeclaredSignalsAllowed(t *testing.T) {
	r := NewRouter(1, Capability{
		Signals:      map[Signal]bool{SignalInterrupt: true},
		WindowResize: false,
	})
	proc := newFakeProcess(9)
	h := r.Start(proc)

	if err := h.Send(SignalInterrupt); err != nil {
		t.Fatalf("declared signal: %v", err)
	}
	for _, sig := range []Signal{SignalTerminate, SignalKill, SignalHangup, SignalQuit} {
		if err := h.Send(sig); !errors.Is(err, ErrSignalNotAllowed) {
			t.Fatalf("undeclared signal %v = %v, want ErrSignalNotAllowed", sig, err)
		}
	}
	if err := h.Send(Signal(99)); !errors.Is(err, ErrInvalidSignal) {
		t.Fatalf("unknown signal = %v, want ErrInvalidSignal", err)
	}
	if err := h.Resize(24, 80); !errors.Is(err, ErrWindowNotAllowed) {
		t.Fatalf("resize without capability = %v, want ErrWindowNotAllowed", err)
	}
	if got := proc.gotSignals(); len(got) != 1 || got[0] != SignalInterrupt {
		t.Fatalf("process signals = %v, want only the declared interrupt", got)
	}
	if got := proc.gotResizes(); len(got) != 0 {
		t.Fatalf("process received resizes without capability: %v", got)
	}
}

// 窗口调整同样绑定启动代。
func TestResizeBoundToGeneration(t *testing.T) {
	r := NewRouter(1, fullCaps())
	first := newFakeProcess(10)
	h1 := r.Start(first)
	if err := h1.Resize(24, 80); err != nil {
		t.Fatalf("resize current generation: %v", err)
	}
	if err := r.Complete(h1.Generation()); err != nil {
		t.Fatalf("complete: %v", err)
	}

	second := newFakeProcess(10)
	h2 := r.Start(second)
	if err := h1.Resize(40, 120); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale resize = %v, want ErrStaleGeneration", err)
	}
	if got := second.gotResizes(); len(got) != 0 {
		t.Fatalf("stale resize hit new process: %v", got)
	}
	if err := h2.Resize(40, 120); err != nil {
		t.Fatalf("resize new generation: %v", err)
	}
	if got := second.gotResizes(); len(got) != 1 || got[0] != [2]uint16{40, 120} {
		t.Fatalf("new process resizes = %v", got)
	}
}

// 并发压力：大量并发 Send/Cancel 与 Complete 竞争，
// 每个控制操作要么完整投递要么被拒绝，依赖 -race 检测数据竞争。
func TestConcurrentControlAndComplete(t *testing.T) {
	for i := 0; i < 50; i++ {
		r := NewRouter(1, fullCaps())
		proc := newFakeProcess(i)
		h := r.Start(proc)

		var wg sync.WaitGroup
		for j := 0; j < 8; j++ {
			wg.Add(1)
			go func(sig Signal) {
				defer wg.Done()
				_ = h.Send(sig)
			}([]Signal{SignalInterrupt, SignalTerminate, SignalKill, SignalHangup}[j%4])
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Complete(h.Generation())
		}()
		wg.Wait()

		if err := h.Send(SignalTerminate); !errors.Is(err, ErrProcessExited) {
			t.Fatalf("iteration %d: send after complete = %v", i, err)
		}
	}
}
