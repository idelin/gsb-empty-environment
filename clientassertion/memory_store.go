package clientassertion

import (
	"context"
	"sync"
	"time"
)

// MemoryStore 是进程内的 jti 防重放存储，按 client_id 分区。
// 多实例部署时应替换为共享存储（Redis 等）并保证 CheckAndStore 原子性。
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]map[string]time.Time
	now     func() time.Time
}

// NewMemoryStore 创建空的内存防重放存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		entries: make(map[string]map[string]time.Time),
		now:     time.Now,
	}
}

// CheckAndStore 在单把互斥锁内完成“检查 + 登记”，保证原子性。
// 每次调用顺带清理本 client 分区内已过期的 jti，避免无界增长。
func (s *MemoryStore) CheckAndStore(_ context.Context, clientID, jti string, exp time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	seen, ok := s.entries[clientID]
	if !ok {
		seen = make(map[string]time.Time)
		s.entries[clientID] = seen
	}

	for existingJTI, existingExp := range seen {
		if !now.Before(existingExp) {
			delete(seen, existingJTI)
		}
	}

	if existingExp, duplicated := seen[jti]; duplicated && now.Before(existingExp) {
		return ErrReplayedJTI
	}
	seen[jti] = exp
	return nil
}
