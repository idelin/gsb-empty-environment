package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Digest is the hash of a tree node.
type Digest [sha256.Size]byte

// Domain separation prefixes so a leaf hash can never collide with an
// internal-node hash.
const (
	domainLeaf     = 0x00
	domainInternal = 0x01
)

var (
	ErrIndexOutOfRange = errors.New("netsec: leaf index out of range")
	ErrLeafSize        = errors.New("netsec: leaf data has wrong size")
	ErrTxClosed        = errors.New("netsec: transaction already closed")
	ErrIntegrity       = errors.New("netsec: path verification failed")
)

// Config fixes the shape of a Tree. Fanout and LeafSize are given by the
// storage host and never change for the lifetime of the tree.
type Config struct {
	Fanout    int   // children per internal node, >= 2
	LeafSize  int   // bytes per leaf, >= 1
	NumLeaves int   // number of leaves, >= 1; may be odd
	Store     Store // optional durability target for committed nodes
}

func (c Config) validate() error {
	if c.Fanout < 2 {
		return fmt.Errorf("netsec: fanout must be >= 2, got %d", c.Fanout)
	}
	if c.LeafSize < 1 {
		return fmt.Errorf("netsec: leaf size must be >= 1, got %d", c.LeafSize)
	}
	if c.NumLeaves < 1 {
		return fmt.Errorf("netsec: leaf count must be >= 1, got %d", c.NumLeaves)
	}
	return nil
}

// node is immutable once constructed. Copy-on-write updates never mutate a
// node reachable from a published root.
type node struct {
	digest   Digest
	data     []byte  // leaf only
	children []*node // internal only
}

func leafDigest(data []byte) Digest {
	h := sha256.New()
	h.Write([]byte{domainLeaf})
	h.Write(data)
	var d Digest
	h.Sum(d[:0])
	return d
}

func internalDigest(children []*node) Digest {
	h := sha256.New()
	h.Write([]byte{domainInternal})
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(children)))
	h.Write(count[:])
	for _, c := range children {
		h.Write(c.digest[:])
	}
	var d Digest
	h.Sum(d[:0])
	return d
}

// Tree is a copy-on-write integrity tree. The only mutable state is the root
// pointer, swapped atomically under a lock at commit time.
type Tree struct {
	cfg    Config
	store  Store
	height int // 0 means the root is itself a leaf

	mu   sync.RWMutex
	root *node
}

// Open builds a tree of NumLeaves zero-filled leaves and persists the initial
// nodes to the configured Store.
func Open(cfg Config) (*Tree, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	store := cfg.Store
	if store == nil {
		store = NewMemoryStore()
	}
	zero := make([]byte, cfg.LeafSize)
	d := leafDigest(zero)
	leaves := make([]*node, cfg.NumLeaves)
	for i := range leaves {
		leaves[i] = &node{digest: d, data: zero}
	}
	root, height := build(leaves, cfg.Fanout)
	t := &Tree{cfg: cfg, store: store, height: height, root: root}
	if err := t.persist(nil, root); err != nil {
		return nil, fmt.Errorf("netsec: persisting initial tree: %w", err)
	}
	return t, nil
}

// build groups nodes level by level; the last node of each level may have
// fewer than fanout children when the count is odd.
func build(level []*node, fanout int) (*node, int) {
	height := 0
	for len(level) > 1 {
		next := make([]*node, 0, (len(level)+fanout-1)/fanout)
		for i := 0; i < len(level); i += fanout {
			j := i + fanout
			if j > len(level) {
				j = len(level)
			}
			kids := level[i:j]
			next = append(next, &node{digest: internalDigest(kids), children: kids})
		}
		level = next
		height++
	}
	return level[0], height
}

// span returns how many leaf slots a node at the given level covers.
func (t *Tree) span(level int) int {
	span := 1
	for i := 0; i < level; i++ {
		span *= t.cfg.Fanout
	}
	return span
}

// Root returns the digest of the currently committed root.
func (t *Tree) Root() Digest {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.root.digest
}

// Snapshot pins the current root. Snapshots stay valid forever: nodes are
// immutable, so an old root keeps authenticating the old version no matter
// how many commits follow.
func (t *Tree) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return Snapshot{
		root:      t.root,
		height:    t.height,
		fanout:    t.cfg.Fanout,
		leafSize:  t.cfg.LeafSize,
		numLeaves: t.cfg.NumLeaves,
	}
}

// leafUpdate is one staged leaf write, sorted by index before a commit.
type leafUpdate struct {
	index int
	data  []byte
}

// cow rebuilds the subtree rooted at n (level `level`, position `idx` among
// its level) if any update falls inside its leaf range. Untouched subtrees
// are returned by pointer, so old and new roots share them.
func (t *Tree) cow(n *node, level, idx int, upd []leafUpdate) *node {
	span := t.span(level)
	lo := idx * span
	hi := lo + span
	if hi > t.cfg.NumLeaves {
		hi = t.cfg.NumLeaves
	}
	a := sort.Search(len(upd), func(i int) bool { return upd[i].index >= lo })
	b := sort.Search(len(upd), func(i int) bool { return upd[i].index >= hi })
	sub := upd[a:b]
	if len(sub) == 0 {
		return n
	}
	if level == 0 {
		return &node{digest: leafDigest(sub[0].data), data: sub[0].data}
	}
	kids := make([]*node, len(n.children))
	for i, c := range n.children {
		kids[i] = t.cow(c, level-1, idx*t.cfg.Fanout+i, sub)
	}
	return &node{digest: internalDigest(kids), children: kids}
}

// persist writes every node reachable from newRoot but not from oldRoot to
// the store. Shared subtrees are detected by pointer identity.
func (t *Tree) persist(oldRoot, newRoot *node) error {
	return t.persistWalk(oldRoot, newRoot)
}

func (t *Tree) persistWalk(old, cur *node) error {
	if old == cur {
		return nil
	}
	if err := t.store.Put(cur.digest, encodeNode(cur)); err != nil {
		return err
	}
	for i, c := range cur.children {
		var oldChild *node
		if old != nil && i < len(old.children) {
			oldChild = old.children[i]
		}
		if err := t.persistWalk(oldChild, c); err != nil {
			return err
		}
	}
	return nil
}

func encodeNode(n *node) []byte {
	if n.children == nil {
		blob := make([]byte, 1+len(n.data))
		blob[0] = domainLeaf
		copy(blob[1:], n.data)
		return blob
	}
	blob := make([]byte, 0, 1+4+len(n.children)*sha256.Size)
	blob = append(blob, domainInternal)
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(n.children)))
	blob = append(blob, count[:]...)
	for _, c := range n.children {
		blob = append(blob, c.digest[:]...)
	}
	return blob
}

// Tx stages leaf updates against a Tree. A transaction may update arbitrary
// non-adjacent leaves; nothing it does is visible until Commit.
type Tx struct {
	tree    *Tree
	updates map[int][]byte
	done    bool
}

// Begin starts a new transaction.
func (t *Tree) Begin() *Tx {
	return &Tx{tree: t, updates: make(map[int][]byte)}
}

// Set stages a full-leaf write. data must be exactly LeafSize bytes and is
// copied, so the caller may reuse its buffer.
func (tx *Tx) Set(index int, data []byte) error {
	if tx.done {
		return ErrTxClosed
	}
	if index < 0 || index >= tx.tree.cfg.NumLeaves {
		return ErrIndexOutOfRange
	}
	if len(data) != tx.tree.cfg.LeafSize {
		return ErrLeafSize
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	tx.updates[index] = cp
	return nil
}

// Rollback discards the staged updates.
func (tx *Tx) Rollback() {
	tx.updates = nil
	tx.done = true
}

// Commit builds the new root off to the side, persists the new nodes, and
// only then swaps the root pointer. If any store write fails, the tree keeps
// the old root and every old snapshot stays valid; the returned error wraps
// the store failure.
func (tx *Tx) Commit() (Digest, error) {
	if tx.done {
		return Digest{}, ErrTxClosed
	}
	tx.done = true
	t := tx.tree
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(tx.updates) == 0 {
		return t.root.digest, nil
	}
	upd := make([]leafUpdate, 0, len(tx.updates))
	for idx, data := range tx.updates {
		upd = append(upd, leafUpdate{index: idx, data: data})
	}
	sort.Slice(upd, func(i, j int) bool { return upd[i].index < upd[j].index })

	oldRoot := t.root
	newRoot := t.cow(oldRoot, t.height, 0, upd)
	if err := t.persist(oldRoot, newRoot); err != nil {
		// No swap: the published tree is untouched.
		return Digest{}, fmt.Errorf("netsec: commit aborted, root unchanged: %w", err)
	}
	t.root = newRoot
	return newRoot.digest, nil
}
