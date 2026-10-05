package rebuild

import "sync"

// Record 是一条带版本的键值记录，Version 即该键当前的版本。
type Record struct {
	Key     string
	Value   string
	Version uint64
}

// Event 是一条变更事件。Seq 是源库全局递增版本，同时作为该键的写入版本，
// 用于按每键版本消重。
type Event struct {
	Seq     uint64
	Key     string
	Value   string
	Deleted bool
}

// Source 是主存储：每次写入分配单调递增的全局版本，并维护追加式变更日志。
type Source struct {
	mu        sync.Mutex
	data      map[string]Record
	version   uint64
	log       []Event
	listeners []func(Event)
}

func NewSource() *Source {
	return &Source{data: make(map[string]Record)}
}

// OnChange 注册变更监听器（如旧索引的既有同步管线、重建器的待确认跟踪）。
func (s *Source) OnChange(fn func(Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, fn)
}

func (s *Source) Put(key, value string) Event {
	s.mu.Lock()
	s.version++
	s.data[key] = Record{Key: key, Value: value, Version: s.version}
	ev := Event{Seq: s.version, Key: key, Value: value}
	s.log = append(s.log, ev)
	listeners := append([]func(Event){}, s.listeners...)
	s.mu.Unlock()
	for _, fn := range listeners {
		fn(ev)
	}
	return ev
}

func (s *Source) Delete(key string) Event {
	s.mu.Lock()
	s.version++
	delete(s.data, key)
	ev := Event{Seq: s.version, Key: key, Deleted: true}
	s.log = append(s.log, ev)
	listeners := append([]func(Event){}, s.listeners...)
	s.mu.Unlock()
	for _, fn := range listeners {
		fn(ev)
	}
	return ev
}

// Snapshot 返回一致性快照及快照时刻的版本（冻结版本）。
func (s *Source) Snapshot() (map[string]Record, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[string]Record, len(s.data))
	for k, v := range s.data {
		cp[k] = v
	}
	return cp, s.version
}

// EventsSince 按序返回所有 Seq > version 的变更事件。
func (s *Source) EventsSince(version uint64) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Event
	for _, ev := range s.log {
		if ev.Seq > version {
			out = append(out, ev)
		}
	}
	return out
}

// Version 返回源库当前水位（最高已分配版本）。
func (s *Source) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Scan 全量扫描存活记录，作为校验参考数据。
func (s *Source) Scan() map[string]Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[string]Record, len(s.data))
	for k, v := range s.data {
		cp[k] = v
	}
	return cp
}
