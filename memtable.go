package lsm

import "sort"

// Memtable is the in-memory write buffer. Entries are kept sorted by key so
// they can be flushed to an SSTable in order. Only the latest version of a
// key is retained.
type Memtable struct {
	entries []Entry // sorted by Key
	size    int     // approximate payload size in bytes
}

func NewMemtable() *Memtable {
	return &Memtable{}
}

func (m *Memtable) Put(e Entry) {
	i := sort.Search(len(m.entries), func(i int) bool { return m.entries[i].Key >= e.Key })
	if i < len(m.entries) && m.entries[i].Key == e.Key {
		m.size += len(e.Value) - len(m.entries[i].Value)
		m.entries[i] = e
		return
	}
	m.entries = append(m.entries, Entry{})
	copy(m.entries[i+1:], m.entries[i:])
	m.entries[i] = e
	m.size += len(e.Key) + len(e.Value) + 1
}

// Get returns the entry for key and whether it exists.
func (m *Memtable) Get(key string) (Entry, bool) {
	i := sort.Search(len(m.entries), func(i int) bool { return m.entries[i].Key >= key })
	if i < len(m.entries) && m.entries[i].Key == key {
		return m.entries[i], true
	}
	return Entry{}, false
}

func (m *Memtable) Size() int { return m.size }

func (m *Memtable) Entries() []Entry { return m.entries }

func (m *Memtable) Reset() {
	m.entries = nil
	m.size = 0
}
