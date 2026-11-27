package samlsec

import (
	"sync"
	"time"
)

// MemoryReplayCache 是进程内单次断言 ID 防重放实现。多实例部署应换成
// 共享存储实现 ReplayCache 接口。
type MemoryReplayCache struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	ttl    time.Duration
	lastGC time.Time
}

// NewMemoryReplayCache 创建缓存，ttl 小于等于 0 时默认 2 小时。
func NewMemoryReplayCache(ttl time.Duration) *MemoryReplayCache {
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	return &MemoryReplayCache{
		seen:   map[string]time.Time{},
		ttl:    ttl,
		lastGC: time.Now(),
	}
}

// SeenBefore 实现 ReplayCache。
func (c *MemoryReplayCache) SeenBefore(id string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if now.Sub(c.lastGC) > c.ttl/2 {
		for k, t := range c.seen {
			if now.Sub(t) > c.ttl {
				delete(c.seen, k)
			}
		}
		c.lastGC = now
	}

	if _, ok := c.seen[id]; ok {
		return true, nil
	}
	c.seen[id] = now
	return false, nil
}
