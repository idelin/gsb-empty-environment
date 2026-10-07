package pageindex

import "sync"

// Snapshot is a read-only view of the index pinned at one commit epoch.
// Every page reachable from the snapshot's root stays allocated and
// unmodified until Close. Close is idempotent and safe to call concurrently;
// reading after Close is a programming error.
type Snapshot struct {
	db    *DB
	root  PageNo
	epoch uint64
	once  sync.Once
}

// Root returns the pinned root page number.
func (s *Snapshot) Root() PageNo { return s.root }

// Epoch returns the pinned commit epoch.
func (s *Snapshot) Epoch() uint64 { return s.epoch }

// Get reads the pinned root.
func (s *Snapshot) Get(key uint64) (uint64, bool) {
	return getFrom(s.db.pager, s.root, key)
}

// Items returns all key/value pairs of the pinned root in key order.
func (s *Snapshot) Items() []KV {
	return collectItems(s.db.pager, s.root)
}

// Close releases the snapshot. Pages only reachable from it become
// reclaimable. Safe to call any number of times from any goroutine.
func (s *Snapshot) Close() {
	s.once.Do(func() { s.db.release(s.epoch) })
}
