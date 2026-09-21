package quota

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// eventually 在 deadline 内周期性检查 cond，用于等待由 channel 闸门
// 驱动的确定时机，避免对真实 sleep 时机的依赖。
func eventually(cond func() bool) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// TestSequentialFlowAndAccounting 验证基本的申请/切换/归还以及额度记账。
func TestSequentialFlowAndAccounting(t *testing.T) {
	sched := New(map[Phase]int{CPU: 1, IO: 1})
	if got := sched.Available(CPU); got != 1 {
		t.Fatalf("initial CPU available = %d, want 1", got)
	}

	enteredIO := make(chan struct{})
	releaseIO := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- sched.Run(context.Background(),
			[]Phase{CPU, IO},
			map[Phase]Work{
				CPU: func(context.Context) error { return nil },
				IO: func(context.Context) error {
					close(enteredIO)
					<-releaseIO
					return nil
				},
			})
	}()

	<-enteredIO
	// 任务处于 IO 阶段：CPU 额度已释放，IO 额度被占用。
	if got := sched.Available(CPU); got != 1 {
		t.Fatalf("CPU while task in IO = %d, want 1", got)
	}
	if got := sched.Available(IO); got != 0 {
		t.Fatalf("IO while task in IO = %d, want 0", got)
	}

	close(releaseIO)
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if got := sched.Available(CPU); got != 1 || sched.Available(IO) != 1 {
		t.Fatalf("after finish avail cpu=%d io=%d, want 1/1", got, sched.Available(IO))
	}
}

// TestTwoTasksSwapPhases 两个容量均为 1 的阶段，两个任务同时互换阶段。
// 旧实现若“持有旧额度等待新额度”会死锁；本实现先释放后等待，双方都有进展。
func TestTwoTasksSwapPhases(t *testing.T) {
	sched := New(map[Phase]int{CPU: 1, IO: 1})

	// 两个任务先各自拿到一个阶段。
	t1 := sched.NewTask()
	t2 := sched.NewTask()
	ctx := context.Background()
	if err := t1.Acquire(ctx, CPU); err != nil {
		t.Fatal(err)
	}
	if err := t2.Acquire(ctx, IO); err != nil {
		t.Fatal(err)
	}
	if sched.Available(CPU) != 0 || sched.Available(IO) != 0 {
		t.Fatalf("before swap avail cpu=%d io=%d, want 0/0", sched.Available(CPU), sched.Available(IO))
	}

	// 两侧都就位后同时放行切换，形成对称交换。
	ready := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var errs [2]error
	startSwap := func(task *Task, target Phase, idx int) {
		defer wg.Done()
		<-ready
		errs[idx] = task.Switch(context.Background(), target)
	}
	go startSwap(t1, IO, 0)
	go startSwap(t2, CPU, 1)
	close(ready)

	// 用超时兜底：若存在占有等待死锁，这里会超时失败。
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("swap deadlocked: both tasks held old quota while waiting")
	}

	for i, err := range errs {
		if err != nil {
			t.Fatalf("swap task %d: %v", i, err)
		}
	}
	if p, _ := t1.Current(); p != IO {
		t.Fatalf("t1 phase = %q, want io", p)
	}
	if p, _ := t2.Current(); p != CPU {
		t.Fatalf("t2 phase = %q, want cpu", p)
	}

	t1.Release()
	t2.Release()
	if sched.Available(CPU) != 1 || sched.Available(IO) != 1 {
		t.Fatalf("after release avail cpu=%d io=%d, want 1/1", sched.Available(CPU), sched.Available(IO))
	}
}

// TestZeroCapacityCancelable 某阶段容量为 0 时，任务无法获取额度，
// 通过取消上下文退出而不是永久阻塞。
func TestZeroCapacityCancelable(t *testing.T) {
	sched := New(map[Phase]int{CPU: 1, IO: 0})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- sched.Run(ctx, []Phase{CPU, IO}, map[Phase]Work{
			CPU: func(context.Context) error { return nil },
		})
	}()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("zero-capacity acquire could not be canceled")
	}

	// 取消退出后额度仍回归初值。
	if sched.Available(CPU) != 1 {
		t.Fatalf("CPU after cancel = %d, want 1", sched.Available(CPU))
	}
}

// TestCancellationDuringHalfTransition 取消发生在“已释放旧额度、正在等待
// 新额度”的半转换状态：任务应不再持有任何额度，且旧阶段额度已对他人可用。
func TestCancellationDuringHalfTransition(t *testing.T) {
	sched := New(map[Phase]int{CPU: 1, IO: 1})

	// t1 先占住唯一的 IO 额度。
	holder := sched.NewTask()
	if err := holder.Acquire(context.Background(), IO); err != nil {
		t.Fatal(err)
	}

	task := sched.NewTask()
	if err := task.Acquire(context.Background(), CPU); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	switchDone := make(chan error, 1)
	go func() { switchDone <- task.Switch(ctx, IO) }()

	// 等待进入半转换状态：CPU 已释放（可用数恢复为 1），IO 仍被 holder 占用。
	if !eventually(func() bool { return sched.Available(CPU) == 1 }) {
		t.Fatal("task never released CPU quota before waiting for IO")
	}
	if _, held := task.Current(); held {
		t.Fatal("task still holds a quota while blocked in half-transition")
	}

	cancel()
	select {
	case err := <-switchDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Switch returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("half-transition wait could not be canceled")
	}

	if _, held := task.Current(); held {
		t.Fatal("task holds a quota after canceled half-transition")
	}
	if sched.Available(CPU) != 1 {
		t.Fatalf("CPU after cancel = %d, want 1 (quota leaked)", sched.Available(CPU))
	}
	if sched.Available(IO) != 0 {
		t.Fatalf("IO after cancel = %d, want 0 (holder still occupies it)", sched.Available(IO))
	}

	holder.Release()
	task.Release() // 取消后不持有额度，空操作
	if sched.Available(CPU) != 1 || sched.Available(IO) != 1 {
		t.Fatalf("avail cpu=%d io=%d, want 1/1", sched.Available(CPU), sched.Available(IO))
	}
}

// TestQuotaRestoredAfterFailure 工作返回错误后，所有额度回归初值。
func TestQuotaRestoredAfterFailure(t *testing.T) {
	sched := New(map[Phase]int{CPU: 2, IO: 1})
	wantErr := errors.New("boom")

	err := sched.Run(context.Background(), []Phase{CPU, IO}, map[Phase]Work{
		CPU: func(context.Context) error { return nil },
		IO:  func(context.Context) error { return wantErr },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run returned %v, want boom", err)
	}
	if sched.Available(CPU) != 2 || sched.Available(IO) != 1 {
		t.Fatalf("after failure avail cpu=%d io=%d, want 2/1", sched.Available(CPU), sched.Available(IO))
	}
}

// TestQuotaRestoredAfterPanic 工作 panic 后，defer 仍归还额度，panic 继续上抛。
func TestQuotaRestoredAfterPanic(t *testing.T) {
	sched := New(map[Phase]int{CPU: 1, IO: 1})

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to propagate")
			}
		}()
		_ = sched.Run(context.Background(), []Phase{CPU, IO}, map[Phase]Work{
			CPU: func(context.Context) error { return nil },
			IO:  func(context.Context) error { panic("kaboom") },
		})
	}()

	if sched.Available(CPU) != 1 || sched.Available(IO) != 1 {
		t.Fatalf("after panic avail cpu=%d io=%d, want 1/1", sched.Available(CPU), sched.Available(IO))
	}
}

// TestConcurrentStressRace 并发混合成功/失败任务，在 -race 下验证记账正确，
// 结束后额度全部回归初值。
func TestConcurrentStressRace(t *testing.T) {
	sched := New(map[Phase]int{CPU: 3, IO: 2})
	const n = 60

	var wg sync.WaitGroup
	var failCount int64
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			err := sched.Run(context.Background(), []Phase{CPU, IO}, map[Phase]Work{
				CPU: func(context.Context) error { return nil },
				IO: func(context.Context) error {
					if i%3 == 0 {
						atomic.AddInt64(&failCount, 1)
						return errors.New("injected failure")
					}
					return nil
				},
			})
			if i%3 == 0 && err == nil {
				t.Errorf("task %d: expected error", i)
			}
		}()
	}
	wg.Wait()

	if sched.Available(CPU) != 3 || sched.Available(IO) != 2 {
		t.Fatalf("after stress avail cpu=%d io=%d, want 3/2 (failures=%d)",
			sched.Available(CPU), sched.Available(IO), failCount)
	}
}
