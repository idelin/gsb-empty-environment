package rulecache

import "sync"

// DimKey 唯一标识一条外部维度记录，例如 "tenant1:user:U1:member"。
type DimKey string

// DimensionStore 模拟外部维度表，并在维度更新时通知订阅者。
type DimensionStore struct {
	mu        sync.RWMutex
	data      map[DimKey]map[string]string
	listeners []func(DimKey)
}

func NewDimensionStore() *DimensionStore {
	return &DimensionStore{data: make(map[DimKey]map[string]string)}
}

// Set 写入/更新一条维度记录，并触发更新通知。
func (s *DimensionStore) Set(key DimKey, attrs map[string]string) {
	s.mu.Lock()
	cp := make(map[string]string, len(attrs))
	for k, v := range attrs {
		cp[k] = v
	}
	s.data[key] = cp
	listeners := append([]func(DimKey){}, s.listeners...)
	s.mu.Unlock()
	for _, l := range listeners {
		l(key)
	}
}

// Get 读取维度记录的某个属性。
func (s *DimensionStore) Get(key DimKey, attr string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	attrs, ok := s.data[key]
	if !ok {
		return "", false
	}
	v, ok := attrs[attr]
	return v, ok
}

// OnUpdate 订阅维度更新事件。
func (s *DimensionStore) OnUpdate(l func(DimKey)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, l)
}
