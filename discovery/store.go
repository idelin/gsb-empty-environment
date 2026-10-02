package discovery

import "sync"

// Store 是线程安全的成员表，按心跳时间做最后写入者获胜（LWW）合并。
type Store struct {
	mu      sync.RWMutex
	members map[string]NodeMeta
}

func NewStore() *Store {
	return &Store{members: make(map[string]NodeMeta)}
}

// Upsert 写入或按心跳时间刷新单个成员。
func (s *Store) Upsert(m NodeMeta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.members[m.ID]; !ok || m.Heartbeat > old.Heartbeat {
		s.members[m.ID] = m
	}
}

// Merge 批量合并成员快照，返回新发现的成员数。
func (s *Store) Merge(metas []NodeMeta) int {
	added := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range metas {
		if old, ok := s.members[m.ID]; !ok {
			s.members[m.ID] = m
			added++
		} else if m.Heartbeat > old.Heartbeat {
			s.members[m.ID] = m
		}
	}
	return added
}

// Snapshot 返回当前全部成员快照。
func (s *Store) Snapshot() []NodeMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]NodeMeta, 0, len(s.members))
	for _, m := range s.members {
		out = append(out, m)
	}
	return out
}

// Size 返回已知成员数量。
func (s *Store) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.members)
}
