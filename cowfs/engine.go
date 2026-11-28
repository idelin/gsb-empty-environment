package cowfs

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Errors returned by engine operations.
var (
	ErrSnapshotExists   = errors.New("cowfs: snapshot already exists")
	ErrSnapshotNotFound = errors.New("cowfs: snapshot not found")
	ErrFileNotFound     = errors.New("cowfs: file not found")
	ErrInvalidSlot      = errors.New("cowfs: block slot out of range")
)

// headOwner marks the live (non-snapshot) root inside block owner sets.
const headOwner uint64 = 0

// BlockPtr is a logical pointer to an immutable physical block.
type BlockPtr struct {
	ID       PhysID
	BirthTxg uint64
	Size     int
}

type deadEntry struct {
	birthTxg uint64
	size     int
}

// Snapshot is an immutable root captured at one transaction-group boundary.
type Snapshot struct {
	id   uint64
	name string
	txg  uint64

	// tree maps a file name to its ordered list of block pointers.
	tree map[string][]BlockPtr

	// dead holds blocks that died between this snapshot and the previous one
	// (or between the newest snapshot and the head for the active deadlist).
	dead map[PhysID]deadEntry

	referenced int
}

// SnapshotInfo is the externally visible accounting record for one snapshot.
type SnapshotInfo struct {
	Name           string
	Txg            uint64
	Referenced     int
	UniqueUsed     int
	DeadlistBlocks int
	DeadlistBytes  int
}

// EngineStats summarizes storage and cache state.
type EngineStats struct {
	Txg            uint64
	Snapshots      int
	AllocatedBytes int
	LiveBlocks     int
	ARC            ARCStats
}

type config struct {
	arcCapacity int
}

// Option customizes engine construction.
type Option func(*config)

// WithARCCapacity overrides the default resident ARC capacity of 64 blocks.
func WithARCCapacity(c int) Option {
	return func(cfg *config) { cfg.arcCapacity = c }
}

// Engine ties the block allocator, CoW snapshot/deadlist machinery and the ARC
// into a single coherent storage layer.
type Engine struct {
	mu sync.RWMutex

	store *BlockStore
	arc   *ARC

	txg uint64

	head     map[string][]BlockPtr
	headDead map[PhysID]deadEntry

	snaps      []*Snapshot
	snapByName map[string]*Snapshot
	nextSnapID uint64
}

// NewEngine constructs an engine with a fresh allocator and ARC.
func NewEngine(opts ...Option) *Engine {
	cfg := config{arcCapacity: 64}
	for _, opt := range opts {
		opt(&cfg)
	}

	store := NewBlockStore()
	arc := NewARC(cfg.arcCapacity)
	store.SetInvalidateHook(arc.Invalidate)

	return &Engine{
		store:      store,
		arc:        arc,
		txg:        1,
		head:       make(map[string][]BlockPtr),
		headDead:   make(map[PhysID]deadEntry),
		snapByName: make(map[string]*Snapshot),
	}
}

// Store exposes the underlying allocator (mainly for tests and metrics).
func (e *Engine) Store() *BlockStore { return e.store }

// ARC exposes the adaptive replacement cache.
func (e *Engine) ARC() *ARC { return e.arc }

// Txg returns the current open transaction group.
func (e *Engine) Txg() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.txg
}

// Sync closes the current transaction group and opens the next one.
func (e *Engine) Sync() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.txg++
}

// WriteBlock copy-on-writes one block slot of a file: a fresh physical block is
// allocated and stamped with the current txg, while the displaced block is
// either freed immediately or moved onto the active deadlist.
func (e *Engine) WriteBlock(file string, slot int, data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if slot < 0 {
		return ErrInvalidSlot
	}
	blocks := e.head[file]
	if slot > len(blocks) {
		return ErrInvalidSlot
	}

	id, b := e.store.Alloc(data, e.txg)
	ptr := BlockPtr{ID: id, BirthTxg: b.BirthTxg, Size: b.Size}

	if slot == len(blocks) {
		e.head[file] = append(blocks, ptr)
	} else {
		old := blocks[slot]
		e.head[file][slot] = ptr
		e.displace(old)
	}
	return nil
}

// Write replaces a whole file with a single CoW block.
func (e *Engine) Write(file string, data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, old := range e.head[file] {
		e.displace(old)
	}

	id, b := e.store.Alloc(data, e.txg)
	ptr := BlockPtr{ID: id, BirthTxg: b.BirthTxg, Size: b.Size}
	e.head[file] = []BlockPtr{ptr}
	return nil
}

// DeleteFile removes a file from the live root and retires every block it held.
func (e *Engine) DeleteFile(file string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	blocks, ok := e.head[file]
	if !ok {
		return fmt.Errorf("%w: %s", ErrFileNotFound, file)
	}
	for _, old := range blocks {
		e.displace(old)
	}
	delete(e.head, file)
	return nil
}

// displace retires a block that has just left the live root. Blocks born before
// the newest snapshot cannot be freed yet: a snapshot still observes them, so
// they enter the active deadlist. Blocks born after every snapshot are dead to
// all roots and return to the allocator immediately.
func (e *Engine) displace(old BlockPtr) {
	if len(e.snaps) > 0 && old.BirthTxg <= e.snaps[len(e.snaps)-1].txg {
		e.headDead[old.ID] = deadEntry{birthTxg: old.BirthTxg, size: old.Size}
		return
	}
	e.freeBlock(old.ID)
}

// freeBlock returns a physical id to the allocator. The store hook synchronously
// invalidates every ARC resident or ghost entry for that id.
func (e *Engine) freeBlock(id PhysID) {
	if _, ok := e.store.Free(id); !ok {
		panic(fmt.Sprintf("cowfs: double free of physical block %d", id))
	}
}

// Snapshot captures the live root at the current txg. The active deadlist is
// inherited by the new snapshot, after which a fresh txg is opened so later
// writes can never be attributed to this snapshot.
func (e *Engine) Snapshot(name string) (*SnapshotInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if name == "" {
		return nil, errors.New("cowfs: snapshot name must not be empty")
	}
	if _, exists := e.snapByName[name]; exists {
		return nil, fmt.Errorf("%w: %s", ErrSnapshotExists, name)
	}

	e.nextSnapID++
	snap := &Snapshot{
		id:   e.nextSnapID,
		name: name,
		txg:  e.txg,
		tree: make(map[string][]BlockPtr, len(e.head)),
		dead: e.headDead,
	}

	referenced := 0
	for file, blocks := range e.head {
		copied := append([]BlockPtr(nil), blocks...)
		snap.tree[file] = copied
		for _, ptr := range copied {
			referenced += ptr.Size
		}
	}
	snap.referenced = referenced

	// The active deadlist belongs to the new snapshot; the head starts empty.
	e.headDead = make(map[PhysID]deadEntry)
	e.snaps = append(e.snaps, snap)
	e.snapByName[name] = snap

	// Open the next transaction group: subsequent writes are strictly younger
	// than this snapshot.
	e.txg++

	info := e.snapshotInfo(snap)
	return &info, nil
}

// DeleteSnapshot destroys a snapshot in any position, merges its deadlist into
// the neighboring boundary and physically frees exactly the blocks that no
// remaining snapshot can observe. The returned byte count equals the
// UniqueUsed value reported immediately before deletion.
func (e *Engine) DeleteSnapshot(name string) (freedBytes int, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	snap, ok := e.snapByName[name]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrSnapshotNotFound, name)
	}

	idx := e.indexOfSnapshot(snap)

	expectedUnique := e.computeUniqueUsed(snap)

	// Determine the exact block set that only this snapshot keeps alive. These
	// blocks lose their last holder on deletion and are the sole bytes freed.
	holders := e.blockHolders()
	soleBlocks := make(map[PhysID]struct{})
	for id, set := range holders {
		if len(set) == 1 {
			if _, only := set[snap.id]; only {
				soleBlocks[id] = struct{}{}
			}
		}
	}

	// Merge the dying snapshot's deadlist into the boundary deadlist that
	// survives it: the next snapshot's, or the active head deadlist.
	target := e.headDead
	if idx+1 < len(e.snaps) {
		target = e.snaps[idx+1].dead
	}
	for id, entry := range snap.dead {
		if _, collision := target[id]; !collision {
			target[id] = entry
		}
	}

	// Free exactly the blocks that have no remaining holder. This covers both
	// tree-visible exclusive blocks and deadlist blocks whose last observer was
	// the deleted snapshot. Remove them from whichever deadlist retained them.
	for id := range soleBlocks {
		size, ok := e.store.Free(id)
		if !ok {
			panic(fmt.Sprintf("cowfs: expected live block %d on snapshot deletion", id))
		}
		freedBytes += size
		delete(target, id)
		delete(snap.dead, id)
		if idx > 0 {
			delete(e.snaps[idx-1].dead, id)
		}
	}

	e.snaps = append(e.snaps[:idx], e.snaps[idx+1:]...)
	delete(e.snapByName, name)

	if freedBytes != expectedUnique {
		panic(fmt.Sprintf(
			"cowfs: accounting invariant violated deleting %q: freed=%d unique_used=%d",
			name, freedBytes, expectedUnique))
	}
	return freedBytes, nil
}

func (e *Engine) indexOfSnapshot(snap *Snapshot) int {
	for i, s := range e.snaps {
		if s == snap {
			return i
		}
	}
	return -1
}

// ListSnapshots returns accounting records in creation order.
func (e *Engine) ListSnapshots() []SnapshotInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]SnapshotInfo, 0, len(e.snaps))
	for _, snap := range e.snaps {
		out = append(out, e.snapshotInfo(snap))
	}
	return out
}

// HeadFiles returns the live root's file names in stable order.
func (e *Engine) HeadFiles() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]string, 0, len(e.head))
	for name := range e.head {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (e *Engine) snapshotInfo(snap *Snapshot) SnapshotInfo {
	deadBytes := 0
	for _, entry := range snap.dead {
		deadBytes += entry.size
	}
	return SnapshotInfo{
		Name:           snap.name,
		Txg:            snap.txg,
		Referenced:     snap.referenced,
		UniqueUsed:     e.computeUniqueUsed(snap),
		DeadlistBlocks: len(snap.dead),
		DeadlistBytes:  deadBytes,
	}
}

// Read returns the live blocks of a file through the ARC.
func (e *Engine) Read(file string) ([]byte, error) {
	e.mu.RLock()
	blocks, ok := e.head[file]
	if !ok {
		e.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s", ErrFileNotFound, file)
	}
	out, err := e.readBlocks(blocks)
	e.mu.RUnlock()
	return out, err
}

// ReadAtSnapshot returns a file's contents as captured by a named snapshot.
func (e *Engine) ReadAtSnapshot(snapshot, file string) ([]byte, error) {
	e.mu.RLock()
	snap, ok := e.snapByName[snapshot]
	if !ok {
		e.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s", ErrSnapshotNotFound, snapshot)
	}
	blocks, ok := snap.tree[file]
	if !ok {
		e.mu.RUnlock()
		return nil, fmt.Errorf("%w: %s", ErrFileNotFound, file)
	}
	out, err := e.readBlocks(blocks)
	e.mu.RUnlock()
	return out, err
}

func (e *Engine) readBlocks(blocks []BlockPtr) ([]byte, error) {
	out := make([]byte, 0)
	for _, ptr := range blocks {
		data, ok := e.arc.Get(ptr.ID)
		if !ok {
			b, exists := e.store.Read(ptr.ID)
			if !exists {
				return nil, fmt.Errorf("cowfs: physical block %d missing", ptr.ID)
			}
			e.arc.Fill(ptr.ID, b.Data)
			data = b.Data
		}
		out = append(out, data...)
	}
	return out, nil
}

// Stats returns a combined storage and cache snapshot.
func (e *Engine) Stats() EngineStats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return EngineStats{
		Txg:            e.txg,
		Snapshots:      len(e.snaps),
		AllocatedBytes: e.store.AllocatedBytes(),
		LiveBlocks:     e.store.LiveBlocks(),
		ARC:            e.arc.Stats(),
	}
}
