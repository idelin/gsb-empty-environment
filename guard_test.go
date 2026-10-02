package clockguard

import (
	"errors"
	"testing"
	"time"
)

func newFixture(threshold time.Duration) (*FakeClock, *LeaseManager, *Guardian) {
	clock := NewFakeClock(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))
	manager := NewLeaseManager(clock)
	guard := NewGuardian(clock, manager, threshold, 10*time.Millisecond)
	return clock, manager, guard
}

// 正常运行：两个时钟步调一致，不熔断，租约保持存活。
func TestNormalOperationNoTrip(t *testing.T) {
	clock, manager, guard := newFixture(5 * time.Second)

	if _, err := manager.Acquire("lease-1", time.Minute); err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	guard.CheckOnce() // 首次采样建立基线

	for i := 0; i < 10; i++ {
		clock.Advance(100 * time.Millisecond)
		if guard.CheckOnce() {
			t.Fatalf("第 %d 次检查误触发熔断", i)
		}
	}
	if got := len(manager.Active()); got != 1 {
		t.Fatalf("活跃租约数 = %d, 期望 1", got)
	}
	if manager.SafeMode() {
		t.Fatal("不应进入安全模式")
	}
}

// 验收边界：系统时间突然回拨 1 小时，下一次检查立即熔断全部租约并降级安全模式。
func TestWallClockRollbackOneHour(t *testing.T) {
	clock, manager, guard := newFixture(5 * time.Second)

	for _, id := range []string{"lease-a", "lease-b", "lease-c"} {
		if _, err := manager.Acquire(id, time.Hour); err != nil {
			t.Fatalf("Acquire %s 失败: %v", id, err)
		}
	}
	guard.CheckOnce() // 建立基线
	clock.Advance(time.Second)

	// 模拟运维人员将系统时间向前回拨 1 小时。
	clock.JumpWall(-time.Hour)

	var ev TripEvent
	guard.OnTrip(func(e TripEvent) { ev = e })
	if !guard.CheckOnce() {
		t.Fatal("墙钟回拨 1 小时未触发熔断")
	}

	if !guard.Tripped() {
		t.Fatal("守护器未标记为已熔断")
	}
	if got := len(manager.Active()); got != 0 {
		t.Fatalf("熔断后仍有 %d 个活跃租约，期望 0", got)
	}
	if !manager.SafeMode() {
		t.Fatal("熔断后未降级为安全模式")
	}
	if _, err := manager.Acquire("lease-new", time.Minute); !errors.Is(err, ErrSafeMode) {
		t.Fatalf("安全模式下 Acquire 应返回 ErrSafeMode，实际: %v", err)
	}
	if ev.Skew >= 0 {
		t.Fatalf("回拨场景撕裂量应为负，实际 %v", ev.Skew)
	}
	if ev.WallDelta != -time.Hour+time.Second {
		t.Fatalf("墙钟推进量 = %v，期望 -59m59s", ev.WallDelta)
	}
	t.Logf("熔断事件: %s", ev.Reason)
}

// NTP 大幅向前阶跃同样触发熔断。
func TestNTPForwardStep(t *testing.T) {
	clock, manager, guard := newFixture(5 * time.Second)

	manager.Acquire("lease-1", time.Hour)
	guard.CheckOnce()
	clock.Advance(time.Second)
	clock.JumpWall(2 * time.Hour) // NTP 向前阶跃 2 小时

	if !guard.CheckOnce() {
		t.Fatal("NTP 向前阶跃未触发熔断")
	}
	if !manager.SafeMode() || len(manager.Active()) != 0 {
		t.Fatal("熔断后应注销全部租约并进入安全模式")
	}
}

// 虚拟机时钟挂起：墙钟冻结，单调时钟继续前进，撕裂超限即熔断。
func TestWallClockStall(t *testing.T) {
	clock, manager, guard := newFixture(2 * time.Second)

	manager.Acquire("lease-1", time.Hour)
	guard.CheckOnce()
	clock.AdvanceMonotonicOnly(10 * time.Second) // 墙钟挂起，单调时钟走了 10s

	if !guard.CheckOnce() {
		t.Fatal("墙钟挂起未触发熔断")
	}
	if !manager.SafeMode() {
		t.Fatal("熔断后未进入安全模式")
	}
}

// 阈值内的微小漂移（如正常 NTP 微调）不应误触发。
func TestSmallDriftTolerated(t *testing.T) {
	clock, manager, guard := newFixture(5 * time.Second)

	manager.Acquire("lease-1", time.Hour)
	guard.CheckOnce()
	clock.Advance(time.Second)
	clock.JumpWall(100 * time.Millisecond) // 正常 NTP 微调

	if guard.CheckOnce() {
		t.Fatal("阈值内的微小漂移不应触发熔断")
	}
	if manager.SafeMode() || len(manager.Active()) != 1 {
		t.Fatal("租约应保持存活且未进入安全模式")
	}
}

// 后台监视循环：熔断在下一拍自动发生，无需手动调用 CheckOnce。
func TestRunLoopDetectsTrip(t *testing.T) {
	clock, manager, guard := newFixture(5 * time.Second)
	guard.interval = 5 * time.Millisecond

	manager.Acquire("lease-1", time.Hour)
	guard.CheckOnce() // 先建立基线，避免后台首拍采到已回拨的时钟
	guard.Start()
	defer guard.Stop()

	clock.Advance(20 * time.Millisecond)
	time.Sleep(20 * time.Millisecond) // 让后台循环至少正常采样一拍
	clock.JumpWall(-time.Hour)        // 回拨 1 小时

	deadline := time.Now().Add(2 * time.Second)
	for !manager.SafeMode() {
		if time.Now().After(deadline) {
			t.Fatal("后台监视循环未在超时前熔断")
		}
		time.Sleep(time.Millisecond)
	}
	if len(manager.Active()) != 0 {
		t.Fatal("熔断后仍有活跃租约")
	}
}

// 熔断是幂等的：重复检查保持熔断状态，不会复活租约。
func TestTripIsSticky(t *testing.T) {
	clock, manager, guard := newFixture(5 * time.Second)

	manager.Acquire("lease-1", time.Hour)
	guard.CheckOnce()
	clock.JumpWall(-time.Hour)

	if !guard.CheckOnce() || !guard.CheckOnce() {
		t.Fatal("熔断状态应保持")
	}
	if _, err := manager.Acquire("x", time.Minute); !errors.Is(err, ErrSafeMode) {
		t.Fatal("熔断后不应再发放租约")
	}
}
