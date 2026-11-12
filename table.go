package taskpkg

import "errors"

// ErrTableFull is returned when every live entry is pinned by a waiting
// section and no entry can be evicted to make room for a new insertion.
var ErrTableFull = errors.New("taskpkg: dynamic table full of pinned entries")

// Entry is a single dynamic table entry. Each entry belongs to the
// connection generation (Gen) in which it was inserted.
type Entry struct {
	Index uint64 // absolute, never reused within a generation
	Gen   uint64
	Name  string
	Value string
}

// DynamicTable is a bounded FIFO dynamic table. Eviction never removes a
// pinned entry; waiting sections pin the entries they reference so that a
// cancelled section cannot drop pins still held by other sections.
type DynamicTable struct {
	capacity int
	gen      uint64
	inserted uint64
	entries  map[uint64]Entry
	order    []uint64 // live absolute indices, oldest first
	pins     map[uint64]int
}

// NewDynamicTable returns a table holding at most capacity entries.
func NewDynamicTable(capacity int) *DynamicTable {
	if capacity < 1 {
		capacity = 1
	}
	return &DynamicTable{
		capacity: capacity,
		entries:  make(map[uint64]Entry),
		pins:     make(map[uint64]int),
	}
}

// Gen returns the current connection generation.
func (t *DynamicTable) Gen() uint64 { return t.gen }

// Inserted returns the total number of insertions in this generation,
// i.e. the absolute index the next inserted entry will receive.
func (t *DynamicTable) Inserted() uint64 { return t.inserted }

// Live reports whether idx has been inserted and not yet evicted.
func (t *DynamicTable) Live(idx uint64) bool {
	_, ok := t.entries[idx]
	return ok
}

// Get returns the entry at idx if it is live.
func (t *DynamicTable) Get(idx uint64) (Entry, bool) {
	e, ok := t.entries[idx]
	return e, ok
}

// Insert appends a new entry, evicting the oldest unpinned entries as
// needed. It returns the absolute index of the new entry.
func (t *DynamicTable) Insert(name, value string) (uint64, error) {
	for len(t.entries) >= t.capacity {
		if err := t.evictOldest(); err != nil {
			return 0, err
		}
	}
	idx := t.inserted
	t.entries[idx] = Entry{Index: idx, Gen: t.gen, Name: name, Value: value}
	t.order = append(t.order, idx)
	t.inserted++
	return idx, nil
}

func (t *DynamicTable) evictOldest() error {
	for i, idx := range t.order {
		if t.pins[idx] > 0 {
			continue
		}
		delete(t.entries, idx)
		t.order = append(t.order[:i], t.order[i+1:]...)
		return nil
	}
	return ErrTableFull
}

// Pin records one more hold on idx. Pins on not-yet-inserted indices are
// allowed: they take effect as soon as the entry exists.
func (t *DynamicTable) Pin(idx uint64) { t.pins[idx]++ }

// Unpin releases one hold on idx. It never drives the count negative.
func (t *DynamicTable) Unpin(idx uint64) {
	if t.pins[idx] <= 1 {
		delete(t.pins, idx)
		return
	}
	t.pins[idx]--
}

// PinCount reports how many holds idx currently has.
func (t *DynamicTable) PinCount(idx uint64) int { return t.pins[idx] }

// Reissue starts a new connection generation, discarding every entry and
// pin. Entries from earlier generations become unreachable.
func (t *DynamicTable) Reissue() {
	t.gen++
	t.inserted = 0
	t.entries = make(map[uint64]Entry)
	t.order = nil
	t.pins = make(map[uint64]int)
}
