package leaseread

import "sync"

// VersionedValue 是带单调递增版本号的值，用于线性一致性比较。
type VersionedValue struct {
	Value   string
	Version uint64
}

// KVStore 是单节点上的内存版本化 KV 存储。
type KVStore struct {
	mu   sync.RWMutex
	data map[string]VersionedValue
}

func NewKVStore() *KVStore {
	return &KVStore{data: make(map[string]VersionedValue)}
}

func (s *KVStore) Get(key string) (VersionedValue, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// Put 仅在版本号更新时写入，保证副本收敛到最高版本。
func (s *KVStore) Put(key string, v VersionedValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.data[key]; !ok || v.Version > cur.Version {
		s.data[key] = v
	}
}
