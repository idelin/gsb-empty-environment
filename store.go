package snapscan

import (
	"sort"
	"sync"
)

// Record 是交付给调用方的一条键值数据。
type Record struct {
	Key   string
	Value string
}

// versionedValue 是某个键的一次历史版本。
type versionedValue struct {
	seq     uint64
	value   string
	deleted bool
}

// Store 是带版本的键值存储。每次写/删都会推进全局序列号。
// Snapshot 固定某个序列号；基于快照的扫描只能看到该序列号
// 之前可见的数据，之后的写入与删除均不影响扫描结果。
type Store struct {
	mu       sync.RWMutex
	seq      uint64
	versions map[string][]versionedValue // 每个键的版本链，按 seq 升序
	keys     []string                    // 出现过的所有键，升序
}

// NewStore 返回一个空 Store。
func NewStore() *Store {
	return &Store{versions: make(map[string][]versionedValue)}
}

// Snapshot 标识存储历史上一个固定的读取点。
type Snapshot struct {
	Seq uint64
}

// Put 写入一个键的新版本。
func (s *Store) Put(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	if _, ok := s.versions[key]; !ok {
		i := sort.SearchStrings(s.keys, key)
		s.keys = append(s.keys, "")
		copy(s.keys[i+1:], s.keys[i:])
		s.keys[i] = key
	}
	s.versions[key] = append(s.versions[key], versionedValue{seq: s.seq, value: value})
}

// Delete 为键写入一个删除版本。
func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.versions[key]; !ok {
		return
	}
	s.seq++
	s.versions[key] = append(s.versions[key], versionedValue{seq: s.seq, deleted: true})
}

// Pin 把当前序列号固定为一个稳定的数据快照。
func (s *Store) Pin() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{Seq: s.seq}
}

// visibleAt 返回键在快照点可见的值；不存在或已删除时 ok=false。
// 调用方需持有读锁。
func (s *Store) visibleAt(key string, snap Snapshot) (value string, ok bool) {
	chain := s.versions[key]
	idx := sort.Search(len(chain), func(i int) bool { return chain[i].seq > snap.Seq }) - 1
	if idx < 0 || chain[idx].deleted {
		return "", false
	}
	return chain[idx].value, true
}

// ScanAfter 按升序返回快照中可见、且键严格大于 afterKey 的至多 limit 条数据。
func (s *Store) ScanAfter(snap Snapshot, afterKey string, limit int) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Record
	i := sort.SearchStrings(s.keys, afterKey)
	if i < len(s.keys) && s.keys[i] == afterKey {
		i++
	}
	for ; i < len(s.keys) && len(out) < limit; i++ {
		if val, ok := s.visibleAt(s.keys[i], snap); ok {
			out = append(out, Record{Key: s.keys[i], Value: val})
		}
	}
	return out
}
