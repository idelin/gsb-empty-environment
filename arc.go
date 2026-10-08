// Package arc implements an Adaptive Replacement Cache (ARC).
//
// The cache splits its capacity between a first-access queue (T1) and a
// repeat-access queue (T2). Keys evicted from either queue are remembered
// as valueless ghost records (B1 and B2). A request that hits a ghost
// record shifts the target size p of the first-access queue, always kept
// within [0, capacity]. Capacity is accounted in entries: every key
// counts as one, regardless of the size of its value.
package arc

import (
	"container/list"
	"fmt"
	"strings"
)

type section uint8

const (
	secT1 section = iota
	secT2
	secB1
	secB2
)

// entry is one cache record. Ghost entries (secB1, secB2) hold no value.
type entry struct {
	key   string
	value interface{}
	sec   section
}

// Cache is an ARC cache. It is not safe for concurrent use.
type Cache struct {
	capacity int
	p        int // target size of T1, always within [0, capacity]
	t1       *list.List
	t2       *list.List
	b1       *list.List
	b2       *list.List
	index    map[string]*list.Element
}

// New returns a Cache holding at most capacity entries. Capacity is
// counted in entries, regardless of value size.
func New(capacity int) *Cache {
	if capacity < 1 {
		panic("arc: capacity must be at least 1")
	}
	return &Cache{
		capacity: capacity,
		t1:       list.New(),
		t2:       list.New(),
		b1:       list.New(),
		b2:       list.New(),
		index:    make(map[string]*list.Element),
	}
}

// Len returns the number of cached entries (T1 + T2). Ghost records do
// not count: they carry no value.
func (c *Cache) Len() int { return c.t1.Len() + c.t2.Len() }

// Capacity returns the maximum number of entries.
func (c *Cache) Capacity() int { return c.capacity }

// Target returns the current target size p of the first-access queue T1.
// It always satisfies 0 <= p <= Capacity().
func (c *Cache) Target() int { return c.p }

// Get returns the value for key and marks it as repeatedly accessed,
// promoting it to the repeat-access queue T2. Ghost records hold no
// value, so a ghost hit reports (nil, false) and does not adapt the
// target; adaptation happens when the key is stored again via Set.
func (c *Cache) Get(key string) (interface{}, bool) {
	e, ok := c.index[key]
	if !ok {
		return nil, false
	}
	ent := e.Value.(*entry)
	if ent.sec == secB1 || ent.sec == secB2 {
		return nil, false
	}
	c.promote(e)
	return ent.value, true
}

// Set stores value under key following the ARC replacement policy.
//
// A hit in T1 or T2 updates the value and promotes the entry to T2. A
// hit on a ghost record in B1 grows the target p (clamped at capacity);
// a hit in B2 shrinks it (clamped at zero); in both cases the entry is
// fetched into T2. A complete miss inserts into T1, evicting to the
// ghost lists as needed.
func (c *Cache) Set(key string, value interface{}) {
	if e, ok := c.index[key]; ok {
		ent := e.Value.(*entry)
		switch ent.sec {
		case secT1, secT2:
			ent.value = value
			c.promote(e)
		case secB1:
			c.p = minInt(c.capacity, c.p+maxInt(c.b2.Len()/c.b1.Len(), 1))
			c.replace(false)
			c.b1.Remove(e)
			ent.sec = secT2
			ent.value = value
			c.index[key] = c.t2.PushFront(ent)
		case secB2:
			c.p = maxInt(0, c.p-maxInt(c.b1.Len()/c.b2.Len(), 1))
			c.replace(true)
			c.b2.Remove(e)
			ent.sec = secT2
			ent.value = value
			c.index[key] = c.t2.PushFront(ent)
		}
		return
	}

	switch l1 := c.t1.Len() + c.b1.Len(); {
	case l1 == c.capacity:
		if c.t1.Len() < c.capacity {
			c.removeBack(c.b1)
			c.replace(false)
		} else {
			// B1 is empty: the evicted key leaves no ghost behind.
			c.removeBack(c.t1)
		}
	case l1 < c.capacity:
		if total := l1 + c.t2.Len() + c.b2.Len(); total >= c.capacity {
			if total == 2*c.capacity {
				c.removeBack(c.b2)
			}
			c.replace(false)
		}
	}

	c.index[key] = c.t1.PushFront(&entry{key: key, value: value, sec: secT1})
}

// Delete removes key from the cache, including any ghost record. It
// reports whether key held a value. Deleting a missing key is a no-op
// and reports false.
func (c *Cache) Delete(key string) bool {
	e, ok := c.index[key]
	if !ok {
		return false
	}
	ent := e.Value.(*entry)
	switch ent.sec {
	case secT1:
		c.t1.Remove(e)
	case secT2:
		c.t2.Remove(e)
	case secB1:
		c.b1.Remove(e)
	case secB2:
		c.b2.Remove(e)
	}
	delete(c.index, key)
	return ent.sec == secT1 || ent.sec == secT2
}

// Snapshot describes the cache state on one line: the target p and the
// four queues, most-recently-used first. Ghost queues list keys without
// values.
func (c *Cache) Snapshot() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "p=%d T1=%v T2=%v B1=%v B2=%v",
		c.p, queueKeys(c.t1), queueKeys(c.t2), queueKeys(c.b1), queueKeys(c.b2))
	return sb.String()
}

// promote moves a live entry to the front of the repeat-access queue T2.
func (c *Cache) promote(e *list.Element) {
	ent := e.Value.(*entry)
	if ent.sec == secT1 {
		c.t1.Remove(e)
	} else {
		c.t2.Remove(e)
	}
	ent.sec = secT2
	c.index[ent.key] = c.t2.PushFront(ent)
}

// replace evicts one live entry to its ghost list. inB2 reports whether
// the current request hit ghost list B2.
func (c *Cache) replace(inB2 bool) {
	if c.t1.Len() == 0 && c.t2.Len() == 0 {
		// Deletes may have drained both live queues; there is
		// nothing to evict and the cache has free room.
		return
	}
	if c.t1.Len() >= 1 && ((inB2 && c.t1.Len() == c.p) || c.t1.Len() > c.p) {
		c.evict(c.t1, c.b1, secB1)
		return
	}
	if c.t2.Len() >= 1 {
		c.evict(c.t2, c.b2, secB2)
		return
	}
	c.evict(c.t1, c.b1, secB1)
}

// evict moves the LRU entry of from to the MRU position of the ghost
// list to, dropping its value.
func (c *Cache) evict(from, to *list.List, sec section) {
	e := from.Back()
	ent := e.Value.(*entry)
	from.Remove(e)
	ent.sec = sec
	ent.value = nil
	c.index[ent.key] = to.PushFront(ent)
}

// removeBack drops the LRU element of l entirely, leaving no ghost.
func (c *Cache) removeBack(l *list.List) {
	e := l.Back()
	if e == nil {
		return
	}
	l.Remove(e)
	delete(c.index, e.Value.(*entry).key)
}

func queueKeys(l *list.List) []string {
	keys := make([]string, 0, l.Len())
	for e := l.Front(); e != nil; e = e.Next() {
		keys = append(keys, e.Value.(*entry).key)
	}
	return keys
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
