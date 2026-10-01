package clockguard

import (
	"context"
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// 验收边界：系统时间突然向前回拨 1 小时，守护器在下一瞬间检测到
// 墙钟与单调时钟严重撕裂，立即强制注销所有租约并降级为安全模式。
func TestBackwardOneHourJumpTripsGuardian(t *testing.T) {
	clock := NewFakeClock(t0)

	var tripEvent TripEvent
	var revoked []Lease
	g := NewGuardian(clock,
		WithThreshold(5*time.Second),
		OnTrip(func(ev TripEvent) { tripEvent = ev }),
		OnRevoke(func(l Lease) { revoked = append(revoked, l) }),
	)

	// 正常运行：发放 3 份租约，建立监视基线。
	for _, holder := range []string{"worker-a", "worker-b", "worker-c"} {
		if _, err := g.Acquire(holder); err != nil {
			t.Fatalf("Acquire failed: %v", err)
		}
	}
	g.Check()
	if got := g.LeaseCount(); got != 3 {
		t.Fatalf("LeaseCount = %d, want 3", got)
	}

	// 故障注入：时间正常流逝 2 秒后，系统时间被人为回拨 1 小时。
	clock.Advance(2 * time.Second)
	clock.AdvanceWall(-time.Hour)

	// 下一瞬间的采样必须立即熔断。
	if !g.Check() {
		t.Fatal("guardian did not trip on 1h backward jump")
	}
	if !g.Tripped() {
		t.Fatal("guardian not in safe mode after trip")
	}
	if got := g.LeaseCount(); got != 0 {
		t.Fatalf("LeaseCount = %d after trip, want 0 (all force-revoked)", got)
	}
	if len(revoked) != 3 {
		t.Fatalf("revoked %d leases, want 3", len(revoked))
	}
	for _, l := range revoked {
		if !l.Revoked {
			t.Errorf("lease %s not marked revoked", l.ID)
		}
	}
	if tripEvent.Reason == "" {
		t.Fatal("trip event missing reason")
	}
	if tripEvent.WallDelta >= 0 {
		t.Fatalf("WallDelta = %v, want negative (clock moved backwards)", tripEvent.WallDelta)
	}
	if tripEvent.Skew < 59*time.Minute {
		t.Fatalf("Skew = %v, want >= ~1h", tripEvent.Skew)
	}
	if tripEvent.RevokedLeases != 3 {
		t.Fatalf("RevokedLeases = %d, want 3", tripEvent.RevokedLeases)
	}

	// 安全模式：拒绝发放新租约。
	if _, err := g.Acquire("worker-d"); !errors.Is(err, ErrSafeMode) {
		t.Fatalf("Acquire in safe mode = %v, want ErrSafeMode", err)
	}
	// 已被强制注销的租约无法再释放。
	if err := g.Release("lease-1"); !errors.Is(err, ErrLeaseNotFound) {
		t.Fatalf("Release revoked lease = %v, want ErrLeaseNotFound", err)
	}
}

// NTP 大幅阶跃：墙钟突然向前跳 30 分钟。
func TestForwardNTPStepTripsGuardian(t *testing.T) {
	clock := NewFakeClock(t0)
	g := NewGuardian(clock, WithThreshold(5*time.Second))
	g.Check()

	clock.AdvanceWall(30 * time.Minute)

	if !g.Check() {
		t.Fatal("guardian did not trip on 30m forward NTP step")
	}
	ev := g.TripEvent()
	if ev == nil {
		t.Fatal("missing trip event")
	}
	if ev.WallDelta != 30*time.Minute {
		t.Fatalf("WallDelta = %v, want 30m", ev.WallDelta)
	}
}

// 虚拟机挂起：墙钟继续前进，单调时钟停滞。
func TestMonotonicStallTripsGuardian(t *testing.T) {
	clock := NewFakeClock(t0)
	g := NewGuardian(clock, WithThreshold(5*time.Second))
	g.Check()

	clock.AdvanceWall(10 * time.Minute) // 只有墙钟在走

	if !g.Check() {
		t.Fatal("guardian did not trip on stalled monotonic clock")
	}
	ev := g.TripEvent()
	if ev == nil || ev.MonoDelta != 0 {
		t.Fatalf("MonoDelta = %v, want 0 (stalled)", ev.MonoDelta)
	}
}

// 正常抖动：两个时钟源同步推进且存在阈值内的小幅偏差，不应熔断。
func TestSmallJitterDoesNotTrip(t *testing.T) {
	clock := NewFakeClock(t0)
	g := NewGuardian(clock, WithThreshold(5*time.Second))
	g.Check()

	for i := 0; i < 100; i++ {
		clock.Advance(time.Second)
		// 模拟 ±2s 以内的采样抖动（阈值 5s）。
		if i%2 == 0 {
			clock.AdvanceWall(2 * time.Second)
		} else {
			clock.AdvanceWall(-2 * time.Second)
		}
		if g.Check() {
			t.Fatalf("tripped at iteration %d under normal jitter", i)
		}
	}
	if g.Tripped() {
		t.Fatal("guardian tripped under normal jitter")
	}
}

// 租约的正常申请与释放。
func TestAcquireAndRelease(t *testing.T) {
	clock := NewFakeClock(t0)
	g := NewGuardian(clock)

	l, err := g.Acquire("worker-a")
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	if l.ID == "" || l.Holder != "worker-a" {
		t.Fatalf("bad lease: %+v", l)
	}
	if err := g.Release(l.ID); err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if err := g.Release(l.ID); !errors.Is(err, ErrLeaseNotFound) {
		t.Fatalf("double Release = %v, want ErrLeaseNotFound", err)
	}
}

// 端到端：Run 监视循环在后台运行，故障注入后异步熔断。
func TestRunLoopTripsAsynchronously(t *testing.T) {
	clock := NewFakeClock(t0)
	tripped := make(chan TripEvent, 1)
	g := NewGuardian(clock,
		WithThreshold(5*time.Second),
		WithInterval(10*time.Millisecond),
		OnTrip(func(ev TripEvent) { tripped <- ev }),
	)

	if _, err := g.Acquire("worker-a"); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	// 让监视循环跑几个周期，然后回拨系统时间 1 小时。
	time.Sleep(50 * time.Millisecond)
	clock.AdvanceWall(-time.Hour)

	select {
	case ev := <-tripped:
		if ev.RevokedLeases != 1 {
			t.Fatalf("RevokedLeases = %d, want 1", ev.RevokedLeases)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("guardian did not trip within 2s of the time jump")
	}

	if _, err := g.Acquire("worker-b"); !errors.Is(err, ErrSafeMode) {
		t.Fatalf("Acquire after trip = %v, want ErrSafeMode", err)
	}
}
