package cowfs

import (
	"container/list"
	"sync"
)

// arcList is one of the four ARC LRUs. Lookups are O(1) through the index.
type arcList struct {
	ll    *list.List
	index map[PhysID]*list.Element
}

func newArcList() *arcList {
	return &arcList{
		ll:    list.New(),
		index: make(map[PhysID]*list.Element),
	}
}

func (l *arcList) len() int { return l.ll.Len() }

func (l *arcList) contains(id PhysID) bool {
	_, ok := l.index[id]
	return ok
}

// pushFront inserts or re-inserts the entry as most-recently-used.
func (l *arcList) pushFront(e *arcEntry) {
	if e.list != nil {
		e.list.remove(e)
	}
	e.element = l.ll.PushFront(e)
	e.list = l
	l.index[e.id] = e.element
}

func (l *arcList) remove(e *arcEntry) {
	if e.list != l {
		panic("cowfs: arc entry removed from foreign list")
	}
	l.ll.Remove(e.element)
	delete(l.index, e.id)
	e.element = nil
	e.list = nil
}

func (l *arcList) removeID(id PhysID) bool {
	if el, ok := l.index[id]; ok {
		l.remove(el.Value.(*arcEntry))
		return true
	}
	return false
}

// removeBack evicts the least-recently-used entry.
func (l *arcList) removeBack() *arcEntry {
	el := l.ll.Back()
	if el == nil {
		return nil
	}
	e := el.Value.(*arcEntry)
	l.remove(e)
	return e
}

type arcEntry struct {
	id      PhysID
	data    []byte
	ghost   bool
	element *list.Element
	list    *arcList
}

// ARCStats is a point-in-time snapshot of the ARC shape.
type ARCStats struct {
	T1, T2, B1, B2 int
	TargetP        int
	Capacity       int
	Hits           int
	Misses         int
	GhostHits      int
}

// CacheSize describes how many blocks the four lists may hold.
type CacheSize struct {
	// C bounds the resident lists: len(T1)+len(T2) <= C.
	C int
}

// ARC is the adaptive replacement cache. T1/T2 hold resident blocks while B1/B2
// hold ghost metadata only. The adaptive target p steers the split of the
// resident capacity between T1 (single access) and T2 (repeated access).
type ARC struct {
	mu sync.Mutex

	c int
	p int

	t1 *arcList
	t2 *arcList
	b1 *arcList
	b2 *arcList

	// lookup spans all four lists so every id has at most one live entry.
	lookup map[PhysID]*arcEntry

	hits      int
	misses    int
	ghostHits int
}

// NewARC creates an ARC whose resident capacity is c blocks. Total metadata
// footprint (including ghosts) is bounded by 2c.
func NewARC(c int) *ARC {
	if c <= 0 {
		panic("cowfs: ARC capacity must be positive")
	}
	return &ARC{
		c:      c,
		p:      0,
		t1:     newArcList(),
		t2:     newArcList(),
		b1:     newArcList(),
		b2:     newArcList(),
		lookup: make(map[PhysID]*arcEntry),
	}
}

// Get returns the cached block and true on a resident hit. A ghost hit or a
// miss returns false; callers must then supply the block through Fill.
func (a *ARC) Get(id PhysID) ([]byte, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if e, ok := a.lookup[id]; ok {
		if !e.ghost {
			// Case 1/2: resident hit in T1 or T2; both move to the MRU of T2.
			a.hits++
			a.t2.pushFront(e)
			return e.data, true
		}

		// Case 3/4: ghost hit. Adapt p, then re-insert the id as resident.
		a.ghostHits++
		if e.list == a.b1 {
			delta := 1
			if a.b2.len()/a.b1.len() > delta {
				delta = a.b2.len() / a.b1.len()
			}
			if a.p+delta >= a.c {
				a.p = a.c
			} else {
				a.p += delta
			}
			a.b1.remove(e)
		} else {
			delta := 1
			if a.b1.len()/a.b2.len() > delta {
				delta = a.b1.len() / a.b2.len()
			}
			if a.p-delta <= 0 {
				a.p = 0
			} else {
				a.p -= delta
			}
			a.b2.remove(e)
		}
		e.ghost = false
		e.data = nil
		a.t2.pushFront(e)
		a.replace(id)
		return nil, false
	}

	// Case M: total miss. Make room for the new T1 entry.
	a.misses++
	l1 := a.t1.len() + a.b1.len()
	l2 := a.t2.len() + a.b2.len()

	switch {
	case l1 == a.c:
		// L1 is full: replace() below reclaims the resident T1 LRU, reusing its
		// slot for the incoming entry while keeping |T1|+|B1| <= c.
		if l1+l2 >= 2*a.c {
			a.evictGhost()
		}
	case l1+l2 >= 2*a.c:
		a.evictGhost()
	}

	e := &arcEntry{id: id}
	a.lookup[id] = e
	a.t1.pushFront(e)
	a.replace(id)
	return nil, false
}

// Fill stores data for an id whose preceding Get returned false. The entry
// already occupies the resident list chosen by Get.
func (a *ARC) Fill(id PhysID, data []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.lookup[id]; ok && !e.ghost {
		e.data = append([]byte(nil), data...)
	}
}

// Invalidate removes an id from whichever list holds it. Called when the block
// store frees a physical id, guaranteeing no ghost or resident entry survives
// to mislead a future allocation that reuses that id.
func (a *ARC) Invalidate(id PhysID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.lookup[id]; ok {
		if e.list != nil {
			e.list.remove(e)
		}
		a.dropEntry(e)
	}
}

// replace implements Replace(x, p): keep the resident lists at capacity c,
// moving T1 evictions into B1 and T2 evictions into B2.
func (a *ARC) replace(incoming PhysID) {
	for a.t1.len()+a.t2.len() > a.c {
		if a.t1.len() > 0 && (a.t1.len() > a.p ||
			(a.t1.len() == a.p && (a.b2.contains(incoming) || a.t2.len() == 0))) {
			e := a.t1.removeBack()
			// The victim already left T1, so this is the exact post-eviction
			// footprint before the ghost would be inserted. Keep |T1|+|B1|<=c.
			roomForB1 := a.t1.len()+a.b1.len() < a.c
			if roomForB1 {
				e.ghost = true
				e.data = nil
				a.b1.pushFront(e)
			} else {
				a.dropEntry(e)
			}
		} else {
			e := a.t2.removeBack()
			if e == nil {
				return
			}
			footprint := a.t1.len() + a.t2.len() + a.b1.len() + a.b2.len()
			roomForB2 := footprint < 2*a.c
			if roomForB2 {
				e.ghost = true
				e.data = nil
				a.b2.pushFront(e)
			} else {
				a.dropEntry(e)
			}
		}
	}
}

// evictGhost drops the LRU ghost when the four-list footprint reaches 2c.
func (a *ARC) evictGhost() {
	if a.b2.len() > 0 {
		a.dropEntry(a.b2.removeBack())
	} else if a.b1.len() > 0 {
		a.dropEntry(a.b1.removeBack())
	}
}

func (a *ARC) dropEntry(e *arcEntry) {
	if e == nil {
		return
	}
	delete(a.lookup, e.id)
}

// Contains reports list membership for diagnostics and tests.
func (a *ARC) Contains(id PhysID) (t1, t2, b1, b2 bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.lookup[id]; ok && e.list != nil {
		return e.list == a.t1, e.list == a.t2, e.list == a.b1, e.list == a.b2
	}
	return false, false, false, false
}

// Stats returns a snapshot of list sizes, target p and hit counters.
func (a *ARC) Stats() ARCStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return ARCStats{
		T1:        a.t1.len(),
		T2:        a.t2.len(),
		B1:        a.b1.len(),
		B2:        a.b2.len(),
		TargetP:   a.p,
		Capacity:  a.c,
		Hits:      a.hits,
		Misses:    a.misses,
		GhostHits: a.ghostHits,
	}
}

// CheckInvariants validates the structural ARC bounds after every operation.
func (a *ARC) CheckInvariants() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.t1.len()+a.t2.len() > a.c {
		return errInvariant("ARC: resident lists exceed capacity c")
	}
	if a.t1.len()+a.b1.len() > a.c {
		return errInvariant("ARC: T1+B1 exceeds c")
	}
	if a.t1.len()+a.t2.len()+a.b1.len()+a.b2.len() > 2*a.c {
		return errInvariant("ARC: four-list footprint exceeds 2c")
	}
	if a.p < 0 || a.p > a.c {
		return errInvariant("ARC: target p outside [0,c]")
	}
	return nil
}

type errInvariant string

func (e errInvariant) Error() string { return string(e) }
