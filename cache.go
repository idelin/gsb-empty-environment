// Package expcache 实现一个基于最小堆调度的过期缓存。
//
// 核心不变量：
//   - 每次 Set/Renew 使该键代际 gen 单调递增，并向堆中推入一个新的定时事件；
//     旧定时事件成为惰性失效的陈旧节点，弹出时被丢弃，绝不删除续期后的条目。
//   - 过期判定为半开语义：条目在 [设置时刻, deadline) 内有效，
//     时刻恰好等于 deadline 时即失效。
//   - 堆节点与缓存条目做双向代际校验（gen 与 deadline 同时匹配才删除）。
//   - 堆定期压缩：压缩期间新定时器进入 pending 队列，新堆原子交换后重放，
//     不丢事件。设 L 为存活条目数、S 为压缩余量（默认 64），堆长度恒满足
//     H <= 2L + S + P（P 为压缩窗口内并发入队数），压缩后 H <= L。
//     同一键续期 K 次，堆不会无限增长：陈旧节点占用上界为 2L+S+P，
//     摊还压缩代价为每次续期 O(1)（每 Ω(H) 次入队触发一次 O(H) 压缩）。
//   - 时钟回拨策略：截止时间是以注入时钟表示的绝对时刻；时钟回拨只会让
//     条目活得更久，绝不会提前删除任何未到期版本。时钟前跳时，Sweep 按
//     当前时刻批量弹出，仅删除当时确实过期的当前版本。
package expcache

import (
	"container/heap"
	"errors"
	"sync"
	"time"
)

// ErrClosed 在缓存关闭后继续写入时返回。
var ErrClosed = errors.New("expcache: cache is closed")

const defaultCompactSlack = 64

// Options 注入数据源、时钟与失败回调；全部为零值时缓存仍可工作
// （使用系统时钟、无加载器、丢弃事件）。
type Options[V any] struct {
	// Now 返回当前时刻。注入假时钟可确定性测试时钟跳跃与回拨。
	Now func() time.Time
	// Load 是数据源：GetOrLoad 未命中时调用。错误经 OnError 上报。
	Load func(key string) (V, error)
	// OnExpire 在条目确实过期被删除时触发（在锁外调用）。
	OnExpire func(key string, gen uint64, value V)
	// OnError 上报数据源等失败（在锁外调用）。
	OnError func(err error)
	// JanitorInterval > 0 时启动后台清扫协程；Close 释放它。
	JanitorInterval time.Duration
	// CompactSlack 是压缩触发余量 S；<=0 时使用默认值 64。
	CompactSlack int

	// onCompactBuild 仅供测试：在压缩构建新堆期间（锁外）调用，
	// 用于确定性地构造“压缩期间并发入队”的场景。
	onCompactBuild func()
}

type entry[V any] struct {
	value    V
	gen      uint64
	deadline time.Time
}

type expireEvent[V any] struct {
	key   string
	gen   uint64
	value V
}

// Cache 是并发安全的过期缓存。
type Cache[V any] struct {
	mu           sync.Mutex
	now          func() time.Time
	load         func(string) (V, error)
	onExpire     func(string, uint64, V)
	onError      func(error)
	compactSlack int

	entries map[string]entry[V]
	gens    map[string]uint64 // 键级代际计数器，条目删除后仍单调递增

	heap       timerHeap
	pending    []timer // 压缩期间入队的定时器
	compacting bool

	closed  bool
	stopCh  chan struct{}
	doneCh  chan struct{}
	janitor bool

	onCompactBuild func() // 仅测试注入
}

// New 创建缓存；opts 为 nil 时使用全部默认行为。
func New[V any](opts *Options[V]) *Cache[V] {
	c := &Cache[V]{
		now:          time.Now,
		entries:      make(map[string]entry[V]),
		gens:         make(map[string]uint64),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
		compactSlack: defaultCompactSlack,
	}
	if opts != nil {
		if opts.Now != nil {
			c.now = opts.Now
		}
		c.load = opts.Load
		c.onExpire = opts.OnExpire
		c.onError = opts.OnError
		if opts.CompactSlack > 0 {
			c.compactSlack = opts.CompactSlack
		}
		c.onCompactBuild = opts.onCompactBuild
		if opts.JanitorInterval > 0 {
			c.janitor = true
			go c.runJanitor(opts.JanitorInterval)
		} else {
			close(c.doneCh)
		}
	} else {
		close(c.doneCh)
	}
	return c
}

// Set 写入键值并设置 TTL，代际递增，旧定时事件随之惰性失效。
func (c *Cache[V]) Set(key string, value V, ttl time.Duration) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.setLocked(key, value, ttl)
	c.mu.Unlock()
	c.maybeCompact()
	return nil
}

func (c *Cache[V]) setLocked(key string, value V, ttl time.Duration) {
	c.gens[key]++
	gen := c.gens[key]
	deadline := c.now().Add(ttl)
	c.entries[key] = entry[V]{value: value, gen: gen, deadline: deadline}
	c.pushTimerLocked(timer{key: key, gen: gen, deadline: deadline})
}

// Renew 续期一个仍存活的键；键不存在或已过期时返回 false。
func (c *Cache[V]) Renew(key string, ttl time.Duration) bool {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}
	e, ok := c.entries[key]
	if !ok || !c.liveLocked(e) {
		c.mu.Unlock()
		return false
	}
	c.setLocked(key, e.value, ttl)
	c.mu.Unlock()
	c.maybeCompact()
	return true
}

// RenewMany 批量续期，返回成功续期的键数。
// 只续期调用时刻仍存活的版本；已过期的版本不受影响。
func (c *Cache[V]) RenewMany(keys []string, ttl time.Duration) int {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0
	}
	n := 0
	for _, key := range keys {
		e, ok := c.entries[key]
		if !ok || !c.liveLocked(e) {
			continue
		}
		c.setLocked(key, e.value, ttl)
		n++
	}
	c.mu.Unlock()
	c.maybeCompact()
	return n
}

// Get 读取键值。半开语义：now == deadline 时视为过期。
// 读到已过期条目时惰性删除并触发过期事件。
func (c *Cache[V]) Get(key string) (V, bool) {
	var zero V
	var ev *expireEvent[V]
	c.mu.Lock()
	e, ok := c.entries[key]
	switch {
	case !ok:
		// miss
	case c.liveLocked(e):
		c.mu.Unlock()
		return e.value, true
	default:
		delete(c.entries, key)
		ev = &expireEvent[V]{key: key, gen: e.gen, value: e.value}
	}
	c.mu.Unlock()
	if ev != nil {
		c.fireExpire([]expireEvent[V]{*ev})
	}
	return zero, false
}

// GetOrLoad 命中即返回；未命中时调用注入的数据源加载并写回。
// 数据源失败经 OnError 上报并返回错误。
func (c *Cache[V]) GetOrLoad(key string, ttl time.Duration) (V, error) {
	var zero V
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	if c.load == nil {
		return zero, errors.New("expcache: miss and no loader configured")
	}
	v, err := c.load(key)
	if err != nil {
		if c.onError != nil {
			c.onError(err)
		}
		return zero, err
	}
	if err := c.Set(key, v, ttl); err != nil {
		return zero, err
	}
	return v, nil
}

// Delete 显式删除键；其堆节点成为陈旧节点，之后被惰性丢弃。
func (c *Cache[V]) Delete(key string) bool {
	c.mu.Lock()
	_, ok := c.entries[key]
	delete(c.entries, key)
	c.mu.Unlock()
	return ok
}

// Sweep 弹出并处理所有在当前时刻到期的堆节点。
// 仅删除通过双向代际校验的条目；返回删除条数。时钟前跳后调用一次
// 即可批量清理，且只删除当时确实过期的版本。
func (c *Cache[V]) Sweep() int {
	c.mu.Lock()
	events := c.expireLocked(c.now())
	c.mu.Unlock()
	c.fireExpire(events)
	c.maybeCompact()
	return len(events)
}

// expireLocked 是过期处理核心：弹出所有 deadline <= now 的节点，
// 做堆节点 -> 条目的双向校验（gen 与 deadline 均一致才删除）。
func (c *Cache[V]) expireLocked(now time.Time) []expireEvent[V] {
	var events []expireEvent[V]
	for len(c.heap) > 0 && !c.heap[0].deadline.After(now) {
		t := heap.Pop(&c.heap).(timer)
		e, ok := c.entries[t.key]
		if !ok {
			continue // 条目已被删除/过期，节点陈旧
		}
		if e.gen != t.gen || !e.deadline.Equal(t.deadline) {
			continue // 续期产生过新版本，旧定时事件不得删除续期项
		}
		delete(c.entries, t.key)
		events = append(events, expireEvent[V]{key: t.key, gen: t.gen, value: e.value})
	}
	return events
}

// Compact 压缩堆：丢弃全部陈旧节点。
// 构建新堆期间不持锁，新定时器进入 pending 队列；交换在锁内原子完成，
// 随后重放 pending，不丢任何事件。压缩后堆长度 <= 存活条目数。
func (c *Cache[V]) Compact() {
	c.mu.Lock()
	if c.compacting {
		c.mu.Unlock()
		return
	}
	c.compacting = true
	snapshot := make([]timer, 0, len(c.entries))
	for key, e := range c.entries {
		snapshot = append(snapshot, timer{key: key, gen: e.gen, deadline: e.deadline})
	}
	hook := c.onCompactBuild
	c.mu.Unlock()

	if hook != nil {
		hook() // 测试钩子：此窗口内的入队进入 pending
	}

	fresh := make(timerHeap, 0, len(snapshot))
	fresh = append(fresh, snapshot...)
	heap.Init(&fresh)

	c.mu.Lock()
	c.heap = fresh // 原子交换新堆
	for _, t := range c.pending {
		heap.Push(&c.heap, t)
	}
	c.pending = nil
	c.compacting = false
	c.mu.Unlock()
}

// Len 返回存活条目数：先按当前时刻做一次过期处理再计数。
func (c *Cache[V]) Len() int {
	c.Sweep()
	c.mu.Lock()
	n := len(c.entries)
	c.mu.Unlock()
	return n
}

// HeapLen 返回堆中节点数（含惰性失效的陈旧节点），用于观测压缩上界。
func (c *Cache[V]) HeapLen() int {
	c.mu.Lock()
	n := len(c.heap) + len(c.pending)
	c.mu.Unlock()
	return n
}

// Close 释放后台清扫协程。幂等；关闭后写入返回 ErrClosed。
func (c *Cache[V]) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	if c.janitor {
		close(c.stopCh)
		<-c.doneCh
	}
	return nil
}

// Done 返回在后台协程退出后关闭的通道，供测试验证资源释放。
func (c *Cache[V]) Done() <-chan struct{} { return c.doneCh }

func (c *Cache[V]) runJanitor(interval time.Duration) {
	defer close(c.doneCh)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-tick.C:
			c.Sweep()
		}
	}
}

// liveLocked 实现半开语义：仅当 now < deadline 时存活。
func (c *Cache[V]) liveLocked(e entry[V]) bool {
	return c.now().Before(e.deadline)
}

func (c *Cache[V]) pushTimerLocked(t timer) {
	if c.compacting {
		c.pending = append(c.pending, t)
		return
	}
	heap.Push(&c.heap, t)
}

// maybeCompact 在堆长度超过 2L+S 时触发压缩，保证陈旧节点占用有界。
func (c *Cache[V]) maybeCompact() {
	c.mu.Lock()
	over := len(c.heap) > 2*len(c.entries)+c.compactSlack
	busy := c.compacting
	c.mu.Unlock()
	if over && !busy {
		c.Compact()
	}
}

func (c *Cache[V]) fireExpire(events []expireEvent[V]) {
	if c.onExpire == nil {
		return
	}
	for _, ev := range events {
		c.onExpire(ev.key, ev.gen, ev.value)
	}
}
