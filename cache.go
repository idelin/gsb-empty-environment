package rulecache

import "sync"

// CacheKey 唯一标识一条规则结果缓存项。
type CacheKey string

type entry struct {
	value any
	deps  map[DimKey]struct{}
}

// Stats 缓存运行统计。
type Stats struct {
	Size          int // 当前缓存项数量
	Hits          int // 命中次数
	Misses        int // 未命中次数
	Invalidations int // 被精准剔除的缓存项数量
}

// Cache 规则结果缓存，维护 维度键 -> 缓存键 的精确反向索引。
type Cache struct {
	mu      sync.RWMutex
	entries map[CacheKey]entry
	index   map[DimKey]map[CacheKey]struct{} // 反向索引：维度键 -> 依赖它的缓存键集合
	stats   Stats
}

func NewCache() *Cache {
	return &Cache{
		entries: make(map[CacheKey]entry),
		index:   make(map[DimKey]map[CacheKey]struct{}),
	}
}

func (c *Cache) Get(k CacheKey) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if ok {
		c.stats.Hits++
	} else {
		c.stats.Misses++
	}
	return e.value, ok
}

// Put 写入缓存项，并登记其依赖的维度键到反向索引。
func (c *Cache) Put(k CacheKey, v any, deps map[DimKey]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[k]; ok {
		c.removeFromIndex(k, old.deps)
	}
	c.entries[k] = entry{value: v, deps: deps}
	for d := range deps {
		set, ok := c.index[d]
		if !ok {
			set = make(map[CacheKey]struct{})
			c.index[d] = set
		}
		set[k] = struct{}{}
	}
	c.stats.Size = len(c.entries)
}

// InvalidateByDim 精准剔除所有依赖维度 d 的缓存项，并清理反向索引。
// 返回被剔除的缓存项数量；不依赖 d 的缓存完全不受影响。
func (c *Cache) InvalidateByDim(d DimKey) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	affected := c.index[d]
	for k := range affected {
		e, ok := c.entries[k]
		if !ok {
			continue
		}
		delete(c.entries, k)
		c.removeFromIndex(k, e.deps)
		c.stats.Invalidations++
	}
	c.stats.Size = len(c.entries)
	return len(affected)
}

func (c *Cache) removeFromIndex(k CacheKey, deps map[DimKey]struct{}) {
	for d := range deps {
		if set, ok := c.index[d]; ok {
			delete(set, k)
			if len(set) == 0 {
				delete(c.index, d)
			}
		}
	}
}

func (c *Cache) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.stats
	s.Size = len(c.entries)
	return s
}
