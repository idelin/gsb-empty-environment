// Package depcache implements a byte-bounded cache whose entries may
// depend on other entries.
//
// Semantics:
//   - An entry may declare dependencies on other live entries. The
//     dependency graph is a DAG; Put rejects edges that would create
//     a cycle.
//   - Evicting an entry first revokes every entry that transitively
//     depends on it (its children), so a parent is never removed while
//     a child still references it.
//   - An active borrow (Handle obtained via Get) pins the whole
//     dependency group: neither the borrowed entry nor any entry it
//     transitively depends on can be evicted until Release is called.
//   - Capacity is accounted in actual value bytes. Put evicts LRU
//     groups to make room; if not enough bytes can be freed, the new
//     entry is rejected with ErrNoSpace and the cache is left
//     unchanged. Releasing borrows unblocks eviction, so a rejected
//     Put may be retried afterwards.
package depcache

import (
	"container/list"
	"errors"
	"sync"
)

var (
	// ErrNoSpace is returned when the cache cannot free enough bytes
	// to store a new entry (capacity too small or too much pinned by
	// active borrows).
	ErrNoSpace = errors.New("depcache: insufficient capacity")
	// ErrMissingDep is returned when a declared dependency does not
	// name a live entry.
	ErrMissingDep = errors.New("depcache: dependency not found")
	// ErrCycle is returned when the declared dependencies would create
	// a cycle in the dependency graph.
	ErrCycle = errors.New("depcache: dependency cycle")
)

// Cache is a byte-bounded, dependency-aware cache. It is safe for
// concurrent use.
type Cache struct {
	mu       sync.Mutex
	capacity int64
	used     int64
	entries  map[string]*entry
	lru      *list.List // front = most recently used, elements are *entry
}

type entry struct {
	key      string
	value    []byte
	size     int64
	deps     map[string]*entry // parents this entry depends on
	children map[string]*entry // entries that depend on this one
	borrows  int
	elem     *list.Element
}

// New returns a Cache holding at most capacity bytes of values.
func New(capacity int64) *Cache {
	return &Cache{
		capacity: capacity,
		entries:  make(map[string]*entry),
		lru:      list.New(),
	}
}

// Used reports the number of bytes currently accounted to live entries.
func (c *Cache) Used() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

// Capacity reports the configured byte capacity.
func (c *Cache) Capacity() int64 { return c.capacity }

// Handle is an active borrow of an entry. The borrowed entry and its
// whole dependency group are pinned against eviction until Release.
type Handle struct {
	c        *Cache
	e        *entry
	released bool
}

// Value returns the borrowed value. It remains valid after Release.
func (h *Handle) Value() []byte { return h.e.value }

// Key returns the borrowed entry's key.
func (h *Handle) Key() string { return h.e.key }

// Release ends the borrow. It is idempotent.
func (h *Handle) Release() {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	if h.released {
		return
	}
	h.released = true
	h.e.borrows--
}

// Get borrows the entry for key. The caller must Release the returned
// handle. The second return value reports whether the entry exists.
func (c *Cache) Get(key string) (*Handle, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	e.borrows++
	c.lru.MoveToFront(e.elem)
	return &Handle{c: c, e: e}, true
}

// Put stores value under key, replacing any existing entry with that
// key. deps names live entries the new entry depends on.
//
// Put fails, leaving the cache unchanged, when a dependency is missing
// (ErrMissingDep), would create a cycle (ErrCycle), or when not enough
// bytes can be freed by evicting unpinned entries (ErrNoSpace).
func (c *Cache) Put(key string, value []byte, deps ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	parents := make(map[string]*entry, len(deps))
	for _, d := range deps {
		if d == key {
			return ErrCycle
		}
		p, ok := c.entries[d]
		if !ok {
			return ErrMissingDep
		}
		parents[d] = p
	}
	// Adding key -> parent closes a cycle iff parent already reaches key.
	for _, p := range parents {
		if reaches(p, key, make(map[*entry]bool)) {
			return ErrCycle
		}
	}

	size := int64(len(value))
	if size > c.capacity {
		return ErrNoSpace
	}

	old := c.entries[key]
	var delta int64
	if old != nil {
		delta = size - old.size
	} else {
		delta = size
	}

	if need := c.used + delta - c.capacity; need > 0 {
		protected := make(map[string]bool, len(parents)+1)
		protected[key] = true
		for d := range parents {
			protected[d] = true
		}
		if !c.evict(need, protected) {
			return ErrNoSpace
		}
	}

	if old != nil {
		for _, p := range old.deps {
			delete(p.children, key)
		}
		old.value = value
		old.size = size
		old.deps = parents
		for _, p := range parents {
			p.children[key] = old
		}
		c.used += delta
		c.lru.MoveToFront(old.elem)
		return nil
	}

	e := &entry{
		key:      key,
		value:    value,
		size:     size,
		deps:     parents,
		children: make(map[string]*entry),
	}
	e.elem = c.lru.PushFront(e)
	c.entries[key] = e
	for _, p := range parents {
		p.children[key] = e
	}
	c.used += size
	return nil
}

// reaches reports whether e transitively depends on key.
func reaches(e *entry, key string, seen map[*entry]bool) bool {
	if seen[e] {
		return false
	}
	seen[e] = true
	for k, p := range e.deps {
		if k == key {
			return true
		}
		if reaches(p, key, seen) {
			return true
		}
	}
	return false
}

// closure returns e plus every entry that transitively depends on e.
func closure(e *entry) []*entry {
	var out []*entry
	seen := map[*entry]bool{e: true}
	queue := []*entry{e}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)
		for _, ch := range cur.children {
			if !seen[ch] {
				seen[ch] = true
				queue = append(queue, ch)
			}
		}
	}
	return out
}

// evict frees at least need bytes by removing whole eviction groups in
// LRU order, returning false without changing the cache when the bytes
// cannot be freed. A group is the victim plus its dependent closure; it
// is skipped when any member is borrowed or protected. Eviction is
// two-phase: groups are selected first and only removed once the need
// is provably met, so a failed Put leaves the cache untouched.
func (c *Cache) evict(need int64, protected map[string]bool) bool {
	var groups [][]*entry
	selected := make(map[*entry]bool)
	var freed int64
	for el := c.lru.Back(); el != nil && freed < need; el = el.Prev() {
		cand := el.Value.(*entry)
		if selected[cand] {
			continue
		}
		g := closure(cand)
		blocked := false
		var bytes int64
		for _, m := range g {
			if m.borrows > 0 || protected[m.key] || selected[m] {
				blocked = true
				break
			}
			bytes += m.size
		}
		if blocked {
			continue
		}
		for _, m := range g {
			selected[m] = true
		}
		groups = append(groups, g)
		freed += bytes
	}
	if freed < need {
		return false
	}
	// Children are revoked before their parents: each group lists the
	// victim's dependents breadth-first, so removing in reverse order
	// unlinks deepest children first.
	for _, g := range groups {
		for i := len(g) - 1; i >= 0; i-- {
			c.remove(g[i])
		}
	}
	return true
}

// remove unlinks e from the graph, the LRU list and the byte account.
func (c *Cache) remove(e *entry) {
	for _, p := range e.deps {
		delete(p.children, e.key)
	}
	delete(c.entries, e.key)
	c.lru.Remove(e.elem)
	c.used -= e.size
}

// Snapshot returns a consistent view of the cache for verification:
// live keys with sizes, the dependency edges, and borrow counts.
func (c *Cache) Snapshot() (sizes map[string]int64, deps map[string][]string, borrows map[string]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sizes = make(map[string]int64, len(c.entries))
	deps = make(map[string][]string, len(c.entries))
	borrows = make(map[string]int, len(c.entries))
	for k, e := range c.entries {
		sizes[k] = e.size
		borrows[k] = e.borrows
		for d := range e.deps {
			deps[k] = append(deps[k], d)
		}
	}
	return sizes, deps, borrows
}
