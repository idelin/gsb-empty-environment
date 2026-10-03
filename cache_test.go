package cachebench

import (
	"strconv"
	"testing"
	"time"
)

func testMetrics() *Metrics {
	return NewMetrics(time.Minute, 8)
}

func TestCacheSetGet(t *testing.T) {
	c := NewCache(1024, testMetrics())
	c.Set("foo", []byte("bar"))
	v, ok := c.Get("foo")
	if !ok {
		t.Fatal("expected hit for foo")
	}
	if string(v) != "bar" {
		t.Fatalf("got %q, want %q", v, "bar")
	}
	if got := c.Metrics().Hits(); got != 1 {
		t.Fatalf("hits = %d, want 1", got)
	}
	if got := c.Metrics().Sets(); got != 1 {
		t.Fatalf("sets = %d, want 1", got)
	}
}

func TestCacheMiss(t *testing.T) {
	c := NewCache(1024, testMetrics())
	if _, ok := c.Get("nope"); ok {
		t.Fatal("expected miss")
	}
	if got := c.Metrics().Misses(); got != 1 {
		t.Fatalf("misses = %d, want 1", got)
	}
}

func TestCacheOverwrite(t *testing.T) {
	c := NewCache(1024, testMetrics())
	c.Set("k", []byte("v1"))
	c.Set("k", []byte("v2-longer"))
	v, ok := c.Get("k")
	if !ok || string(v) != "v2-longer" {
		t.Fatalf("got %q ok=%v", v, ok)
	}
	mem := c.MemoryStats()
	want := int64(len("k") + len("v2-longer"))
	if mem.LogicalBytes != want {
		t.Fatalf("logical bytes = %d, want %d", mem.LogicalBytes, want)
	}
	if mem.LiveEntries != 1 {
		t.Fatalf("live entries = %d, want 1", mem.LiveEntries)
	}
}

func TestCacheLRUEviction(t *testing.T) {
	// 容量等于分片数：每个分片仅容纳 1 个条目。
	c := NewCache(shardCount, testMetrics())
	const n = shardCount * 4
	for i := 0; i < n; i++ {
		c.Set("key-"+strconv.Itoa(i), []byte("v"))
	}
	m := c.Metrics()
	if got := m.Evictions(); got != n-shardCount {
		t.Fatalf("evictions = %d, want %d", got, n-shardCount)
	}
	mem := c.MemoryStats()
	if mem.LiveEntries != shardCount {
		t.Fatalf("live entries = %d, want %d", mem.LiveEntries, shardCount)
	}
	// 最早写入的 key-0 必然已被驱逐。
	if _, ok := c.Get("key-0"); ok {
		t.Fatal("expected key-0 to be evicted")
	}
}

func TestMemoryFragmentationRange(t *testing.T) {
	c := NewCache(4096, testMetrics())
	for i := 0; i < 1000; i++ {
		c.Set(strconv.Itoa(i), []byte("value"))
	}
	mem := c.MemoryStats()
	if mem.CacheFragmentationRate <= 0 || mem.CacheFragmentationRate >= 1 {
		t.Fatalf("cache fragmentation rate out of range: %v", mem.CacheFragmentationRate)
	}
	if mem.GoHeapFragmentationRate < 0 || mem.GoHeapFragmentationRate >= 1 {
		t.Fatalf("heap fragmentation rate out of range: %v", mem.GoHeapFragmentationRate)
	}
}
