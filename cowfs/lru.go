package cowfs

import "container/list"

// LRU is a conventional single-list least-recently-used cache with the same
// resident capacity and Get/Fill/Invalidate surface as ARC. It exists as the
// baseline that the sequential-scan acceptance test beats.
type LRU struct {
	c      int
	ll     *list.List
	index  map[PhysID]*list.Element
	data   map[PhysID][]byte
	hits   int
	misses int
}

type lruEntry struct {
	id PhysID
}

// NewLRU creates an LRU holding at most c resident blocks.
func NewLRU(c int) *LRU {
	if c <= 0 {
		panic("cowfs: LRU capacity must be positive")
	}
	return &LRU{
		c:     c,
		ll:    list.New(),
		index: make(map[PhysID]*list.Element),
		data:  make(map[PhysID][]byte),
	}
}

// Get returns a cached block on hit and records the access for LRU ordering.
func (l *LRU) Get(id PhysID) ([]byte, bool) {
	if el, ok := l.index[id]; ok {
		l.hits++
		l.ll.MoveToFront(el)
		return l.data[id], true
	}
	l.misses++
	return nil, false
}

// Fill caches a block, evicting the LRU entry when at capacity.
func (l *LRU) Fill(id PhysID, data []byte) {
	if el, ok := l.index[id]; ok {
		l.ll.MoveToFront(el)
		l.data[id] = append([]byte(nil), data...)
		return
	}
	if l.ll.Len() >= l.c {
		back := l.ll.Back()
		if back != nil {
			evicted := back.Value.(*lruEntry).id
			l.ll.Remove(back)
			delete(l.index, evicted)
			delete(l.data, evicted)
		}
	}
	el := l.ll.PushFront(&lruEntry{id: id})
	l.index[id] = el
	l.data[id] = append([]byte(nil), data...)
}

// Invalidate drops an id so reused physical ids cannot read stale data.
func (l *LRU) Invalidate(id PhysID) {
	if el, ok := l.index[id]; ok {
		l.ll.Remove(el)
		delete(l.index, id)
		delete(l.data, id)
	}
}

// Len is the current resident entry count.
func (l *LRU) Len() int { return l.ll.Len() }

// HitRate returns hits divided by total accesses, zero when no accesses.
func (l *LRU) HitRate() (rate float64, hits, misses int) {
	total := l.hits + l.misses
	if total == 0 {
		return 0, l.hits, l.misses
	}
	return float64(l.hits) / float64(total), l.hits, l.misses
}
