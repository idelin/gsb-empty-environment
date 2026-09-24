package cacheaudit

import (
	"errors"
	"fmt"
	"sync"
)

// ErrClosed 在缓存关闭后的操作上返回。
var ErrClosed = errors.New("cacheaudit: cache closed")

// Loader 是可注入数据源：返回键的值与版本，或注入失败。
type Loader func(key string) (value string, version uint64, err error)

// Stats 是命中统计。分母口径固定为 Hits+Misses；
// 加载、淘汰、发布与失败均不进入分母。
type Stats struct {
	Hits   int64
	Misses int64
}

// Total 返回固定口径的分母。
func (s Stats) Total() int64 { return s.Hits + s.Misses }

// HitRate 返回 Hits/(Hits+Misses)；分母为 0 时返回 0。
func (s Stats) HitRate() float64 {
	if s.Total() == 0 {
		return 0
	}
	return float64(s.Hits) / float64(s.Total())
}

type entry struct {
	value     string
	version   uint64
	published bool
	source    string // 产生该版本的加载事件，形如 "load:<seq>"
}

// Cache 记录命中、失效、加载、淘汰与发布事件。
// 未发布的加载不会被记为命中。所有外部依赖均可注入。
type Cache struct {
	mu      sync.Mutex
	entries map[string]*entry
	log     *Log
	load    Loader
	now     func() int64
	cleanup func() error // 可注入的资源释放钩子
	hits    int64
	misses  int64
	closed  bool
}

// NewCache 创建缓存。logCap 为事件日志容量（有界环形）；
// loader 为数据源；now 为时钟；cleanup 为关闭时调用一次的释放钩子（可为 nil）。
func NewCache(logCap int, loader Loader, now func() int64, cleanup func() error) *Cache {
	return &Cache{
		entries: make(map[string]*entry),
		log:     NewLog(logCap, now),
		load:    loader,
		now:     now,
		cleanup: cleanup,
	}
}

// Get 读取键。仅当对应版本已发布时记为命中；
// 未命中（含未发布、已淘汰）记为失效并触发加载，加载失败按注入错误返回。
func (c *Cache) Get(key string) (string, uint64, error) {
	start := c.now()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", 0, ErrClosed
	}
	if ent, ok := c.entries[key]; ok && ent.published {
		c.hits++
		c.log.Append(KindHit, key, ent.version, ent.source, "", start, c.now())
		val, ver := ent.value, ent.version
		c.mu.Unlock()
		return val, ver, nil
	}
	c.misses++
	c.log.Append(KindMiss, key, 0, "", "", start, c.now())
	c.mu.Unlock()

	val, ver, err := c.load(key)
	end := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", 0, ErrClosed
	}
	if err != nil {
		c.log.Append(KindLoad, key, 0, "loader", err.Error(), start, end)
		return "", 0, err
	}
	ev := c.log.Append(KindLoad, key, ver, "loader", "", start, end)
	c.entries[key] = &entry{value: val, version: ver, source: fmt.Sprintf("load:%d", ev.Seq)}
	return val, ver, nil
}

// Publish 发布已加载版本，使其对后续命中可见。返回是否实际发布。
func (c *Cache) Publish(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	ent, ok := c.entries[key]
	if !ok || ent.published {
		return false
	}
	ent.published = true
	now := c.now()
	c.log.Append(KindPublish, key, ent.version, ent.source, "", now, now)
	return true
}

// Evict 淘汰键并记录事件。返回是否实际淘汰了缓存项。
func (c *Cache) Evict(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	if _, ok := c.entries[key]; !ok {
		return false
	}
	delete(c.entries, key)
	now := c.now()
	c.log.Append(KindEvict, key, 0, "", "", now, now)
	return true
}

// Stats 返回命中统计快照。
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{Hits: c.hits, Misses: c.misses}
}

// Log 返回事件日志（有界）。
func (c *Cache) Log() *Log { return c.log }

// Close 释放资源：注入的 cleanup 恰好调用一次，重复关闭幂等。
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.cleanup != nil {
		return c.cleanup()
	}
	return nil
}
