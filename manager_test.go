package quota

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const testTimeout = 2 * time.Second

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(testTimeout):
		t.Fatalf("timeout waiting for %s (possible deadlock)", what)
	}
}

// result 收集一个任务的最终结果，panic 也会被捕获（Manager 在归还额度后重新抛出）。
type result struct {
	err      error
	panicked any
	finished bool
}

func runAsync(m *Manager, ctx context.Context, phases []Phase, fn TaskFunc) <-chan result {
	out := make(chan result, 1)
	go func() {
		var r result
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					r.panicked = rec
				}
			}()
			r.err = m.Run(ctx, phases, fn)
		}()
		r.finished = true
		out <- r
	}()
	return out
}

// TestSwapPhasesMakesProgress 验证核心无死锁性质：
// A 持 CPU 想换 IO，B 持 IO 想换 CPU —— 经典的“互相等待”局面。
// 因为切换时先释放旧额度再申请新额度（不占有旧额度等待），互换必须成功完成。
func TestSwapPhasesMakesProgress(t *testing.T) {
	m := NewManager(map[Phase]int{CPU: 1, IO: 1})

	// 各阶段进入时关闭的信号。
	aCpu := make(chan struct{})
	aIo := make(chan struct{})
	bIo := make(chan struct{})
	bCpu2 := make(chan struct{})

	// 各阶段放行开关。
	var stepMu sync.Mutex
	aCpuStep := 0
	bIoStep := 0
	allowA2 := make(chan struct{})
	allowA3 := make(chan struct{})
	allowB2 := make(chan struct{})
	allowB3 := make(chan struct{})

	aPhases := []Phase{CPU, IO, CPU}
	bPhases := []Phase{IO, CPU, IO}

	aFn := func(p Phase) error {
		switch p {
		case CPU:
			stepMu.Lock()
			if aCpuStep == 0 {
				aCpuStep++
				close(aCpu)
			}
			stepMu.Unlock()
			if !isClosed(aIo) { // 第一次进入 CPU：等放行后换 IO
				<-allowA2
			} else { // 第二次回到 CPU
				<-allowA3
			}
		case IO:
			close(aIo)
			<-allowA3
		}
		return nil
	}
	bFn := func(p Phase) error {
		switch p {
		case IO:
			stepMu.Lock()
			if bIoStep == 0 {
				bIoStep++
				close(bIo)
			}
			stepMu.Unlock()
			if !isClosed(bCpu2) { // 第一次进入 IO：等放行后换 CPU
				<-allowB2
			} else { // 第二次回到 IO
				<-allowB3
			}
		case CPU:
			close(bCpu2)
			<-allowB3
		}
		return nil
	}

	ctx := context.Background()
	ar := runAsync(m, ctx, aPhases, aFn)
	br := runAsync(m, ctx, bPhases, bFn)

	// 1) 建立僵持局面：A 持 CPU，B 持 IO，各占 1 份。
	waitSignal(t, aCpu, "task A holding CPU")
	waitSignal(t, bIo, "task B holding IO")
	if got := m.Usage(CPU); got != 1 {
		t.Fatalf("CPU usage = %d, want 1", got)
	}
	if got := m.Usage(IO); got != 1 {
		t.Fatalf("IO usage = %d, want 1", got)
	}

	// 2) 同时放行互换：A CPU->IO，B IO->CPU。
	//    若实现“占有旧额度等新额度”，这里会永久死锁。
	close(allowA2)
	close(allowB2)
	waitSignal(t, aIo, "task A switched to IO")
	waitSignal(t, bCpu2, "task B switched to CPU")
	if got := m.Usage(CPU); got != 1 {
		t.Fatalf("after swap CPU usage = %d, want 1 (held by B)", got)
	}
	if got := m.Usage(IO); got != 1 {
		t.Fatalf("after swap IO usage = %d, want 1 (held by A)", got)
	}

	// 3) 放行最后一个阶段并等待正常收尾。
	close(allowA3)
	close(allowB3)
	select {
	case r := <-ar:
		if r.err != nil || r.panicked != nil {
			t.Fatalf("task A failed: err=%v panic=%v", r.err, r.panicked)
		}
	case <-time.After(testTimeout):
		t.Fatal("timeout waiting task A to finish")
	}
	select {
	case r := <-br:
		if r.err != nil || r.panicked != nil {
			t.Fatalf("task B failed: err=%v panic=%v", r.err, r.panicked)
		}
	case <-time.After(testTimeout):
		t.Fatal("timeout waiting task B to finish")
	}

	if got := m.Usage(CPU); got != 0 || m.Usage(IO) != 0 {
		t.Fatalf("quotas not fully returned: CPU=%d IO=%d", m.Usage(CPU), m.Usage(IO))
	}
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// TestZeroCapacityCancelable 验证容量为 0 的阶段永远无法满足，
// 只能通过取消退出；取消后不占用任何额度。
func TestZeroCapacityCancelable(t *testing.T) {
	m := NewManager(map[Phase]int{CPU: 1, IO: 0})

	// 先取消再申请：立即返回，不应阻塞。
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Run(cctx, []Phase{IO}, func(Phase) error {
		t.Fatal("task function must never run when capacity is 0 and ctx canceled")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}

	// 先拿到 CPU，再进入容量 0 的 IO：无限等待，必须能取消退出。
	cpuHeld := make(chan struct{})
	allowCpuDone := make(chan struct{})
	ctx, cancel2 := context.WithCancel(context.Background())
	r := runAsync(m, ctx, []Phase{CPU, IO}, func(p Phase) error {
		if p == CPU {
			close(cpuHeld)
			<-allowCpuDone
		}
		return nil
	})
	waitSignal(t, cpuHeld, "task holding CPU before zero-cap IO")
	close(allowCpuDone) // CPU 阶段完成 -> 释放 CPU -> 永远等 IO

	// 等待任务进入半转换（CPU 已归还、IO 在等），避免取消早于切换。
	waitFor(t, func() bool { return m.Usage(CPU) == 0 }, "CPU quota released while waiting on zero-cap IO")

	cancel2()
	res := waitResult(t, r, "cancel out of zero-capacity IO")
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("want context.Canceled, got err=%v panic=%v", res.err, res.panicked)
	}
	if got := m.Usage(CPU); got != 0 || m.Usage(IO) != 0 {
		t.Fatalf("quotas leaked after cancel: CPU=%d IO=%d", got, m.Usage(IO))
	}
}

// TestCancelHalfTransition 验证在“已释放旧额度、正在等新额度”的
// 半转换状态下取消：直接退出，旧额度已回归且新额度未占用。
func TestCancelHalfTransition(t *testing.T) {
	m := NewManager(map[Phase]int{CPU: 1, IO: 1})

	// 占住唯一的 CPU。
	xRelease := make(chan struct{})
	xCtx, xCancel := context.WithCancel(context.Background())
	xr := runAsync(m, xCtx, []Phase{CPU}, func(Phase) error {
		<-xRelease
		return nil
	})
	waitFor(t, func() bool { return m.Usage(CPU) == 1 }, "holder occupying CPU")

	// Y 先拿 IO，随后释放 IO 并等待 CPU（半转换）。
	yAtIo := make(chan struct{})
	yCtx, yCancel := context.WithCancel(context.Background())
	yr := runAsync(m, yCtx, []Phase{IO, CPU}, func(p Phase) error {
		if p == IO {
			close(yAtIo)
		}
		return nil
	})
	waitSignal(t, yAtIo, "task Y holding IO")
	// Y 的 IO 函数返回后进入 CPU 等待：IO 应已回归 0，CPU 仍被 X 占 1。
	waitFor(t, func() bool { return m.Usage(IO) == 0 }, "Y released IO while waiting CPU")
	if got := m.Usage(CPU); got != 1 {
		t.Fatalf("CPU usage = %d, want 1 (held by X)", got)
	}

	yCancel() // 在半转换等待中取消
	res := waitResult(t, yr, "cancel Y during half transition")
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", res.err)
	}
	if got := m.Usage(CPU); got != 1 || m.Usage(IO) != 0 {
		t.Fatalf("unexpected usage after half-transition cancel: CPU=%d IO=%d", m.Usage(CPU), m.Usage(IO))
	}

	// 持有者正常结束后，全部额度回到初值。
	close(xRelease)
	waitResult(t, xr, "holder finish")
	if got := m.Usage(CPU); got != 0 || m.Usage(IO) != 0 {
		t.Fatalf("quotas leaked: CPU=%d IO=%d", m.Usage(CPU), m.Usage(IO))
	}
	xCancel()
}

// TestFailureAndPanicReturnAllQuotas 验证异常结束（返回错误或 panic）
// 都会归还已持有的额度，且之后容量完全恢复到初值、可被重新申请。
func TestFailureAndPanicReturnAllQuotas(t *testing.T) {
	errBoom := errors.New("injected failure")

	t.Run("error", func(t *testing.T) {
		m := NewManager(map[Phase]int{CPU: 2, IO: 1})
		err := m.Run(context.Background(), []Phase{CPU, IO}, func(p Phase) error {
			if p == IO {
				return errBoom
			}
			return nil
		})
		if !errors.Is(err, errBoom) {
			t.Fatalf("want injected error, got %v", err)
		}
		assertEmpty(t, m)
		assertFullCapacityAvailable(t, m, map[Phase]int{CPU: 2, IO: 1})
	})

	t.Run("panic", func(t *testing.T) {
		m := NewManager(map[Phase]int{CPU: 2, IO: 1})
		r := <-runAsync(m, context.Background(), []Phase{CPU, IO}, func(p Phase) error {
			if p == IO {
				panic("injected panic")
			}
			return nil
		})
		if r.panicked == nil {
			t.Fatal("expected panic to propagate")
		}
		assertEmpty(t, m)
		assertFullCapacityAvailable(t, m, map[Phase]int{CPU: 2, IO: 1})
	})

	t.Run("error in first phase", func(t *testing.T) {
		m := NewManager(map[Phase]int{CPU: 1, IO: 1})
		err := m.Run(context.Background(), []Phase{CPU}, func(Phase) error { return errBoom })
		if !errors.Is(err, errBoom) {
			t.Fatalf("want injected error, got %v", err)
		}
		assertEmpty(t, m)
		assertFullCapacityAvailable(t, m, map[Phase]int{CPU: 1, IO: 1})
	})
}

// TestPhasesRunInOrder 验证阶段严格按顺序申请，时间点由测试通过 channel 注入。
func TestPhasesRunInOrder(t *testing.T) {
	m := NewManager(map[Phase]int{CPU: 2, IO: 2})
	var (
		mu  sync.Mutex
		got []Phase
	)
	gate := make(chan Phase, 3)
	err := m.Run(context.Background(), []Phase{CPU, IO, CPU}, func(p Phase) error {
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
		gate <- p
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []Phase{CPU, IO, CPU}
	if len(got) != len(want) {
		t.Fatalf("phases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("phase order = %v, want %v", got, want)
		}
	}
	assertEmpty(t, m)
}

func waitResult(t *testing.T, ch <-chan result, what string) result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(testTimeout):
		t.Fatalf("timeout waiting for %s", what)
		return result{}
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func assertEmpty(t *testing.T, m *Manager) {
	t.Helper()
	if got := m.Usage(CPU); got != 0 {
		t.Fatalf("CPU usage = %d after abnormal end, want 0", got)
	}
	if got := m.Usage(IO); got != 0 {
		t.Fatalf("IO usage = %d after abnormal end, want 0", got)
	}
}

func assertFullCapacityAvailable(t *testing.T, m *Manager, cap map[Phase]int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for p, c := range cap {
		for i := 0; i < c; i++ {
			go m.Run(ctx, []Phase{p}, func(Phase) error {
				<-ctx.Done() // 占住额度直到统一释放
				return ctx.Err()
			})
		}
	}
	waitFor(t, func() bool {
		for p, c := range cap {
			if m.Usage(p) != c {
				return false
			}
		}
		return true
	}, "full initial capacity available again")
}
