// Package ttlcache 实现分层时间感知（TTL/TTD）LRU 双重索引缓存。
//
// 每个对象同时被两个索引引用：
//   - 访问热度双向链表（container/list）：O(1) 提升/淘汰最冷对象；
//   - 到期小顶堆（container/heap）：按生效截止时间排序，后台收割协程
//     依据堆顶设定定时器，单次唤醒即可批量剔除所有已过期死对象。
//
// 时间语义：
//   - TTL（绝对过期）：插入时确定的硬上限 absolute = now+ttl，永不延长；
//   - TTD（滑动过期）：每次命中后将生效截止时间续期为 now+sliding，
//     但永远不会超过 absolute 硬上限。
package ttlcache

import (
	"container/heap"
	"container/list"
	"sync"
	"time"
)

// entry 是缓存中的单个对象，同时挂在 LRU 链表与到期堆上。
type entry struct {
	key       string
	value     any
	expireAt  time.Time     // 当前生效截止时间（小顶堆排序键）
	absolute  time.Time     // 绝对过期硬上限（now+TTL），不因访问而延长
	sliding   time.Duration // TTD 滑动窗口，0 表示关闭滑动续期
	lruElem   *list.Element // 在热度链表中的位置
	heapIndex int           // 在到期堆中的下标，-1 表示不在堆中（永不过期）
}

// expiryHeap 是按 expireAt 排序的小顶堆。
type expiryHeap []*entry

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].expireAt.Before(h[j].expireAt) }
func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *expiryHeap) Push(x any) {
	e := x.(*entry)
	e.heapIndex = len(*h)
	*h = append(*h, e)
}

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil // 避免堆数组持有引用造成泄漏
	e.heapIndex = -1
	*h = old[:n-1]
	return e
}

// Cache 是并发安全的 TTL/TTD LRU 双重索引缓存。
type Cache struct {
	mu       sync.Mutex
	items    map[string]*entry
	lru      *list.List // 队首 = 最热，队尾 = 最冷
	h        expiryHeap // 按生效截止时间排序的小顶堆
	capacity int        // <=0 表示不限容量

	stopCh chan struct{}
	doneCh chan struct{}
	wakeCh chan struct{}
	closed bool
}

// New 创建缓存并启动后台收割协程。capacity<=0 表示不限容量。
// 使用完毕必须调用 Close 以停止后台协程并清空索引。
func New(capacity int) *Cache {
	c := &Cache{
		items:    make(map[string]*entry),
		lru:      list.New(),
		capacity: capacity,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		wakeCh:   make(chan struct{}, 1),
	}
	go c.reaper()
	return c
}

// Set 写入对象。ttl 为绝对过期硬上限（<=0 表示永不过期）；
// sliding 为滑动过期窗口（<=0 表示关闭）。重复 key 会被覆盖。
func (c *Cache) Set(key string, value any, ttl, sliding time.Duration) {
	now := time.Now()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	if old, ok := c.items[key]; ok {
		c.removeLocked(old)
	}
	e := &entry{key: key, value: value, sliding: sliding, heapIndex: -1}
	if ttl > 0 {
		e.absolute = now.Add(ttl)
		e.expireAt = e.absolute
		if sliding > 0 {
			if s := now.Add(sliding); s.Before(e.expireAt) {
				e.expireAt = s
			}
		}
		heap.Push(&c.h, e)
	}
	e.lruElem = c.lru.PushFront(e)
	c.items[key] = e
	// 容量超限：从队尾（最冷）逐出。
	for c.capacity > 0 && c.lru.Len() > c.capacity {
		if back := c.lru.Back(); back != nil {
			c.removeLocked(back.Value.(*entry))
		}
	}
	c.mu.Unlock()
	c.wakeReaper()
}

// Get 读取对象。命中时在单次操作内完成 O(1) 热度提升，
// 并按 TTD 续期（不超过绝对硬上限）；已过期对象立即剔除并返回未命中。
func (c *Cache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return nil, false
	}
	now := time.Now()
	if e.heapIndex >= 0 && !now.Before(e.expireAt) {
		c.removeLocked(e) // 读取时判定已超时：立即剔除，返回空
		return nil, false
	}
	c.lru.MoveToFront(e.lruElem) // O(1) 热度提升
	if e.sliding > 0 && e.heapIndex >= 0 {
		newExp := now.Add(e.sliding)
		if newExp.After(e.absolute) {
			newExp = e.absolute // 滑动续期不得突破绝对硬上限
		}
		if newExp.After(e.expireAt) {
			e.expireAt = newExp
			heap.Fix(&c.h, e.heapIndex)
		}
	}
	return e.value, true
}

// Delete 显式删除对象。
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	if e, ok := c.items[key]; ok {
		c.removeLocked(e)
	}
	c.mu.Unlock()
	c.wakeReaper()
}

// Len 返回当前存活对象数。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// Close 停止后台收割协程并清空全部索引，保证内存零泄漏。
func (c *Cache) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.items = make(map[string]*entry)
	c.lru.Init()
	c.h = nil
	c.mu.Unlock()
	close(c.stopCh)
	<-c.doneCh
}

// removeLocked 将对象从 map、LRU 链表、到期堆三个索引中同时摘除。
// 调用方必须持有锁。
func (c *Cache) removeLocked(e *entry) {
	delete(c.items, e.key)
	if e.lruElem != nil {
		c.lru.Remove(e.lruElem)
		e.lruElem = nil
	}
	if e.heapIndex >= 0 {
		heap.Remove(&c.h, e.heapIndex)
	}
}

// evictExpired 批量剔除所有已过期死对象，返回剔除数量。
func (c *Cache) evictExpired() int {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for len(c.h) > 0 && !c.h[0].expireAt.After(now) {
		e := c.h[0]
		heap.Pop(&c.h)
		delete(c.items, e.key)
		if e.lruElem != nil {
			c.lru.Remove(e.lruElem)
			e.lruElem = nil
		}
		n++
	}
	return n
}

// wakeReaper 非阻塞地通知收割协程重算定时器（堆顶可能提前）。
func (c *Cache) wakeReaper() {
	select {
	case c.wakeCh <- struct{}{}:
	default:
	}
}

// reaper 是后台收割协程：按堆顶截止时间设定定时器，
// 单次唤醒批量剔除所有已过期死对象，然后重新对准新的堆顶。
func (c *Cache) reaper() {
	defer close(c.doneCh)
	var timer *time.Timer
	var timerC <-chan time.Time
	reset := func() {
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer, timerC = nil, nil
		}
		c.mu.Lock()
		if len(c.h) > 0 {
			d := time.Until(c.h[0].expireAt)
			if d <= 0 {
				d = time.Millisecond
			}
			timer = time.NewTimer(d)
			timerC = timer.C
		}
		c.mu.Unlock()
	}
	reset()
	for {
		select {
		case <-c.stopCh:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-c.wakeCh:
			reset()
		case <-timerC:
			c.evictExpired()
			reset()
		}
	}
}
