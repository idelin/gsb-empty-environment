// Package ttlru 实现分层时间感知（TTL/TTD）LRU 双重索引缓存。
//
// 每个条目同时挂在两条索引上：
//   - 访问热度双向链表：Get/Set 命中时 O(1) 提升到队首；
//   - 到期时间小顶堆：按生效到期时间（绝对过期与滑动过期取较早者）排序，
//     堆顶即最早死亡的对象，后台 janitor 与写路径可 O(k log n) 批量剔除
//     k 个已过期死对象。
package ttlru

import (
	"container/heap"
	"container/list"
	"sync"
	"time"
)

// Stats 是缓存运行期统计信息。
type Stats struct {
	Hits    uint64 // 命中次数
	Misses  uint64 // 未命中次数（含过期即空）
	Expired uint64 // 读取路径上发现并已剔除的过期条目数
	Evicted uint64 // 因容量超限被 LRU 淘汰的条目数
	Reaped  uint64 // 后台/写路径批量回收的过期条目数
}

// Cache 是并发安全的 TTL/TTD LRU 缓存。
type Cache struct {
	mu        sync.Mutex
	items     map[string]*entry
	lru       *list.List // 队首 = 最热，队尾 = 最冷
	exp       expireHeap // 按生效到期时间排序的小顶堆
	capacity  int        // 最大条目数，<=0 表示不限
	janitor   time.Duration
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	stats     Stats
	now       func() time.Time // 可注入时钟，便于测试
}

// Option 是 Cache 的配置项。
type Option func(*Cache)

// WithCapacity 设置最大条目数，超限时按 LRU 从队尾淘汰。<=0 表示不限容量。
func WithCapacity(n int) Option {
	return func(c *Cache) { c.capacity = n }
}

// WithJanitor 设置后台批量回收周期。<=0 表示不启动后台回收
// （过期条目仍会在读取时即时剔除、在写入时批量剔除）。
func WithJanitor(interval time.Duration) Option {
	return func(c *Cache) { c.janitor = interval }
}

// New 创建缓存。若配置了 janitor 周期，会启动一个后台 goroutine，
// 使用 Close 停止。
func New(opts ...Option) *Cache {
	c := &Cache{
		items: make(map[string]*entry),
		lru:   list.New(),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		now:   time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.janitor > 0 {
		go c.janitorLoop()
	} else {
		close(c.done)
	}
	return c
}

// Set 写入条目。ttl 为绝对过期时间（<=0 表示无绝对过期），
// idle 为滑动过期窗口（<=0 表示不启用滑动过期）；两者同时设置时取较早者。
// 写入路径会顺带批量剔除已过期死对象。
func (c *Cache) Set(key string, value interface{}, ttl, idle time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.reapExpiredLocked(now) // 写路径批量回收

	if e, ok := c.items[key]; ok {
		e.value = value
		e.lastAccess = now
		e.expireAt = expireAt(now, ttl)
		e.idle = idle
		c.lru.MoveToFront(e.elem) // O(1) 热度提升
		heap.Fix(&c.exp, e.heapIndex)
	} else {
		e := &entry{
			key:        key,
			value:      value,
			expireAt:   expireAt(now, ttl),
			idle:       idle,
			lastAccess: now,
		}
		e.elem = c.lru.PushFront(e)
		heap.Push(&c.exp, e)
		c.items[key] = e
	}
	c.evictLocked()
}

// Get 读取条目。过期条目立即返回空并同步剔除；
// 命中时 O(1) 提升热度，并刷新滑动过期窗口。
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.items[key]
	if !ok {
		c.stats.Misses++
		return nil, false
	}
	now := c.now()
	if e.expired(now) {
		c.removeLocked(e)
		c.stats.Expired++
		c.stats.Misses++
		return nil, false
	}
	c.lru.MoveToFront(e.elem) // O(1) 热度提升
	if e.idle > 0 {
		e.lastAccess = now // 滑动过期：续期并修正堆序
		heap.Fix(&c.exp, e.heapIndex)
	}
	c.stats.Hits++
	return e.value, true
}

// Delete 删除条目。
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.removeLocked(e)
	}
}

// Len 返回当前存活（含尚未被回收的过期）条目数。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// ReapExpired 批量剔除所有已过期死对象，返回剔除数量。
// 后台 janitor 定期调用，也可手动调用。
func (c *Cache) ReapExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reapExpiredLocked(c.now())
}

// Stats 返回统计信息快照。
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// Close 停止后台 janitor 并等待其退出。可重复调用。
func (c *Cache) Close() {
	c.closeOnce.Do(func() {
		if c.janitor > 0 {
			close(c.stop)
		}
		<-c.done
	})
}

func (c *Cache) janitorLoop() {
	t := time.NewTicker(c.janitor)
	defer func() {
		t.Stop()
		close(c.done)
	}()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			c.ReapExpired()
		}
	}
}

// reapExpiredLocked 从堆顶连续弹出已过期条目，调用方须持锁。
func (c *Cache) reapExpiredLocked(now time.Time) int {
	n := 0
	for c.exp.Len() > 0 {
		top := c.exp[0]
		d := top.deadline()
		if d.IsZero() || now.Before(d) {
			break // 堆顶未过期，其余更不会过期
		}
		c.removeLocked(top)
		c.stats.Reaped++
		n++
	}
	return n
}

// evictLocked 按 LRU 从队尾淘汰直至不超容量，调用方须持锁。
func (c *Cache) evictLocked() {
	for c.capacity > 0 && len(c.items) > c.capacity {
		back := c.lru.Back()
		if back == nil {
			return
		}
		c.removeLocked(back.Value.(*entry))
		c.stats.Evicted++
	}
}

// removeLocked 从 map、LRU 链表、小顶堆三处同时摘除条目，调用方须持锁。
func (c *Cache) removeLocked(e *entry) {
	delete(c.items, e.key)
	c.lru.Remove(e.elem)
	heap.Remove(&c.exp, e.heapIndex)
}

func expireAt(now time.Time, ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return now.Add(ttl)
}
