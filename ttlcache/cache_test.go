package ttlcache

import (
	"testing"
	"time"
)

func TestSetGetAndExpiry(t *testing.T) {
	clock := NewManualClock()
	c := New[string](clock)

	c.Set("a", "alpha", 5*time.Second)
	if v, ok := c.Get("a"); !ok || v != "alpha" {
		t.Fatalf("got (%q,%v), want (alpha,true)", v, ok)
	}

	clock.Advance(4 * time.Second)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("key expired too early")
	}

	clock.Advance(1 * time.Second) // 恰好到期，边界即过期
	outcomes := c.ExpireDue()
	if len(outcomes) != 1 || !outcomes[0].Applied {
		t.Fatalf("ExpireDue outcomes = %+v, want one applied", outcomes)
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("key should be expired")
	}
}

// TestEventReplay_UpdateDoesNotDeleteNewValue 是核心验证：
// 更新键产生新一代，重放旧代到期事件时不得删除新值。
func TestEventReplay_UpdateDoesNotDeleteNewValue(t *testing.T) {
	clock := NewManualClock()
	c := New[string](clock)

	c.Set("k", "v1", 5*time.Second)  // 第 1 代，事件 E1 @5s
	c.Set("k", "v2", 10*time.Second) // 第 2 代，事件 E2 @10s；E1 变陈旧

	clock.Advance(6 * time.Second) // 越过 E1，未到 E2
	outcomes := c.ExpireDue()
	if len(outcomes) != 1 {
		t.Fatalf("want 1 replayed event, got %d", len(outcomes))
	}
	if outcomes[0].Applied {
		t.Fatal("stale event for old generation must not be applied")
	}
	if outcomes[0].Generation != 1 {
		t.Fatalf("replayed event generation = %d, want 1", outcomes[0].Generation)
	}
	if v, ok := c.Get("k"); !ok || v != "v2" {
		t.Fatalf("old expiry event deleted new value: got (%q,%v)", v, ok)
	}

	clock.Advance(4 * time.Second) // 到达 E2 @10s
	outcomes = c.ExpireDue()
	if len(outcomes) != 1 || !outcomes[0].Applied || outcomes[0].Generation != 2 {
		t.Fatalf("gen-2 event should apply, got %+v", outcomes)
	}
	if _, ok := c.Get("k"); ok {
		t.Fatal("gen-2 value should expire at its own deadline")
	}
}

// 多次连续更新：所有旧代事件依次重放均为陈旧，最新值存活。
func TestEventReplay_MultipleUpdates(t *testing.T) {
	clock := NewManualClock()
	c := New[int](clock)

	c.Set("k", 1, 2*time.Second)
	c.Set("k", 2, 4*time.Second)
	c.Set("k", 3, 6*time.Second)

	clock.Advance(5 * time.Second) // 越过第 1、2 代的事件
	outcomes := c.ExpireDue()
	if len(outcomes) != 2 {
		t.Fatalf("want 2 replayed events, got %d", len(outcomes))
	}
	for _, o := range outcomes {
		if o.Applied {
			t.Fatalf("stale event applied: %+v", o)
		}
	}
	if v, ok := c.Get("k"); !ok || v != 3 {
		t.Fatalf("got (%d,%v), want (3,true)", v, ok)
	}
}

// 删除后重建同名键：旧事件重放不得误伤新键。
func TestEventReplay_DeleteThenRecreate(t *testing.T) {
	clock := NewManualClock()
	c := New[string](clock)

	c.Set("k", "old", 3*time.Second)
	c.Delete("k")
	c.Set("k", "new", 10*time.Second)

	clock.Advance(5 * time.Second)
	outcomes := c.ExpireDue()
	if len(outcomes) != 1 || outcomes[0].Applied {
		t.Fatalf("deleted key's event must be dropped, got %+v", outcomes)
	}
	if v, ok := c.Get("k"); !ok || v != "new" {
		t.Fatalf("recreated key harmed by old event: (%q,%v)", v, ok)
	}
}

func TestLazyExpiryOnGet(t *testing.T) {
	clock := NewManualClock()
	c := New[string](clock)
	c.Set("x", "v", time.Second)
	clock.Advance(2 * time.Second)
	if _, ok := c.Get("x"); ok {
		t.Fatal("Get must lazily expire")
	}
	if c.Len() != 0 {
		t.Fatalf("Len = %d, want 0", c.Len())
	}
}
