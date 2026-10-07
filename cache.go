// Package cache implements a byte-bounded cache whose entries may depend on
// other entries.
//
// Semantics:
//   - An entry may declare dependencies on other live entries. Dependency
//     cycles (including self-dependencies) are rejected.
//   - Evicting an entry first revokes every entry that transitively depends
//     on it; the revoked group frees its bytes atomically.
//   - An active borrow (Handle) blocks eviction of the whole group the
//     borrowed entry belongs to.
//   - Capacity is accounted in actual value bytes. If not enough bytes can be
//     freed, new entries are rejected with ErrNoSpace and the cache is left
//     unchanged; the insertion can be retried once borrows are released.
package cache

import (
	"container/list"
	"errors"
	"sync"
)

var (
	// ErrTooLarge is returned when a single entry exceeds the capacity.
	ErrTooLarge = errors.New("cache: entry exceeds capacity")
	// ErrMissingDep is returned when a dependency does not exist.
	ErrMissingDep = errors.New("cache: dependency does not exist")
	// ErrCycle is returned when the dependencies would form a cycle.
	ErrCycle = errors.New("cache: dependency cycle")
	// ErrNoSpace is returned when not enough bytes can be freed.
	ErrNoSpace = errors.New("cache: cannot free enough space")
)

// Cache is a byte-bounded, dependency-aware cache. It is safe for concurrent
// use.
type Cache struct {
	mu       sync.Mutex
	capacity int64
	used     int64
	entries  map[string]*entry
	lru      *list.List // front = most recently used; values are *entry
}

type entry struct {
	key      string
	value    []byte
	size     int64
	deps     map[string]struct{} // entries this one depends on (parents)
	children map[string]struct{} // entries depending on this one
	borrows  int                 // active handles
	elem     *list.Element
}

// New returns a Cache with the given byte capacity. Negative capacities are
// treated as zero.
func New(capacity int64) *Cache {
	if capacity < 0 {
		capacity = 0
	}
	return &Cache{
		capacity: capacity,
		entries:  make(map[string]*entry),
		lru:      list.New(),
	}
}

// Capacity returns the configured byte capacity.
func (c *Cache) Capacity() int64 { return c.capacity }

// Used returns the number of bytes currently held by live entries.
func (c *Cache) Used() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

// Len returns the number of live entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Set inserts or replaces an entry. deps names live entries the new entry
// depends on; the dependencies are protected from eviction during this call.
//
// Replacing an existing key keeps its dependents: they are re-attached to the
// new value. Replacement of a borrowed entry is allowed; outstanding handles
// keep the old value.
//
// On any error the cache is left unchanged.
func (c *Cache) Set(key string, value []byte, deps ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	size := int64(len(value))
	if size > c.capacity {
		return ErrTooLarge
	}

	depSet := make(map[string]struct{}, len(deps))
	for _, d := range deps {
		if d == key {
			return ErrCycle
		}
		if _, ok := c.entries[d]; !ok {
			return ErrMissingDep
		}
		depSet[d] = struct{}{}
	}
	for d := range depSet {
		if c.dependsOn(d, key) {
			return ErrCycle
		}
	}

	old := c.entries[key]
	victims, ok := c.planEvictions(old, size, depSet)
	if !ok {
		return ErrNoSpace
	}

	// Detach the replaced entry; its dependents are re-attached below.
	var orphans map[string]struct{}
	if old != nil {
		orphans = old.children
		c.unlink(old)
	}

	for _, v := range victims {
		if _, live := c.entries[v.key]; !live {
			continue // already revoked as part of another group
		}
		for _, m := range c.closure(v, nil) {
			c.unlink(m)
		}
	}

	e := &entry{
		key:      key,
		value:    value,
		size:     size,
		deps:     depSet,
		children: make(map[string]struct{}),
	}
	for k := range orphans {
		if child, live := c.entries[k]; live {
			e.children[k] = struct{}{}
			child.deps[key] = struct{}{}
		}
	}
	for d := range depSet {
		c.entries[d].children[key] = struct{}{}
	}
	e.elem = c.lru.PushFront(e)
	c.entries[key] = e
	c.used += size
	return nil
}

// Get returns a Handle for key, or false if the entry is absent. The entry
// (and therefore its whole dependency group) is protected from eviction until
// the Handle is released.
func (c *Cache) Get(key string) (*Handle, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[key]
	if e == nil {
		return nil, false
	}
	e.borrows++
	c.lru.MoveToFront(e.elem)
	return &Handle{c: c, e: e, val: e.value}, true
}

// dependsOn reports whether `from` transitively depends on `to`.
func (c *Cache) dependsOn(from, to string) bool {
	seen := map[string]struct{}{from: {}}
	queue := []string{from}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		e := c.entries[k]
		if e == nil {
			continue
		}
		for d := range e.deps {
			if d == to {
				return true
			}
			if _, s := seen[d]; !s {
				seen[d] = struct{}{}
				queue = append(queue, d)
			}
		}
	}
	return false
}

// closure returns root plus every live entry that transitively depends on it.
// Entries in removed (and their subtrees) are treated as already gone.
func (c *Cache) closure(root *entry, removed map[*entry]struct{}) []*entry {
	seen := map[*entry]struct{}{root: {}}
	queue := []*entry{root}
	var out []*entry
	for len(queue) > 0 {
		e := queue[0]
		queue = queue[1:]
		out = append(out, e)
		for ck := range e.children {
			child := c.entries[ck]
			if child == nil {
				continue
			}
			if _, gone := removed[child]; gone {
				continue
			}
			if _, s := seen[child]; !s {
				seen[child] = struct{}{}
				queue = append(queue, child)
			}
		}
	}
	return out
}

// planEvictions simulates freeing bytes for an incoming entry of size bytes.
// If replacing is non-nil it is treated as already removed. Entries whose key
// is in protect, and groups with active borrows, are never selected. It
// returns the victim group roots to evict, or ok=false if not enough bytes
// can be freed. Nothing is mutated.
func (c *Cache) planEvictions(replacing *entry, size int64, protect map[string]struct{}) (victims []*entry, ok bool) {
	removed := make(map[*entry]struct{})
	var freed int64
	if replacing != nil {
		removed[replacing] = struct{}{}
		freed = replacing.size
	}
	for c.used-freed+size > c.capacity {
		var victim *entry
		var group []*entry
		for el := c.lru.Back(); el != nil; el = el.Prev() {
			cand := el.Value.(*entry)
			if _, gone := removed[cand]; gone {
				continue
			}
			cl := c.closure(cand, removed)
			blocked := false
			for _, m := range cl {
				_, prot := protect[m.key]
				if m.borrows > 0 || prot {
					blocked = true
					break
				}
			}
			if blocked {
				continue
			}
			victim, group = cand, cl
			break
		}
		if victim == nil {
			return nil, false
		}
		victims = append(victims, victim)
		for _, m := range group {
			removed[m] = struct{}{}
			freed += m.size
		}
	}
	return victims, true
}

// unlink detaches e from the cache and its parents. Its dependents are not
// touched; callers either revoke them (group eviction) or re-attach them
// (replacement).
func (c *Cache) unlink(e *entry) {
	for d := range e.deps {
		if p := c.entries[d]; p != nil {
			delete(p.children, e.key)
		}
	}
	delete(c.entries, e.key)
	c.lru.Remove(e.elem)
	c.used -= e.size
}

// Handle is an active borrow of an entry. While held, the entry's dependency
// group cannot be evicted.
type Handle struct {
	c   *Cache
	e   *entry
	val []byte
	mu  sync.Mutex
	rel bool
}

// Key returns the borrowed entry's key.
func (h *Handle) Key() string { return h.e.key }

// Value returns the value captured when the Handle was acquired.
func (h *Handle) Value() []byte { return h.val }

// Size returns the byte size of the borrowed value.
func (h *Handle) Size() int64 { return h.e.size }

// Release ends the borrow. It is idempotent.
func (h *Handle) Release() {
	h.mu.Lock()
	if h.rel {
		h.mu.Unlock()
		return
	}
	h.rel = true
	h.mu.Unlock()
	h.c.mu.Lock()
	h.e.borrows--
	h.c.mu.Unlock()
}
