package jar

import (
	"sync"
	"time"
)

// ReplayCache 抽象 jti 防重放存储。
// Reserve 必须是原子的“检查并占用”：首次出现的 (clientID, jti) 返回 true，
// 重复出现返回 false。宿主可用 Redis SET NX 等实现分布式版本。
type ReplayCache interface {
	Reserve(clientID, jti string, expiresAt time.Time) bool
}

// replayEntry 记录一条已使用 jti 及其过期时刻。
type replayEntry struct {
	expiresAt time.Time
}

// MemoryReplayCache 是进程内、线程安全的 ReplayCache，
// 采用懒清理策略：每次写入时顺带淘汰已过期条目。
type MemoryReplayCache struct {
	mu      sync.Mutex
	entries map[string]replayEntry
	now     func() time.Time
}

// NewMemoryReplayCache 创建内存防重放缓存。
func NewMemoryReplayCache() *MemoryReplayCache {
	return &MemoryReplayCache{
		entries: make(map[string]replayEntry),
		now:     time.Now,
	}
}

func replayKey(clientID, jti string) string { return clientID + "\x00" + jti }

// Reserve 原子检查并占用 jti。过期条目的占用视为可回收。
func (c *MemoryReplayCache) Reserve(clientID, jti string, expiresAt time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := replayKey(clientID, jti)
	now := c.now()
	if existing, ok := c.entries[key]; ok && existing.expiresAt.After(now) {
		return false
	}
	c.entries[key] = replayEntry{expiresAt: expiresAt}
	if len(c.entries) > 1024 {
		c.sweepLocked(now)
	}
	return true
}

func (c *MemoryReplayCache) sweepLocked(now time.Time) {
	for key, entry := range c.entries {
		if !entry.expiresAt.After(now) {
			delete(c.entries, key)
		}
	}
}
