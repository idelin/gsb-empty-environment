package netsec

import (
	"context"
	"sync"
)

// Key identifies a page within a snapshot. A snapshot holds at most one
// physical page per key.
type Key string

type binding struct {
	page *PhysPage
}

// Snapshot is one authorized view of a set of pages held by a Host. Pages
// shared with forked children are read-only; writes transparently copy the
// backing page first (copy-on-write), leaving every other authorized snapshot
// untouched.
//
// A Snapshot is safe for concurrent use.
type Snapshot struct {
	host     *Host
	id       int
	revoked  bool
	revokeCh chan struct{}
	pages    map[Key]*binding
}

// ID returns the snapshot's unique identifier within its host.
func (s *Snapshot) ID() int { return s.id }

// Revoked reports whether the snapshot has been revoked.
func (s *Snapshot) Revoked() bool {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	return s.revoked
}

// Read returns the current contents of the page bound to key. The returned
// slice must be treated as read-only; mutate pages with Write instead.
func (s *Snapshot) Read(key Key) ([]byte, error) {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	if s.revoked {
		return nil, ErrRevoked
	}
	b, ok := s.pages[key]
	if !ok {
		return nil, ErrNoPage
	}
	return b.page.mem, nil
}

// PageID reports the physical page ID currently backing key. Shared
// snapshots report the same ID; a copy-on-write changes the writer's ID.
func (s *Snapshot) PageID(key Key) (int, error) {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	if s.revoked {
		return 0, ErrRevoked
	}
	b, ok := s.pages[key]
	if !ok {
		return 0, ErrNoPage
	}
	return b.page.id, nil
}

// Has reports whether the snapshot holds a page for key.
func (s *Snapshot) Has(key Key) bool {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	_, ok := s.pages[key]
	return ok
}

// Keys returns the keys currently held by the snapshot.
func (s *Snapshot) Keys() []Key {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	keys := make([]Key, 0, len(s.pages))
	for k := range s.pages {
		keys = append(keys, k)
	}
	return keys
}

// Write applies mutate to a private copy of the page bound to key. If the
// page is shared with another authorized snapshot it is copied first, so the
// write cannot corrupt siblings; otherwise the private page is mutated in
// place. A nil mutate simply performs the copy-on-write detachment.
//
// The write blocks until a page can be allocated and is canceled if ctx is
// canceled or the snapshot is revoked meanwhile.
func (s *Snapshot) Write(ctx context.Context, key Key, mutate func(page []byte)) error {
	h := s.host
	h.mu.Lock()
	if s.revoked {
		h.mu.Unlock()
		return ErrRevoked
	}
	b, ok := s.pages[key]
	if !ok {
		h.mu.Unlock()
		return ErrNoPage
	}
	pg := b.page
	if pg.refs == 1 {
		// Sole owner: the page is already private, no copy needed.
		if mutate != nil {
			mutate(pg.mem)
		}
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()

	fresh, err := h.pool.Alloc(ctx)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if s.revoked {
		h.pool.Release(fresh)
		return ErrRevoked
	}
	// Re-check: another writer may have detached the binding while we waited.
	if b.page != pg {
		h.pool.Release(fresh)
		pg = b.page
		if pg.refs == 1 {
			if mutate != nil {
				mutate(pg.mem)
			}
			return nil
		}
		// Still shared: fall through and use the freshly allocated page.
	}
	copy(fresh.mem, pg.mem)
	if mutate != nil {
		mutate(fresh.mem)
	}
	b.page = fresh
	h.pool.Release(pg)
	return nil
}

// Release drops this snapshot's reference to one page, equivalent to revoking
// the snapshot's authorization for that single key.
func (s *Snapshot) Release(key Key) error {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	if s.revoked {
		return ErrRevoked
	}
	b, ok := s.pages[key]
	if !ok {
		return ErrNoPage
	}
	delete(s.pages, key)
	s.host.pool.Release(b.page)
	return nil
}

// Revoke permanently invalidates the snapshot and releases every page it
// holds. Pages shared with other snapshots keep their remaining references;
// pages whose last reference drops to zero are queued for zeroing.
func (s *Snapshot) Revoke() {
	h := s.host
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.revoked {
		return
	}
	s.revoked = true
	close(s.revokeCh)
	for _, b := range s.pages {
		h.pool.Release(b.page)
	}
	s.pages = nil
}

// Host owns a page pool and the set of authorized snapshots on top of it.
// A Host is safe for concurrent use.
type Host struct {
	mu       sync.Mutex
	pool     *Pool
	nextSnap int
	snaps    map[int]*Snapshot
}

// NewHost wraps an existing page pool. The host does not close the pool on
// its own; callers close the pool once every host is done.
func NewHost(pool *Pool) *Host {
	return &Host{pool: pool, snaps: make(map[int]*Snapshot)}
}

// Pool returns the underlying page pool.
func (h *Host) Pool() *Pool { return h.pool }

// NewSnapshot creates an empty authorized snapshot.
func (h *Host) NewSnapshot() *Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &Snapshot{
		host:     h,
		id:       h.nextSnap,
		revokeCh: make(chan struct{}),
		pages:    make(map[Key]*binding),
	}
	h.nextSnap++
	h.snaps[s.id] = s
	return s
}

// Grant allocates a fresh zeroed page bound to key in snapshot s and fills it
// via seed (seed may be nil). It fails with ErrPageExists if the key is held.
func (h *Host) Grant(ctx context.Context, s *Snapshot, key Key, seed func(page []byte)) error {
	h.mu.Lock()
	if s.revoked {
		h.mu.Unlock()
		return ErrRevoked
	}
	if _, exists := s.pages[key]; exists {
		h.mu.Unlock()
		return ErrPageExists
	}
	h.mu.Unlock()

	pg, err := h.pool.Alloc(ctx)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if s.revoked {
		h.pool.Release(pg)
		return ErrRevoked
	}
	if _, exists := s.pages[key]; exists {
		h.pool.Release(pg)
		return ErrPageExists
	}
	if seed != nil {
		seed(pg.mem)
	}
	s.pages[key] = &binding{page: pg}
	return nil
}

// TryGrant is the non-blocking variant of Grant; it returns ErrPoolExhausted
// when no page is currently available.
func (h *Host) TryGrant(s *Snapshot, key Key, seed func(page []byte)) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.revoked {
		return ErrRevoked
	}
	if _, exists := s.pages[key]; exists {
		return ErrPageExists
	}
	pg, err := h.pool.TryAlloc()
	if err != nil {
		return err
	}
	if seed != nil {
		seed(pg.mem)
	}
	s.pages[key] = &binding{page: pg}
	return nil
}

// Fork creates a child snapshot that shares every page of parent. Shared
// pages are referenced once more and remain read-only until either side
// writes, which triggers a copy-on-write.
func (h *Host) Fork(parent *Snapshot) (*Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if parent.revoked {
		return nil, ErrRevoked
	}
	child := &Snapshot{
		host:     h,
		id:       h.nextSnap,
		revokeCh: make(chan struct{}),
		pages:    make(map[Key]*binding, len(parent.pages)),
	}
	h.nextSnap++
	h.snaps[child.id] = child
	for k, b := range parent.pages {
		h.pool.Acquire(b.page)
		child.pages[k] = &binding{page: b.page}
	}
	return child, nil
}

// SnapshotCount returns the number of snapshots ever created, including
// revoked ones.
func (h *Host) SnapshotCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nextSnap
}
