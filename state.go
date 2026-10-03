// Package lscheckpoint implements a log-structured incremental state
// checkpoint generator. The in-memory state tree is sliced into hashed
// pages; read-only snapshots are produced via copy-on-write so checkpoint
// generation can run in parallel with ongoing writes.
package lscheckpoint

import (
	"hash/fnv"
	"sync"
)

// NumPages is the fixed number of hash-sliced pages in the state tree.
const NumPages = 64

// Page is a shard of the state tree. Once a page is shared with a
// snapshot it becomes immutable; writers must copy-on-write.
type Page struct {
	ID     int
	Data   map[string][]byte
	shared bool
}

func (p *Page) clone() *Page {
	d := make(map[string][]byte, len(p.Data))
	for k, v := range p.Data {
		d[k] = v
	}
	return &Page{ID: p.ID, Data: d}
}

// StateMachine is the in-memory paged key-value state tree.
type StateMachine struct {
	mu    sync.RWMutex
	pages []*Page
	seq   uint64
}

// NewStateMachine returns an empty state machine with NumPages pages.
func NewStateMachine() *StateMachine {
	pages := make([]*Page, NumPages)
	for i := range pages {
		pages[i] = &Page{ID: i, Data: make(map[string][]byte)}
	}
	return &StateMachine{pages: pages}
}

func pageIndex(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() % uint32(NumPages))
}

// Put inserts or updates a key. If the target page is shared with a
// snapshot, it is copied first (copy-on-write), so snapshots never
// observe in-flight mutations.
func (s *StateMachine) Put(key string, value []byte) {
	idx := pageIndex(key)
	s.mu.Lock()
	p := s.pages[idx]
	if p.shared {
		p = p.clone()
		s.pages[idx] = p
	}
	v := make([]byte, len(value))
	copy(v, value)
	p.Data[key] = v
	s.seq++
	s.mu.Unlock()
}

// Get returns the value for key.
func (s *StateMachine) Get(key string) ([]byte, bool) {
	idx := pageIndex(key)
	s.mu.RLock()
	v, ok := s.pages[idx].Data[key]
	s.mu.RUnlock()
	return v, ok
}

// Seq returns the number of applied writes.
func (s *StateMachine) Seq() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq
}

// Len returns the total number of keys across all pages.
func (s *StateMachine) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, p := range s.pages {
		n += len(p.Data)
	}
	return n
}

// Snapshot is a read-only, consistent view of the state tree. Pages are
// shared with the live state machine and protected by copy-on-write, so
// creating a snapshot is O(NumPages) pointer copies.
type Snapshot struct {
	pages []*Page
	seq   uint64
}

// Snapshot captures a consistent read-only view of the state tree.
func (s *StateMachine) Snapshot() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	pages := make([]*Page, len(s.pages))
	for i, p := range s.pages {
		p.shared = true
		pages[i] = p
	}
	return &Snapshot{pages: pages, seq: s.seq}
}

// Seq returns the state sequence number at snapshot time.
func (s *Snapshot) Seq() uint64 { return s.seq }
