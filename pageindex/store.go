// Package pageindex implements an immutable, copy-on-write B-tree index
// over explicitly numbered pages. Writers continuously replace tree nodes
// by allocating fresh pages, while long-lived snapshots keep old root
// versions fully readable. A page is recycled only when it is unreachable
// from the current root and from every active snapshot.
package pageindex

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrNotFound is returned when a key is absent from the index.
var ErrNotFound = errors.New("pageindex: key not found")

// Store owns the page allocator, the current writer root and the set of
// active snapshots. It is safe for concurrent use.
type Store struct {
	mu      sync.RWMutex
	pages   map[PageID]*node // allocated pages
	free    []PageID         // recycled page numbers
	next    PageID           // never-reused high-water mark
	root    PageID           // current writer root
	version uint64           // bumped on every committed write
	snaps   map[*Snapshot]struct{}
}

// NewStore returns an empty index.
func NewStore() *Store {
	st := &Store{
		pages: make(map[PageID]*node),
		snaps: make(map[*Snapshot]struct{}),
		next:  1,
	}
	st.root = st.allocNode(&node{leaf: true})
	return st
}

// allocNode publishes n in a fresh or recycled page. Caller must hold mu.
// Recycled pages come only from the free list, which gcLocked guarantees
// to be unreachable from the current root and all active snapshots, so
// reallocation can never pollute an old reader.
func (st *Store) allocNode(n *node) PageID {
	var id PageID
	if m := len(st.free); m > 0 {
		id = st.free[m-1]
		st.free = st.free[:m-1]
	} else {
		id = st.next
		st.next++
	}
	st.pages[id] = n
	return id
}

// gcLocked recycles every allocated page that is unreachable from the
// current root and from every active snapshot. Caller must hold mu.
func (st *Store) gcLocked() {
	roots := make([]PageID, 0, len(st.snaps)+1)
	roots = append(roots, st.root)
	for s := range st.snaps {
		roots = append(roots, s.root)
	}
	live := st.reachableLocked(roots...)
	for id := range st.pages {
		if !live[id] {
			delete(st.pages, id)
			st.free = append(st.free, id)
		}
	}
}

// reachableLocked returns the set of pages reachable from roots.
// Caller must hold mu (read or write).
func (st *Store) reachableLocked(roots ...PageID) map[PageID]bool {
	live := make(map[PageID]bool)
	stack := append([]PageID{}, roots...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if live[id] {
			continue
		}
		live[id] = true
		if n := st.pages[id]; n != nil {
			stack = append(stack, n.kids...)
		}
	}
	return live
}

// CheckRefGraph is the reference-graph checker. It verifies, over explicit
// page numbers, that:
//
//  1. every allocated page is reachable from the current root or an active
//     snapshot (no leaks);
//  2. every page referenced by a reachable node is allocated (no dangling
//     references);
//  3. the free list contains no duplicates and no page that is allocated
//     or reachable (no premature recycling).
func (st *Store) CheckRefGraph() error {
	st.mu.RLock()
	defer st.mu.RUnlock()

	roots := make([]PageID, 0, len(st.snaps)+1)
	roots = append(roots, st.root)
	for s := range st.snaps {
		roots = append(roots, s.root)
	}
	live := st.reachableLocked(roots...)

	for id := range st.pages {
		if !live[id] {
			return fmt.Errorf("pageindex: page %d allocated but unreachable (leak)", id)
		}
	}
	for id := range live {
		if st.pages[id] == nil {
			return fmt.Errorf("pageindex: page %d referenced but not allocated (dangling)", id)
		}
	}
	seen := make(map[PageID]bool, len(st.free))
	for _, id := range st.free {
		if seen[id] {
			return fmt.Errorf("pageindex: page %d duplicated on free list", id)
		}
		seen[id] = true
		if live[id] {
			return fmt.Errorf("pageindex: page %d on free list but still reachable", id)
		}
		if st.pages[id] != nil {
			return fmt.Errorf("pageindex: page %d on free list but still allocated", id)
		}
	}
	return nil
}

// Version returns the current committed version.
func (st *Store) Version() uint64 {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.version
}

// RootPage returns the page number of the current writer root.
func (st *Store) RootPage() PageID {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.root
}

// AllocatedPages returns the sorted page numbers currently allocated.
func (st *Store) AllocatedPages() []PageID {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make([]PageID, 0, len(st.pages))
	for id := range st.pages {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// FreePages returns the sorted page numbers currently on the free list.
func (st *Store) FreePages() []PageID {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := dupPage(st.free)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ReachablePages returns the sorted page numbers reachable from roots.
func (st *Store) ReachablePages(roots ...PageID) []PageID {
	st.mu.RLock()
	defer st.mu.RUnlock()
	live := st.reachableLocked(roots...)
	out := make([]PageID, 0, len(live))
	for id := range live {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Get looks up key in the current version.
func (st *Store) Get(key uint64) (uint64, error) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	v, ok := st.getFrom(st.root, key)
	if !ok {
		return 0, ErrNotFound
	}
	return v, nil
}

// getFrom descends the tree rooted at root. Caller must hold mu.
func (st *Store) getFrom(root PageID, key uint64) (uint64, bool) {
	id := root
	for {
		n := st.pages[id]
		i := lowerBound(n.keys, key)
		if i < len(n.keys) && n.keys[i] == key {
			return n.vals[i], true
		}
		if n.leaf {
			return 0, false
		}
		id = n.kids[i]
	}
}
