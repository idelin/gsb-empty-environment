package earlydata

import (
	"context"
	"sync"
	"time"
)

// ReplayStore 是 PSK binder 重放记录库。
//
// 实现必须保证 CheckAndMark 是原子的“不存在则创建”：同一 binderKey 的
// 并发调用只能有一次返回 firstObserved=true。跨节点防重放要求实现由整个
// 集群共享（例如基于 Redis SET NX PX、分布式 KV 的条件写或数据库唯一键）。
type ReplayStore interface {
	// CheckAndMark 在 binderKey 不存在或已有记录已过期时写入一条带 ttl
	// 的新记录并返回 (true, nil)；binderKey 仍在记录期内时返回 (false, nil)；
	// 存储不可用时返回非 nil 错误，此时准入器按失败关闭处理。
	CheckAndMark(ctx context.Context, binderKey []byte, ttl time.Duration) (firstObserved bool, err error)
}

const memorySweepThreshold = 256

type replayRecord struct {
	deadline time.Time
}

// MemoryStore 是基于内存 map 的 ReplayStore 参考实现，带 TTL 惰性过期。
// 它不跨进程/节点共享，仅适用于单节点部署与测试；集群部署请提供
// 访问共享存储的 ReplayStore 实现。
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]replayRecord
	now     func() time.Time
}

// NewMemoryStore 创建内存重放记录库。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		records: make(map[string]replayRecord),
		now:     time.Now,
	}
}

// CheckAndMark 实现 ReplayStore。
func (s *MemoryStore) CheckAndMark(_ context.Context, binderKey []byte, ttl time.Duration) (bool, error) {
	if len(binderKey) == 0 {
		return false, ErrEmptyBinderKey
	}
	if ttl <= 0 {
		return false, ErrInvalidTTL
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	key := string(binderKey) // 拷贝，避免调用方复用底层数组造成串键
	if existing, ok := s.records[key]; ok && now.Before(existing.deadline) {
		return false, nil
	}

	delete(s.records, key)
	s.records[key] = replayRecord{deadline: now.Add(ttl)}
	if len(s.records) > memorySweepThreshold {
		s.sweepLocked(now)
	}
	return true, nil
}

// Len 返回当前未过期的记录数，主要用于测试与监控。
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
	return len(s.records)
}

func (s *MemoryStore) sweepLocked(now time.Time) {
	for key, record := range s.records {
		if !now.Before(record.deadline) {
			delete(s.records, key)
		}
	}
}
