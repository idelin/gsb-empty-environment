package lsm

// entryOverhead approximates the per-entry bookkeeping cost (skiplist
// pointers, slice headers) so the size threshold tracks real memory use.
const entryOverhead = 24

// MemTable is an in-memory sorted write buffer. While active it absorbs all
// writes; once its size crosses the configured threshold it is frozen into
// an immutable MemTable and queued for flushing to a Level-0 SSTable.
//
// Each MemTable is paired 1:1 with a WAL segment (walPath): the segment is
// only deleted after this MemTable has been durably flushed.
type MemTable struct {
	id      uint64
	sl      *skiplist
	size    int64 // approximate bytes held
	walPath string
}

func newMemTable(id uint64) *MemTable {
	return &MemTable{id: id, sl: newSkiplist()}
}

func (m *MemTable) Put(key, val []byte) {
	replaced, oldValLen := m.sl.Put(key, val)
	if replaced {
		m.size += int64(len(val) - oldValLen)
	} else {
		m.size += int64(len(key) + len(val) + entryOverhead)
	}
}

func (m *MemTable) Get(key []byte) ([]byte, bool) { return m.sl.Get(key) }

// Size returns the approximate number of bytes held by the MemTable.
func (m *MemTable) Size() int64 { return m.size }

// Len returns the number of live entries.
func (m *MemTable) Len() int { return m.sl.Len() }
