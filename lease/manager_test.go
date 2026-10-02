package lease

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	testTTL      = 10 * time.Second
	testMaxDrift = 500 * time.Millisecond
)

func newCluster() (*ManualClock, *Coordinator) {
	clock := NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	return clock, NewCoordinator(clock, testTTL, testMaxDrift)
}

// 验收边界 1：TTL=10s、maxDrift=500ms，主节点在 9.5s 时必须停止下发写操作。
func TestPrimaryStopsWritesAtSafeDeadline(t *testing.T) {
	clock, coord := newCluster()
	m := NewManager("node-A", coord, clock, testTTL, testMaxDrift)
	if err := m.Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}

	// 9.4s：仍在安全期内，写应成功。
	clock.Advance(9400 * time.Millisecond)
	if _, err := m.Write("put k1=v1"); err != nil {
		t.Fatalf("write at 9.4s should succeed: %v", err)
	}
	if !m.IsPrimary() {
		t.Fatal("should still be primary at 9.4s")
	}

	// 推进到 9.5s：安全期结束，必须主动放弃特权并拒绝写。
	clock.Advance(100 * time.Millisecond)
	if _, err := m.Write("put k2=v2"); !errors.Is(err, ErrNotPrimary) {
		t.Fatalf("write at 9.5s must fail with ErrNotPrimary, got %v", err)
	}
	if m.IsPrimary() {
		t.Fatal("must not be primary at/after 9.5s")
	}
	if reason := m.DemoteReason(); !strings.Contains(reason, "safe period") {
		t.Fatalf("expected safe-period demotion, got %q", reason)
	}
	t.Logf("demoted at 9.5s, reason: %s", m.DemoteReason())
}

// 验收边界 2：注入时钟前拨 1s 突变，安全自检触发紧急提前降级。
func TestClockJumpTriggersEmergencyDemotion(t *testing.T) {
	clock, coord := newCluster()
	m := NewManager("node-A", coord, clock, testTTL, testMaxDrift)
	if err := m.Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}

	// 正常推进 2s，写正常。
	clock.Advance(2 * time.Second)
	if _, err := m.Write("put k1=v1"); err != nil {
		t.Fatalf("write before jump should succeed: %v", err)
	}

	// 注入：墙上时钟前拨 1s（单调时钟不变），远超 500ms 最大漂移。
	clock.JumpWall(1 * time.Second)

	m.SafetyCheck()
	if m.IsPrimary() {
		t.Fatal("emergency demotion expected after 1s wall-clock jump")
	}
	if _, err := m.Write("put k2=v2"); !errors.Is(err, ErrNotPrimary) {
		t.Fatalf("write after clock jump must fail with ErrNotPrimary, got %v", err)
	}
	if reason := m.DemoteReason(); !strings.Contains(reason, "emergency") {
		t.Fatalf("expected emergency demotion, got %q", reason)
	}
	t.Logf("emergency demoted at t=2s, reason: %s", m.DemoteReason())
}

// 小幅漂移（<=500ms）不应触发降级。
func TestSmallDriftWithinTolerance(t *testing.T) {
	clock, coord := newCluster()
	m := NewManager("node-A", coord, clock, testTTL, testMaxDrift)
	if err := m.Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	clock.Advance(3 * time.Second)
	clock.JumpWall(300 * time.Millisecond) // 在容忍范围内
	m.SafetyCheck()
	if !m.IsPrimary() {
		t.Fatalf("300ms drift must be tolerated, demoted: %s", m.DemoteReason())
	}
}

// 脑裂防护：旧主降级后，协调端在 TTL+maxDrift 安全窗口内拒绝新主，
// 且新主 fencing token 严格更大。
func TestNoSplitBrain(t *testing.T) {
	clock, coord := newCluster()
	a := NewManager("node-A", coord, clock, testTTL, testMaxDrift)
	b := NewManager("node-B", coord, clock, testTTL, testMaxDrift)

	if err := a.Campaign(); err != nil {
		t.Fatalf("A campaign: %v", err)
	}
	fencingA := a.Fencing()

	// A 在 9.5s 安全期结束降级。
	clock.Advance(9500 * time.Millisecond)
	a.SafetyCheck()
	if a.IsPrimary() {
		t.Fatal("A should have demoted at 9.5s")
	}

	// 10.4s：仍在 TTL+maxDrift=10.5s 安全窗口内，B 竞选必须失败。
	clock.Advance(900 * time.Millisecond)
	if err := b.Campaign(); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("B campaign at 10.4s must fail with ErrLeaseHeld, got %v", err)
	}

	// 10.5s：旧租约彻底失效，B 成功上位，fencing 单调递增。
	clock.Advance(100 * time.Millisecond)
	if err := b.Campaign(); err != nil {
		t.Fatalf("B campaign at 10.5s should succeed: %v", err)
	}
	if b.Fencing() <= fencingA {
		t.Fatalf("fencing must increase: A=%d B=%d", fencingA, b.Fencing())
	}

	// 任意时刻至多一个主。
	if a.IsPrimary() && b.IsPrimary() {
		t.Fatal("split brain: both nodes primary")
	}
	t.Logf("no split brain: A fencing=%d demoted, B fencing=%d primary", fencingA, b.Fencing())
}

// 看门狗端到端：真实时钟 + 缩短参数，验证后台自检自动降级。
func TestWatchdogAutoDemotion(t *testing.T) {
	clock := NewRealClock()
	coord := NewCoordinator(clock, 300*time.Millisecond, 50*time.Millisecond)
	m := NewManager("node-A", coord, clock, 300*time.Millisecond, 50*time.Millisecond)
	if err := m.Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	m.StartWatchdog(10 * time.Millisecond)
	defer m.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !m.IsPrimary() {
			return // 看门狗已在安全期结束时自动降级
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("watchdog did not demote primary after safe period")
}
