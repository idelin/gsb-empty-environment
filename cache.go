// Package ttlcache 实现一个基于最小堆调度过期的内存缓存。
//
// 核心机制：
//   - 每次写入/续期分配全局单调递增的代际号 gen，缓存条目与堆节点双向校验；
//   - 续期只追加新堆节点，旧节点惰性失效（弹出时发现代际不符即丢弃）；
//   - 堆中陈旧节点比例过高时触发压缩，压缩期间新定时器进入 pending 缓冲，
//     新堆原子换入，不丢事件；
//   - 半开语义：键在 [写入时刻, expireAt) 内有效，now == expireAt 即失效；
//   - 时钟回拨策略：所有过期判断都基于当前时钟的即时比较，回拨只推迟触发，
//     绝不提前删除，也绝不复活已删除的条目。
package ttlcache

import (
	"container/heap"
	"errors"
	"sync"
	"time"
)

// ErrNotFound 在键不存在（或已过期）且无 Loader 时返回。
var ErrNotFound = errors.New("ttlcache: key not found")

// EvictReason 表示条目被移除的原因。
type EvictReason int

const (
	ReasonExpired  EvictReason = iota // 到期被过期堆删除
	ReasonDeleted                     // 被 Delete 显式删除
	ReasonReplaced                    // 被同键 Set 覆盖
)

func (r EvictReason) String() string {
	switch r {
	case ReasonExpired:
		return "expired"
	case ReasonDeleted:
		return "deleted"
	case ReasonReplaced:
		return "replaced"
	}
	return "unknown"
}

// Options 全部为可注入函数，不依赖任何远程缓存。
type Options struct {
	// Now 时钟源，默认 time.Now。测试可注入假时钟模拟时钟跳跃。
	Now func() time.Time
	// Loader 数据源：Get 未命中时回源加载。返回 error 时不产生任何状态。
	Loader func(key string) (value any, ttl time.Duration, err error)
	// OnEvict 条目被移除时的回调（在锁外调用）。
	OnEvict func(key string, value any, reason EvictReason)
}

// entry 是缓存条目，gen 与堆节点做双向代际校验。
type entry struct {
	value    any
	gen      uint64
	expireAt int64
}

// Cache 是并发安全的 TTL 缓存。
type Cache struct {
	now     func() time.Time
	loader  func(string) (any, time.Duration, error)
	onEvict func(string, any, EvictReason)

	mu      sync.Mutex
	items   map[string]*entry
	timers  timerHeap // 主过期堆
	pending timerHeap // 压缩期间新入队的定时器缓冲
	counter uint64    // 全局单调计数器，同时充当 gen 与堆内次序

	compacting bool

	// 统计（测试与观测用）
	Compactions  int // 压缩次数
	StaleDropped int // 被丢弃的惰性失效节点数
}

// New 创建缓存。
func New(opts Options) *Cache {
	c := &Cache{
		now:     opts.Now,
		loader:  opts.Loader,
		onEvict: opts.OnEvict,
		items:   make(map[string]*entry),
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c
}

// pushLocked 入队定时事件；压缩进行中的事件进入 pending，保证不丢。
func (c *Cache) pushLocked(n timerNode) {
	if c.compacting {
		heap.Push(&c.pending, n)
		return
	}
	heap.Push(&c.timers, n)
}

// Set 写入键值并安排过期事件；同键覆盖时旧堆节点惰性失效。
func (c *Cache) Set(key string, value any, ttl time.Duration) {
	if ttl < 0 {
		ttl = 0
	}
	expireAt := c.now().Add(ttl).UnixNano()
	c.mu.Lock()
	old, replaced := c.items[key]
	c.counter++
	gen := c.counter
	c.items[key] = &entry{value: value, gen: gen, expireAt: expireAt}
	c.pushLocked(timerNode{key: key, gen: gen, expireAt: expireAt})
	c.mu.Unlock()
	if replaced && c.onEvict != nil {
		c.onEvict(key, old.value, ReasonReplaced)
	}
}

// Get 读取键值。半开语义：now >= expireAt 视为未命中并就地清理。
// 未命中且配置了 Loader 时回源加载；Loader 失败不留下任何状态。
func (c *Cache) Get(key string) (any, error) {
	now := c.now().UnixNano()
	c.mu.Lock()
	e, ok := c.items[key]
	if ok && now < e.expireAt {
		v := e.value
		c.mu.Unlock()
		return v, nil
	}
	var expired *entry
	if ok { // 已过期：就地删除，释放资源；堆节点稍后惰性丢弃
		expired = e
		delete(c.items, key)
	}
	c.mu.Unlock()
	if expired != nil && c.onEvict != nil {
		c.onEvict(key, expired.value, ReasonExpired)
	}
	if c.loader == nil {
		return nil, ErrNotFound
	}
	v, ttl, err := c.loader(key)
	if err != nil {
		return nil, err
	}
	c.Set(key, v, ttl)
	return v, nil
}

// Renew 续期：代际 +1 并追加新定时事件，旧事件惰性失效。
func (c *Cache) Renew(key string, ttl time.Duration) bool {
	expireAt := c.now().Add(ttl).UnixNano()
	c.mu.Lock()
	e, ok := c.items[key]
	if !ok {
		c.mu.Unlock()
		return false
	}
	c.counter++
	e.gen = c.counter
	e.expireAt = expireAt
	c.pushLocked(timerNode{key: key, gen: e.gen, expireAt: expireAt})
	c.mu.Unlock()
	return true
}

// RenewBatch 在单次加锁内批量续期，所有键共享同一新截止时刻。
func (c *Cache) RenewBatch(keys []string, ttl time.Duration) int {
	expireAt := c.now().Add(ttl).UnixNano()
	n := 0
	c.mu.Lock()
	for _, key := range keys {
		e, ok := c.items[key]
		if !ok {
			continue
		}
		c.counter++
		e.gen = c.counter
		e.expireAt = expireAt
		c.pushLocked(timerNode{key: key, gen: e.gen, expireAt: expireAt})
		n++
	}
	c.mu.Unlock()
	return n
}

// Delete 显式删除；对应堆节点惰性失效。
func (c *Cache) Delete(key string) bool {
	c.mu.Lock()
	e, ok := c.items[key]
	if ok {
		delete(c.items, key)
	}
	c.mu.Unlock()
	if ok && c.onEvict != nil {
		c.onEvict(key, e.value, ReasonDeleted)
	}
	return ok
}

// Expire 处理当前时钟下所有到期的定时事件。
// 只删除“当时确实过期的版本”：堆节点与条目的 gen、expireAt 双向一致才删。
func (c *Cache) Expire() int { return c.ExpireAt(c.now()) }

// ExpireAt 等价于在指定时刻执行 Expire（便于测试时钟跳跃）。
func (c *Cache) ExpireAt(t time.Time) int {
	now := t.UnixNano()
	type evicted struct {
		key   string
		value any
	}
	var fired []evicted
	c.mu.Lock()
	pop := func(h *timerHeap) {
		for len(*h) > 0 && (*h)[0].expireAt <= now {
			n := heap.Pop(h).(timerNode)
			e, ok := c.items[n.key]
			if ok && e.gen == n.gen && e.expireAt == n.expireAt {
				delete(c.items, n.key)
				fired = append(fired, evicted{n.key, e.value})
			} else {
				c.StaleDropped++ // 旧代际的定时事件：不得删除续期项
			}
		}
	}
	pop(&c.timers)
	pop(&c.pending)
	// 压缩触发条件：堆节点数超过活条目数的两倍（陈旧节点 > 活条目 + 2）
	needCompact := !c.compacting && len(c.timers) > 2*(len(c.items)+1)
	c.mu.Unlock()
	for _, ev := range fired {
		if c.onEvict != nil {
			c.onEvict(ev.key, ev.value, ReasonExpired)
		}
	}
	if needCompact {
		c.compact()
	}
	return len(fired)
}

// compact 重建过期堆，剔除陈旧节点。
// 压缩期间新定时器照常入队（进入 pending 或新主堆），
// 最后在锁内一次性原子换入合并后的新堆，不丢任何事件。
func (c *Cache) compact() {
	c.mu.Lock()
	if c.compacting {
		c.mu.Unlock()
		return
	}
	c.compacting = true
	old := c.timers
	c.timers = nil
	// 快照当前活版本（key -> gen, expireAt）。快照后发生的续期只会让
	// 被保留节点变陈旧（惰性失效兜底），而被丢弃节点对应的旧代际
	// 永远不会复活（gen 全局单调），因此过滤方向是安全的。
	type version struct {
		gen      uint64
		expireAt int64
	}
	snap := make(map[string]version, len(c.items))
	for k, e := range c.items {
		snap[k] = version{e.gen, e.expireAt}
	}
	c.mu.Unlock()

	// 无锁过滤 + 重建，不阻塞写入方。
	kept := make(timerHeap, 0, len(old))
	for _, n := range old {
		if v, ok := snap[n.key]; ok && v.gen == n.gen && v.expireAt == n.expireAt {
			kept = append(kept, n)
		}
	}
	heap.Init(&kept)

	c.mu.Lock()
	merged := make(timerHeap, 0, len(kept)+len(c.timers)+len(c.pending))
	merged = append(merged, kept...)
	merged = append(merged, c.timers...) // 防御：压缩期间主堆理论为空
	merged = append(merged, c.pending...)
	heap.Init(&merged)
	c.timers = merged
	c.pending = nil
	c.compacting = false
	c.Compactions++
	c.mu.Unlock()
}

// Len 返回活条目数。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// HeapLen 返回堆中定时事件总数（含惰性失效节点）。
func (c *Cache) HeapLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers) + len(c.pending)
}

// StartJanitor 启动后台周期过期清理，返回停止函数。
func (c *Cache) StartJanitor(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.Expire()
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}
