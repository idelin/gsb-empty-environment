package netsec

import (
	"testing"
	"time"
)

func TestTTLCacheSeenAddEvict(t *testing.T) {
	clock := newFakeClock()
	cache := newTTLCache(time.Minute, 3, clock.now)

	k1, k2, k3, k4 := cacheKey([]byte("a")), cacheKey([]byte("b")), cacheKey([]byte("c")), cacheKey([]byte("d"))
	cache.add(k1)
	cache.add(k2)
	cache.add(k3)
	if !cache.seen(k1) || !cache.seen(k2) || !cache.seen(k3) {
		t.Fatal("inserted keys must be visible")
	}

	// 访问 k1 使其成为最近使用，再插入 k4 时应淘汰最久未使用的 k2。
	if !cache.seen(k1) {
		t.Fatal("k1 should still be present")
	}
	cache.add(k4)
	if cache.seen(k2) {
		t.Fatal("k2 should have been evicted as LRU tail")
	}
	if !cache.seen(k1) || !cache.seen(k3) || !cache.seen(k4) {
		t.Fatal("remaining keys must stay visible")
	}
	if len(cache.entries) != 3 {
		t.Fatalf("cache size = %d, want 3", len(cache.entries))
	}
}

func TestTTLCacheExpiry(t *testing.T) {
	clock := newFakeClock()
	cache := newTTLCache(time.Minute, 4, clock.now)
	key := cacheKey([]byte("once"))
	cache.add(key)
	if !cache.seen(key) {
		t.Fatal("fresh entry must be a hit")
	}
	clock.advance(time.Minute + time.Second)
	if cache.seen(key) {
		t.Fatal("expired entry must be a miss and removed")
	}
}
