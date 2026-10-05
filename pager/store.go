package pager

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Record 是一条记录：唯一编号 + 字段集合。
type Record struct {
	ID     string
	Fields map[string]Value
}

// Get 返回字段值；字段不存在时视为对应类型的空值由调用方决定，
// 这里统一返回 Null(KindString) 之外的通用空值。
func (r Record) Get(name string) (Value, bool) {
	v, ok := r.Fields[name]
	return v, ok
}

// entry 是快照中的一行：物化的排序键 + 记录副本。
type entry struct {
	keys []Value
	rec  Record
}

// Snapshot 是某一时刻、按某一排序规则物化的不可变有序视图。
// 快照建立后，对 Store 的增删改不影响快照内容。
type Snapshot struct {
	id       string
	sortHash string
	entries  []entry
}

// ID 返回快照编号。
func (s *Snapshot) ID() string { return s.id }

// Len 返回快照中的行数。
func (s *Snapshot) Len() int { return len(s.entries) }

// Store 是并发安全的内存记录库，并管理分页快照的生命周期。
type Store struct {
	mu        sync.RWMutex
	records   map[string]Record
	snapshots map[string]*Snapshot
	seq       uint64
}

// NewStore 创建空记录库。
func NewStore() *Store {
	return &Store{
		records:   make(map[string]Record),
		snapshots: make(map[string]*Snapshot),
	}
}

// Put 插入或更新一条记录。
func (s *Store) Put(rec Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.ID] = cloneRecord(rec)
}

// Delete 删除一条记录；不存在的编号视为成功。
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, id)
}

// Get 读取当前记录。
func (s *Store) Get(id string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[id]
	return cloneRecord(rec), ok
}

// newSnapshot 按排序规则物化当前全部记录，生成不可变有序快照。
func (s *Store) newSnapshot(spec SortSpec) *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := make([]entry, 0, len(s.records))
	for _, rec := range s.records {
		keys := make([]Value, len(spec))
		for i, f := range spec {
			v, ok := rec.Fields[f.Name]
			if !ok {
				v = Value{Null: true}
			}
			keys[i] = v
		}
		entries = append(entries, entry{keys: keys, rec: cloneRecord(rec)})
	}

	less := func(i, j int) bool {
		c := spec.compareKeys(entries[i].keys, entries[j].keys)
		if c != 0 {
			return c < 0
		}
		// 唯一编号升序决胜，保证全序。
		return entries[i].rec.ID < entries[j].rec.ID
	}
	sort.Slice(entries, less)

	id := fmt.Sprintf("snap-%d", atomic.AddUint64(&s.seq, 1))
	snap := &Snapshot{id: id, sortHash: spec.Hash(), entries: entries}
	s.snapshots[id] = snap
	return snap
}

// snapshot 按编号取快照。
func (s *Store) snapshot(id string) *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshots[id]
}

// ReleaseSnapshot 释放快照；之后使用该快照的游标将报 ErrSnapshotExpired。
func (s *Store) ReleaseSnapshot(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, id)
}

func cloneRecord(rec Record) Record {
	fields := make(map[string]Value, len(rec.Fields))
	for k, v := range rec.Fields {
		fields[k] = v
	}
	return Record{ID: rec.ID, Fields: fields}
}
