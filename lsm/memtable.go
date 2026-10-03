package lsm

import "sort"

// Memtable 是内存中的写缓冲。删除操作同样以 Entry(Tombstone=true)写入。
type Memtable struct {
	entries map[string]Entry
}

func newMemtable() *Memtable {
	return &Memtable{entries: make(map[string]Entry)}
}

func (m *Memtable) Put(e Entry) { m.entries[e.Key] = e }

func (m *Memtable) Get(key string) (Entry, bool) {
	e, ok := m.entries[key]
	return e, ok
}

func (m *Memtable) Len() int { return len(m.entries) }

// Sorted 返回按 Key 排序的快照,用于 Flush 与 Scan。
func (m *Memtable) Sorted() []Entry {
	out := make([]Entry, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
