package adaptive

import (
	"sync"
	"testing"
)

// 许可严格无泄漏：并发获取/释放后 InFlight 必须归零。
func TestLimiterNoPermitLeak(t *testing.T) {
	c, err := NewController(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	lim := NewLimiter(c)
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				release, ok := lim.Acquire()
				if !ok {
					continue
				}
				release()
			}
		}()
	}
	wg.Wait()
	if n := lim.InFlight(); n != 0 {
		t.Fatalf("permit leak: InFlight = %d, want 0", n)
	}
}

// 释放函数幂等：重复释放不得多减计数。
func TestLimiterReleaseIdempotent(t *testing.T) {
	c, _ := NewController(DefaultConfig())
	lim := NewLimiter(c)
	release, ok := lim.Acquire()
	if !ok {
		t.Fatal("acquire failed")
	}
	release()
	release()
	release()
	if n := lim.InFlight(); n != 0 {
		t.Fatalf("double release corrupted count: %d", n)
	}
}

// 准入受动态阈值约束：阈值为 Min=4 时最多 4 个许可。
func TestLimiterRespectsThreshold(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Min, cfg.Max = 4, 8 // 不调用 Update，阈值保持初始值 Min=4
	c, err := NewController(cfg)
	if err != nil {
		t.Fatal(err)
	}
	lim := NewLimiter(c)
	var releases []func()
	for i := 0; i < 4; i++ {
		r, ok := lim.Acquire()
		if !ok {
			t.Fatalf("acquire %d rejected below threshold", i)
		}
		releases = append(releases, r)
	}
	if _, ok := lim.Acquire(); ok {
		t.Fatal("acquire beyond threshold accepted")
	}
	if lim.Allow() {
		t.Fatal("Allow() = true at saturation")
	}
	releases[0]()
	if !lim.Allow() {
		t.Fatal("Allow() = false after release")
	}
	for _, r := range releases[1:] {
		r()
	}
	if n := lim.InFlight(); n != 0 {
		t.Fatalf("permit leak: %d", n)
	}
}
