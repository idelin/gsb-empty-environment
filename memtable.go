package lsm

import (
	"sort"
	"sync"
)

// entry 是 MemTable 中的值，deleted 表示墓碑(删除标记)。
type entry struct {
	value   []byte
	deleted bool
}

// perEntryOverhead 是每条记录在内存中的近似结构开销，用于大小估算。
const perEntryOverhead = 24

// memtable 是可变内存表。达到阈值后被冻结(frozen)，
// 转为 Immutable MemTable 等待后台刷盘，此后拒绝写入。
type memtable struct {
	mu     sync.RWMutex
	data   map[string]entry
	size   int64 // 近似占用字节数
	frozen bool
}

func newMemtable() *memtable {
	return &memtable{data: make(map[string]entry)}
}

// put 写入一条记录。若表已被冻结则返回 false，调用方需重试新的活跃表。
func (m *memtable) put(key, value []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return false
	}
	k := string(key)
	if old, ok := m.data[k]; ok {
		m.size += int64(len(value) - len(old.value))
	} else {
		m.size += int64(len(key) + len(value) + perEntryOverhead)
	}
	m.data[k] = entry{value: append([]byte(nil), value...)}
	return true
}

// delete 写入墓碑。
func (m *memtable) delete(key []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return false
	}
	k := string(key)
	if _, ok := m.data[k]; !ok {
		m.size += int64(len(key) + perEntryOverhead)
	}
	m.data[k] = entry{deleted: true}
	return true
}

func (m *memtable) get(key string) (entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.data[key]
	return e, ok
}

func (m *memtable) approxSize() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.size
}

// freeze 将表标记为不可变，此后 put/delete 返回 false。
func (m *memtable) freeze() {
	m.mu.Lock()
	m.frozen = true
	m.mu.Unlock()
}

// kv 是排序后的键值对，用于刷盘。
type kv struct {
	key string
	e   entry
}

// sorted 返回按键排序的全量快照，供 SSTable 写入使用。
func (m *memtable) sorted() []kv {
	m.mu.RLock()
	items := make([]kv, 0, len(m.data))
	for k, e := range m.data {
		items = append(items, kv{key: k, e: e})
	}
	m.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
	return items
}
