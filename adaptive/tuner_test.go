package adaptive

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// 严格无 goroutine 泄漏：反复 Start/Stop 后 goroutine 数回到基线。
func TestTunerNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		c, err := NewController(DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		tuner := NewTuner(c, func() Metrics { return healthy(10) }, time.Millisecond)
		tuner.Start()
		tuner.Stop()
	}
	waitGoroutines(t, base)
}

// Stop 幂等：多次调用安全；未 Start 直接 Stop 也安全。
func TestTunerStopIdempotent(t *testing.T) {
	base := runtime.NumGoroutine()
	c, _ := NewController(DefaultConfig())
	tuner := NewTuner(c, func() Metrics { return healthy(1) }, time.Millisecond)
	tuner.Stop() // 未 Start 先 Stop
	tuner.Stop()
	tuner.Start() // Stop 后 Start：goroutine 应立即退出
	tuner.Start() // 重复 Start 安全
	tuner.Stop()
	tuner.Stop()
	waitGoroutines(t, base)
}

// Tuner 确实驱动控制器：过载指标下阈值应下降。
func TestTunerDrivesController(t *testing.T) {
	c, _ := NewController(DefaultConfig())
	// 先把阈值抬高。
	clk := newTestClock()
	c.now = clk.now
	for i := 0; i < 60; i++ {
		clk.advance(3 * time.Second)
		c.Update(healthy(1 << 30))
	}
	high := c.Threshold()
	if high <= c.cfg.Min {
		t.Fatal("precondition failed")
	}
	c.now = time.Now // 恢复真实时钟供 Tuner 使用
	tuner := NewTuner(c, func() Metrics { return overloaded(1000) }, 5*time.Millisecond)
	tuner.Start()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && c.Threshold() >= high {
		time.Sleep(5 * time.Millisecond)
	}
	tuner.Stop()
	if got := c.Threshold(); got >= high {
		t.Fatalf("tuner did not drive threshold down: %v -> %v", high, got)
	}
}

// 并发 Start/Stop 无数据竞争、无泄漏（配合 -race）。
func TestTunerConcurrentStartStop(t *testing.T) {
	base := runtime.NumGoroutine()
	c, _ := NewController(DefaultConfig())
	tuner := NewTuner(c, func() Metrics { return healthy(1) }, time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tuner.Start()
			tuner.Stop()
		}()
	}
	wg.Wait()
	tuner.Stop()
	waitGoroutines(t, base)
}

func waitGoroutines(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > want && time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > want {
		t.Fatalf("goroutine leak: baseline %d, now %d", want, got)
	}
}
