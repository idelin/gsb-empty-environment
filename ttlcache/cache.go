package ttlcache

import (
	"container/heap"
	"sync"
	"time"
)

// entry 是某键当前代（generation）的值与到期时刻。
type entry[V any] struct {
	value      V
	expiresAt  time.Duration
	generation uint64
}

// expiryEvent 到期事件：某键某一代在 at 时刻到期。
// 更新键会产生新一代，旧事件因此成为陈旧事件，
// 重放时凭 (key, generation, at) 三元组判定是否仍然有效。
type expiryEvent struct {
	at         time.Duration
	key        string
	generation uint64
	order      uint64 // 入堆序号，保证同一时刻的事件按产生顺序重放
}

// ExpiryOutcome 记录一次事件重放的结果，便于审计与测试验证。
type ExpiryOutcome struct {
	Key        string
	Generation uint64
	At         time.Duration
	Applied    bool // true=确实删除了该代；false=陈旧事件，已丢弃
}

type eventHeap []expiryEvent

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].order < h[j].order
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(expiryEvent)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	ev := old[n-1]
	*h = old[:n-1]
	return ev
}

// Cache 基于显式单调时间的 TTL 缓存。
type Cache[V any] struct {
	mu      sync.Mutex
	clock   Clock
	items   map[string]entry[V]
	events  eventHeap
	nextGen uint64
	nextOrd uint64
}

func New[V any](clock Clock) *Cache[V] {
	return &Cache[V]{clock: clock, items: make(map[string]entry[V])}
}

// Set 写入键值并开启新一代：旧代的到期事件全部失效，
// 之后重放到旧事件时不会误删本次写入的值。
func (c *Cache[V]) Set(key string, value V, ttl time.Duration) {
	if ttl < 0 {
		ttl = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextGen++
	gen := c.nextGen
	expiresAt := c.clock.Now() + ttl
	c.items[key] = entry[V]{value: value, expiresAt: expiresAt, generation: gen}
	c.pushEventLocked(key, gen, expiresAt)
}

func (c *Cache[V]) pushEventLocked(key string, gen uint64, at time.Duration) {
	c.nextOrd++
	heap.Push(&c.events, expiryEvent{at: at, key: key, generation: gen, order: c.nextOrd})
}

// Get 读取当前代的值；已到期（含恰好到期）视为不存在并惰性清除。
func (c *Cache[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	if c.clock.Now() >= e.expiresAt {
		delete(c.items, key)
		var zero V
		return zero, false
	}
	return e.value, true
}

// Delete 删除键；其到期事件留在堆中，重放时按陈旧事件丢弃。
func (c *Cache[V]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

func (c *Cache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// ExpireDue 重放所有到期时刻不晚于当前时钟的事件。
// 仅当事件的三元组与键当前代完全匹配时才删除；
// 否则作为陈旧事件丢弃。返回每个事件的处理结果。
func (c *Cache[V]) ExpireDue() []ExpiryOutcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock.Now()
	var outcomes []ExpiryOutcome
	for len(c.events) > 0 && c.events[0].at <= now {
		ev := heap.Pop(&c.events).(expiryEvent)
		outcome := ExpiryOutcome{Key: ev.key, Generation: ev.generation, At: ev.at}
		if e, ok := c.items[ev.key]; ok &&
			e.generation == ev.generation && e.expiresAt == ev.at {
			delete(c.items, ev.key)
			outcome.Applied = true
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}
