package ttlcache

import (
	"testing"
	"time"
)

// 快照记录剩余寿命与保存时刻；恢复时扣减停机时长，
// 续命剩余量，绝不重新给满 TTL。
func TestSnapshotRestore_DeductsDowntime(t *testing.T) {
	clock1 := NewManualClock()
	c1 := New[string](clock1)
	c1.Set("k", "v", 10*time.Second)

	clock1.Advance(4 * time.Second) // 已存活 4s，剩余 6s
	records := c1.Snapshot()
	if len(records) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(records))
	}
	r := records[0]
	if r.Remaining != 6*time.Second {
		t.Fatalf("Remaining = %v, want 6s", r.Remaining)
	}
	if r.SavedAt != 4*time.Second {
		t.Fatalf("SavedAt = %v, want 4s", r.SavedAt)
	}

	// 停机 2s 后在新进程（新时钟）恢复
	clock2 := NewManualClock()
	c2, expired := Restore[string](clock2, records, 2*time.Second)
	if expired != 0 {
		t.Fatalf("expired = %d, want 0", expired)
	}

	// 恢复后剩余寿命应为 6s-2s=4s，而非重新给满 10s
	clock2.Advance(4*time.Second - time.Nanosecond)
	if _, ok := c2.Get("k"); !ok {
		t.Fatal("key expired before remaining lifetime elapsed")
	}
	clock2.Advance(time.Nanosecond)
	if _, ok := c2.Get("k"); ok {
		t.Fatal("key should expire after remaining 4s, not a fresh 10s TTL")
	}
	clock2.Advance(10 * time.Second)
	if _, ok := c2.Get("k"); ok {
		t.Fatal("key must not be resurrected with full TTL")
	}
}

// 扣减后非正的记录立即过期，不得恢复。
func TestRestore_NonPositiveRemainingExpiresImmediately(t *testing.T) {
	records := []Record[string]{
		{Key: "dead", Value: "d", Remaining: 3 * time.Second, SavedAt: 0},
		{Key: "edge", Value: "e", Remaining: 5 * time.Second, SavedAt: 0}, // 恰好扣为零
		{Key: "live", Value: "l", Remaining: 8 * time.Second, SavedAt: 0},
	}
	clock := NewManualClock()
	c, expired := Restore[string](clock, records, 5*time.Second)

	if expired != 2 {
		t.Fatalf("expired = %d, want 2 (dead + edge)", expired)
	}
	if _, ok := c.Get("dead"); ok {
		t.Fatal("dead key must not be restored")
	}
	if _, ok := c.Get("edge"); ok {
		t.Fatal("zero-remaining key must expire immediately")
	}
	if _, ok := c.Get("live"); !ok {
		t.Fatal("live key should survive with 3s remaining")
	}

	clock.Advance(3 * time.Second)
	c.ExpireDue()
	if _, ok := c.Get("live"); ok {
		t.Fatal("live key should expire exactly after its deducted remaining")
	}
}

// 快照不得包含已过期键。
func TestSnapshot_SkipsExpired(t *testing.T) {
	clock := NewManualClock()
	c := New[string](clock)
	c.Set("gone", "g", time.Second)
	c.Set("stay", "s", time.Minute)
	clock.Advance(2 * time.Second)

	records := c.Snapshot()
	if len(records) != 1 || records[0].Key != "stay" {
		t.Fatalf("snapshot = %+v, want only stay", records)
	}
}

// 恢复后的键与旧缓存的事件互不干扰：恢复产生的是全新代。
func TestRestore_GenerationsAreFresh(t *testing.T) {
	clock1 := NewManualClock()
	c1 := New[string](clock1)
	c1.Set("k", "v", 10*time.Second)
	records := c1.Snapshot()

	clock2 := NewManualClock()
	c2, _ := Restore[string](clock2, records, 0)
	c2.Set("k", "v2", 30*time.Second) // 恢复后再更新

	clock2.Advance(11 * time.Second) // 越过恢复时的 10s 到期点
	c2.ExpireDue()
	if v, ok := c2.Get("k"); !ok || v != "v2" {
		t.Fatalf("restored-then-updated key harmed: (%q,%v)", v, ok)
	}
}
