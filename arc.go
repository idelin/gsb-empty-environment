// Package arc implements an Adaptive Replacement Cache (ARC).
//
// The cache keeps two value queues: T1 for keys accessed once and T2 for
// keys accessed more than once. Keys evicted from T1/T2 linger as ghost
// records (no value) in B1/B2. A hit on a ghost record adapts the target
// size p of T1 (and, implicitly, capacity-p of T2).
//
// Capacity is accounted in number of entries, never in bytes. The target p
// is always clamped to [0, capacity].
package arc

import (
	"container/list"
	"fmt"
	"strings"
)

type section int

const (
	inT1 section = iota
	inT2
	inB1
	inB2
)

type entry struct {
	key   string
	value interface{}
	sec   section
	elem  *list.Element
}

// Cache is an ARC cache. It is not safe for concurrent use.
type Cache struct {
	capacity int
	p        int // target number of entries in T1, 0 <= p <= capacity
	t1, t2   *list.List
	b1, b2   *list.List
	entries  map[string]*entry
}

// New returns a cache holding at most capacity entries.
func New(capacity int) *Cache {
	if capacity < 1 {
		panic("arc: capacity must be >= 1")
	}
	return &Cache{
		capacity: capacity,
		t1:       list.New(),
		t2:       list.New(),
		b1:       list.New(),
		b2:       list.New(),
		entries:  make(map[string]*entry),
	}
}

// Get returns the value for key. A hit in T1 promotes the key to T2.
// Ghost records carry no value, so a ghost hit reports ok == false.
func (c *Cache) Get(key string) (value interface{}, ok bool) {
	ent, found := c.entries[key]
	if !found {
		return nil, false
	}
	switch ent.sec {
	case inT1:
		c.t1.Remove(ent.elem)
		ent.sec = inT2
		ent.elem = c.t2.PushFront(ent)
		return ent.value, true
	case inT2:
		c.t2.MoveToFront(ent.elem)
		return ent.value, true
	}
	return nil, false
}

// Set inserts or updates key. A key found in a ghost list adapts the target
// p before the key is reinstated into T2.
func (c *Cache) Set(key string, value interface{}) {
	if ent, found := c.entries[key]; found {
		ent.value = value
		switch ent.sec {
		case inT1:
			c.t1.Remove(ent.elem)
			ent.sec = inT2
			ent.elem = c.t2.PushFront(ent)
		case inT2:
			c.t2.MoveToFront(ent.elem)
		case inB1:
			c.p = minInt(c.capacity, c.p+maxInt(c.b2.Len()/c.b1.Len(), 1))
			c.replace(false)
			c.b1.Remove(ent.elem)
			ent.sec = inT2
			ent.elem = c.t2.PushFront(ent)
		case inB2:
			c.p = maxInt(0, c.p-maxInt(c.b1.Len()/c.b2.Len(), 1))
			c.replace(true)
			c.b2.Remove(ent.elem)
			ent.sec = inT2
			ent.elem = c.t2.PushFront(ent)
		}
		return
	}

	switch l1 := c.t1.Len() + c.b1.Len(); {
	case l1 == c.capacity:
		if c.t1.Len() < c.capacity {
			c.removeBack(c.b1)
			c.replace(false)
		} else {
			// B1 is empty; drop the LRU of T1 without a ghost.
			c.removeBack(c.t1)
		}
	case l1 < c.capacity:
		total := l1 + c.t2.Len() + c.b2.Len()
		if total >= c.capacity {
			if total == 2*c.capacity {
				c.removeBack(c.b2)
			}
			c.replace(false)
		}
	}

	ent := &entry{key: key, value: value, sec: inT1}
	ent.elem = c.t1.PushFront(ent)
	c.entries[key] = ent
}

// Delete removes key from whichever queue or ghost list holds it. Deleting
// an absent key (including one deleted before) is a no-op returning false.
func (c *Cache) Delete(key string) bool {
	ent, found := c.entries[key]
	if !found {
		return false
	}
	switch ent.sec {
	case inT1:
		c.t1.Remove(ent.elem)
	case inT2:
		c.t2.Remove(ent.elem)
	case inB1:
		c.b1.Remove(ent.elem)
	case inB2:
		c.b2.Remove(ent.elem)
	}
	delete(c.entries, key)
	return true
}

// Len returns the number of live entries (T1 + T2), ghosts excluded.
func (c *Cache) Len() int { return c.t1.Len() + c.t2.Len() }

// Target returns the current target size p for T1.
func (c *Cache) Target() int { return c.p }

// Capacity returns the maximum number of live entries.
func (c *Cache) Capacity() int { return c.capacity }

// State renders p and all four lists, most-recently-used first. Ghost keys
// are shown without values.
func (c *Cache) State() string {
	return fmt.Sprintf("p=%d T1=%s T2=%s B1=%s B2=%s",
		c.p, keys(c.t1), keys(c.t2), keys(c.b1), keys(c.b2))
}

func (c *Cache) String() string { return c.State() }

// replace moves the LRU entry of T1 or T2 into its ghost list.
func (c *Cache) replace(fromB2 bool) {
	if c.t1.Len() > 0 && ((fromB2 && c.t1.Len() == c.p) || c.t1.Len() > c.p) {
		c.demote(c.t1, c.b1, inB1)
		return
	}
	c.demote(c.t2, c.b2, inB2)
}

// demote moves the LRU entry of src to the MRU position of dst as a ghost.
func (c *Cache) demote(src, dst *list.List, sec section) {
	e := src.Back()
	if e == nil {
		return
	}
	ent := e.Value.(*entry)
	src.Remove(e)
	ent.value = nil
	ent.sec = sec
	ent.elem = dst.PushFront(ent)
}

func (c *Cache) removeBack(l *list.List) {
	e := l.Back()
	if e == nil {
		return
	}
	l.Remove(e)
	delete(c.entries, e.Value.(*entry).key)
}

func keys(l *list.List) string {
	parts := make([]string, 0, l.Len())
	for e := l.Front(); e != nil; e = e.Next() {
		parts = append(parts, e.Value.(*entry).key)
	}
	return "[" + strings.Join(parts, " ") + "]"
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
