package main

import (
	"container/list"
	"sync"
	"time"
)

// entry 缓存条目，size 为值的真实字节数。
type entry struct {
	key  string
	size int64
}

// Cache 是基于 LRU 驱逐策略、按字节容量限制的缓存。
// 内存按页分配（模拟真实分配器），用于计算内存碎片率：
// 碎片率 = (已分配页字节 - 实际使用字节) / 已分配页字节。
type Cache struct {
	mu        sync.Mutex
	capacity  int64 // 容量上限（字节）
	used      int64 // 实际使用字节
	allocated int64 // 按页对齐后的已分配字节
	pageSize  int64
	ll        *list.List
	items     map[string]*list.Element
	metrics   *Metrics
}

func NewCache(capacityBytes, pageSize int64, metrics *Metrics) *Cache {
	if pageSize <= 0 {
		pageSize = 4096
	}
	return &Cache{
		capacity: capacityBytes,
		pageSize: pageSize,
		ll:       list.New(),
		items:    make(map[string]*list.Element),
		metrics:  metrics,
	}
}

func roundUp(size, page int64) int64 {
	return (size + page - 1) / page * page
}

// Get 查询缓存，命中返回 true。全程打点记录命中/未命中与微秒级延迟。
func (c *Cache) Get(key string) bool {
	start := time.Now()
	c.mu.Lock()
	el, ok := c.items[key]
	if ok {
		c.ll.MoveToFront(el)
	}
	c.mu.Unlock()
	if ok {
		c.metrics.RecordHit(start)
	} else {
		c.metrics.RecordMiss(start)
	}
	return ok
}

// Set 写入缓存；容量不足时按 LRU 顺序驱逐，并累计驱逐计数。
func (c *Cache) Set(key string, size int64) {
	start := time.Now()
	c.mu.Lock()
	if size > c.capacity {
		// 超过整体容量的条目不入缓存
		c.mu.Unlock()
		c.metrics.RecordSet(start)
		return
	}
	if el, ok := c.items[key]; ok {
		e := el.Value.(*entry)
		c.used += size - e.size
		c.allocated += roundUp(size, c.pageSize) - roundUp(e.size, c.pageSize)
		e.size = size
		c.ll.MoveToFront(el)
		c.mu.Unlock()
		c.metrics.RecordSet(start)
		return
	}
	for c.used+size > c.capacity && c.ll.Len() > 0 {
		back := c.ll.Back()
		victim := back.Value.(*entry)
		c.ll.Remove(back)
		delete(c.items, victim.key)
		c.used -= victim.size
		c.allocated -= roundUp(victim.size, c.pageSize)
		c.metrics.RecordEviction()
	}
	c.ll.PushFront(&entry{key: key, size: size})
	c.items[key] = c.ll.Front()
	c.used += size
	c.allocated += roundUp(size, c.pageSize)
	c.mu.Unlock()
	c.metrics.RecordSet(start)
}

// Fragmentation 返回内存碎片率 ∈ [0,1)。
func (c *Cache) Fragmentation() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.allocated == 0 {
		return 0
	}
	return float64(c.allocated-c.used) / float64(c.allocated)
}

// MemStats 返回 (used, allocated, capacity) 字节数。
func (c *Cache) MemStats() (used, allocated, capacity int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used, c.allocated, c.capacity
}

// Len 返回当前条目数。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
