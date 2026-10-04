package bptree

import "sync"

// Options configures a Tree.
type Options struct {
	// Capacity is the maximum number of keys per page (>= 2).
	Capacity int
	// MaxValueSize is the maximum value size in bytes (>= 1).
	MaxValueSize int
}

// Tree is a copy-on-write B+tree with int64 keys and []byte values.
//
// A write transaction clones every node on the path it modifies into fresh
// pages and, on commit, atomically publishes a new root. Pages reachable from
// a published root are immutable, so readers holding an older root (via a
// Snapshot) keep a stable, fixed-version view of the tree.
type Tree struct {
	store    *Store
	capacity int
	maxVal   int

	txMu sync.Mutex // serializes write transactions and Collect

	mu          sync.RWMutex
	root        PageID
	version     uint64
	activeRoots map[PageID]int // current root + one ref per open snapshot
}

// New creates a tree backed by a fresh in-memory page file whose page size is
// derived from the options.
func New(opts Options) (*Tree, error) {
	if opts.Capacity < 2 || opts.MaxValueSize < 1 {
		return nil, ErrInvalidOptions
	}
	return NewWithStore(NewStore(pageSizeFor(opts.Capacity, opts.MaxValueSize)), opts)
}

// NewWithStore creates a tree on top of an existing (empty) store.
func NewWithStore(store *Store, opts Options) (*Tree, error) {
	if opts.Capacity < 2 || opts.MaxValueSize < 1 {
		return nil, ErrInvalidOptions
	}
	t := &Tree{
		store:       store,
		capacity:    opts.Capacity,
		maxVal:      opts.MaxValueSize,
		activeRoots: make(map[PageID]int),
	}
	root := &node{id: store.Alloc(), leaf: true}
	if err := store.Write(root.id, root.encode(store.PageSize())); err != nil {
		return nil, err
	}
	t.root = root.id
	t.activeRoots[root.id] = 1
	return t, nil
}

// Store exposes the underlying page file (for integrity checks and tests).
func (t *Tree) Store() *Store { return t.store }

// Version returns the commit sequence number; every successful commit
// increments it by one.
func (t *Tree) Version() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.version
}

// Root returns the current root page id.
func (t *Tree) Root() PageID {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.root
}

// Get reads a single key from the current version of the tree.
func (t *Tree) Get(key int64) ([]byte, error) {
	snap := t.Snapshot()
	defer snap.Close()
	return snap.Get(key)
}

// Snapshot pins the current root and returns a read handle to that fixed
// version. Pages reachable from the snapshot's root are exempt from
// reclamation until Close.
func (t *Tree) Snapshot() *Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.activeRoots[t.root]++
	return &Snapshot{tree: t, root: t.root, version: t.version}
}

// Validate runs the independent structural validator against the current root.
func (t *Tree) Validate() error {
	t.mu.RLock()
	root := t.root
	t.mu.RUnlock()
	return Validate(t.store, root, t.capacity)
}

// minKeys is the minimum occupancy invariant for a non-root node.
func (t *Tree) minKeys(n *node) int {
	if n.leaf {
		return (t.capacity + 1) / 2
	}
	return t.capacity / 2
}
