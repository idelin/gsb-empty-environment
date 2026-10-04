package leasearb

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func setup() (*Arbiter, *ManualClock) {
	clk := NewManualClock(t0)
	return NewArbiter(clk), clk
}

func TestAcquireAndWrite(t *testing.T) {
	a, _ := setup()
	l, err := a.Acquire("alice", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if l.Generation != 1 {
		t.Fatalf("want gen 1, got %d", l.Generation)
	}
	if v := a.AdmitWrite("alice", 1, "r1"); v != Accepted {
		t.Fatalf("want Accepted, got %v", v)
	}
}

func TestDuplicateVsStale(t *testing.T) {
	a, _ := setup()
	a.Acquire("alice", 10*time.Second)
	a.AdmitWrite("alice", 1, "r1")

	// 同代次重试 -> 重复
	if v := a.AdmitWrite("alice", 1, "r1"); v != Duplicate {
		t.Fatalf("want Duplicate, got %v", v)
	}

	// 交接后：旧请求返回 -> 仍是重复（幂等），旧的新写入 -> 过期持有者
	if _, err := a.Transfer("alice", "bob", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if v := a.AdmitWrite("alice", 1, "r1"); v != Duplicate {
		t.Fatalf("delayed ack: want Duplicate, got %v", v)
	}
	if v := a.AdmitWrite("alice", 1, "r2"); v != StaleGeneration {
		t.Fatalf("in-flight write: want StaleGeneration, got %v", v)
	}
	if v := a.AdmitWrite("bob", 2, "r3"); v != Accepted {
		t.Fatalf("new holder: want Accepted, got %v", v)
	}
}

func TestRenewCannotResurrect(t *testing.T) {
	a, _ := setup()
	a.Acquire("alice", 10*time.Second)
	a.Transfer("alice", "bob", 10*time.Second)

	if _, err := a.Renew("alice", 1, 10*time.Second); err != ErrStaleGeneration {
		t.Fatalf("want ErrStaleGeneration, got %v", err)
	}
	if _, err := a.Renew("bob", 2, 10*time.Second); err != nil {
		t.Fatalf("current holder renew should succeed: %v", err)
	}
}

func TestClockJumpExpiresLease(t *testing.T) {
	a, clk := setup()
	a.Acquire("alice", 10*time.Second)

	clk.Advance(30 * time.Second) // 时钟前跳，越过到期点

	if _, active := a.Snapshot(); active {
		t.Fatal("lease should be inactive after clock jump")
	}
	if v := a.AdmitWrite("alice", 1, "r1"); v != LeaseExpired {
		t.Fatalf("want LeaseExpired, got %v", v)
	}
	// 他人可以获取，代次前进
	l, err := a.Acquire("carol", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if l.Generation != 2 {
		t.Fatalf("want gen 2, got %d", l.Generation)
	}
	// 过期前已应用的写入，其延迟重试仍是 Duplicate 而非 LeaseExpired
	a.AdmitWrite("carol", 2, "c1")
	clk.Advance(30 * time.Second)
	if v := a.AdmitWrite("carol", 2, "c1"); v != Duplicate {
		t.Fatalf("want Duplicate, got %v", v)
	}
}

func TestAcquireWhileHeldFails(t *testing.T) {
	a, _ := setup()
	a.Acquire("alice", 10*time.Second)
	if _, err := a.Acquire("bob", 10*time.Second); err != ErrLeaseHeld {
		t.Fatalf("want ErrLeaseHeld, got %v", err)
	}
}

func TestGenerationNeverReused(t *testing.T) {
	a, clk := setup()
	a.Acquire("alice", time.Second)
	clk.Advance(2 * time.Second)
	l, _ := a.Acquire("bob", time.Second)
	clk.Advance(2 * time.Second)
	l2, _ := a.Acquire("carol", time.Second)
	if l.Generation != 2 || l2.Generation != 3 {
		t.Fatalf("generations must increase monotonically: %d, %d", l.Generation, l2.Generation)
	}
}

func TestNotHolder(t *testing.T) {
	a, _ := setup()
	a.Acquire("alice", 10*time.Second)
	if v := a.AdmitWrite("mallory", 1, "r1"); v != NotHolder {
		t.Fatalf("want NotHolder, got %v", v)
	}
	if _, err := a.Transfer("mallory", "bob", 10*time.Second); err != ErrNotHolder {
		t.Fatalf("want ErrNotHolder, got %v", err)
	}
}
