package memsched

import (
	"sync"
	"testing"
	"time"
)

// 验收场景：注入 100 个批处理任务；内存达到 80% 阈值时触发反压，
// 拦截新协程创建，直到存量协程释放内存后平滑恢复接单。
func TestBackpressureAndRecovery(t *testing.T) {
	const (
		budget = int64(1000) // 全局内存配额
		tasks  = 100         // 注入的批处理任务数
		alloc  = int64(100)  // 每个任务申请的临时对象大小
	)
	mon := NewMonitor(budget, 0.8, 0.6)
	sched := NewScheduler(mon, 32, 128)
	sched.Start()
	defer sched.Stop()

	// 并发提交，制造准入压力。
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < tasks; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f := sched.Submit(func(c *Context) error {
					c.Alloc(alloc)
					time.Sleep(2 * time.Millisecond) // 模拟批处理
					c.Release(alloc)
					return nil
				}, false)
				if err := f.Wait(); err != nil {
					t.Errorf("fiber %d failed: %v", f.ID, err)
				}
			}()
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out: backpressure deadlocked the pipeline")
	}

	st := mon.Stats()
	if st.Admitted != tasks {
		t.Errorf("admitted = %d, want %d", st.Admitted, tasks)
	}
	if got := sched.Completed(); got != tasks {
		t.Errorf("completed = %d, want %d", got, tasks)
	}
	if st.BackpressureEvents == 0 {
		t.Error("expected backpressure to trigger at the 80% watermark")
	}
	if st.ResumeEvents == 0 {
		t.Error("expected smooth resume after memory was released")
	}
	if st.ResumeEvents > st.BackpressureEvents {
		t.Errorf("resume events %d exceed backpressure events %d", st.ResumeEvents, st.BackpressureEvents)
	}
	if st.Peak > budget {
		t.Errorf("quota violated: peak %d > budget %d", st.Peak, budget)
	}
	if st.Used != 0 {
		t.Errorf("memory leaked: %d bytes still in use", st.Used)
	}
	t.Logf("stats: %+v", st)
}

// 准入必须在高水位被拦截、在低水位恢复（滞回），而不是抖动式放行。
func TestAdmissionHysteresis(t *testing.T) {
	mon := NewMonitor(1000, 0.8, 0.6)
	holder := &Fiber{}
	mon.Alloc(holder, 850) // 越过 80% 高水位

	admitted := make(chan struct{})
	go func() {
		mon.Admit()
		close(admitted)
	}()

	select {
	case <-admitted:
		t.Fatal("admission should be blocked above the high watermark")
	case <-time.After(50 * time.Millisecond):
	}
	if !mon.UnderPressure() {
		t.Fatal("monitor should report backpressure")
	}

	mon.Release(holder, 100) // 降到 750，仍高于 60% 低水位：不应恢复
	select {
	case <-admitted:
		t.Fatal("admission should stay blocked until usage drops to the low watermark")
	case <-time.After(50 * time.Millisecond):
	}

	mon.Release(holder, 200) // 降到 550，低于低水位：平滑恢复
	select {
	case <-admitted:
	case <-time.After(time.Second):
		t.Fatal("admission should resume after usage falls below the low watermark")
	}
	if mon.UnderPressure() {
		t.Fatal("backpressure should be cleared")
	}
	st := mon.Stats()
	if st.BackpressureEvents != 1 || st.ResumeEvents != 1 {
		t.Errorf("events = (%d, %d), want (1, 1)", st.BackpressureEvents, st.ResumeEvents)
	}
}

// 内存吃紧时，具备 GC 回收能力的协程必须优先于普通协程被调度。
func TestReclaimCapableFibersScheduledFirst(t *testing.T) {
	mon := NewMonitor(1<<20, 0.8, 0.6)
	sched := NewScheduler(mon, 1, 16) // 单 worker，调度顺序可确定
	sched.Start()
	defer sched.Stop()

	release := make(chan struct{})
	blocker := sched.Submit(func(c *Context) error {
		<-release // 占住唯一 worker，让后续任务排队
		return nil
	}, false)

	var mu sync.Mutex
	var order []string
	record := func(name string) Task {
		return func(c *Context) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return nil
		}
	}
	normal := sched.Submit(record("normal"), false)
	reclaim := sched.Submit(record("reclaim"), true)

	close(release)
	blocker.Wait()
	normal.Wait()
	reclaim.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "reclaim" || order[1] != "normal" {
		t.Fatalf("execution order = %v, want [reclaim normal]", order)
	}
}

// 每个 Fiber 的临时对象大小必须被精确统计，且全局配额不可突破。
func TestPerFiberAccountingAndQuota(t *testing.T) {
	mon := NewMonitor(200, 0.8, 0.6)

	f1, f2 := &Fiber{}, &Fiber{}
	mon.Alloc(f1, 120)
	mon.Alloc(f2, 60)
	if got := f1.Used(); got != 120 {
		t.Errorf("f1 used = %d, want 120", got)
	}
	if st := mon.Stats(); st.Used != 180 || st.Peak != 180 {
		t.Errorf("used/peak = %d/%d, want 180/180", st.Used, st.Peak)
	}

	// 再申请 50 会超出配额 200：必须阻塞，直到 f1 释放。
	allocated := make(chan struct{})
	go func() {
		mon.Alloc(f2, 50)
		close(allocated)
	}()
	select {
	case <-allocated:
		t.Fatal("alloc beyond the hard quota must block")
	case <-time.After(50 * time.Millisecond):
	}
	mon.Release(f1, 120)
	select {
	case <-allocated:
	case <-time.After(time.Second):
		t.Fatal("alloc should proceed after memory is released")
	}
	st := mon.Stats()
	if st.Used != 110 {
		t.Errorf("used = %d, want 110", st.Used)
	}
	if st.Peak != 180 {
		t.Errorf("peak = %d, want 180 (quota 200 never exceeded)", st.Peak)
	}
}
