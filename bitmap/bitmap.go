// Package bitmap implements a compressed bitmap (Roaring-style) with
// automatic container selection (array / bitmap / RLE) and a multi-node
// concurrency arbitration layer that guarantees atomicity, linearizability,
// and freedom from deadlocks and data races.
//
// Concurrency model ("multi-node competition & conflict arbitration"):
//
//   - The 48-bit container-key space is striped across 256 shards. Structural
//     changes to a shard's slot table are serialized by that shard's RWMutex.
//   - Every container is published through an atomic pointer (copy-on-write).
//     Single-key mutations (Add/Remove/AddRange) are lock-free: competing
//     executors race a Compare-And-Swap; losers retry against the winner's
//     committed value. The CAS success is the linearization point.
//   - Multi-key mutations (Commit) and multi-key consistent reads
//     (ContainsAll / CountPresent) acquire shard locks in strictly ascending
//     shard order. A single global lock order makes circular wait impossible,
//     hence no deadlock. A batch is atomic: readers holding the same ordered
//     locks can never observe a half-applied batch.
//   - Bitmap-level AND/OR/XOR never hold more than one shard lock at a time
//     (snapshot per shard, then compute lock-free), so they cannot deadlock
//     against concurrent writers; each shard's result is linearizable.
package bitmap

import (
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"unsafe"
)

// shardCount is the number of lock stripes over the container-key space.
const shardCount = 256

// holder is an immutable box around a container so it can be swapped
// atomically as a single pointer.
type holder struct{ c container }

// slot is the atomically-swapped publication point for one container.
type slot struct{ p unsafe.Pointer } // *holder

func newSlot(c container) *slot {
	sl := &slot{}
	atomic.StorePointer(&sl.p, unsafe.Pointer(&holder{c: c}))
	return sl
}

func (sl *slot) load() container {
	return (*holder)(atomic.LoadPointer(&sl.p)).c
}

type shard struct {
	mu    sync.RWMutex
	slots map[uint64]*slot // key: id >> 16
}

// Bitmap is a concurrent compressed bitmap over uint64 IDs.
type Bitmap struct {
	shards [shardCount]shard
}

// New returns an empty Bitmap.
func New() *Bitmap {
	bm := &Bitmap{}
	for i := range bm.shards {
		bm.shards[i].slots = make(map[uint64]*slot)
	}
	return bm
}

func shardIndex(key uint64) int { return int(key & (shardCount - 1)) }

func (bm *Bitmap) getSlot(key uint64) *slot {
	sh := &bm.shards[shardIndex(key)]
	sh.mu.RLock()
	sl := sh.slots[key]
	sh.mu.RUnlock()
	return sl
}

func (bm *Bitmap) getOrCreateSlot(key uint64) *slot {
	sh := &bm.shards[shardIndex(key)]
	sh.mu.RLock()
	sl := sh.slots[key]
	sh.mu.RUnlock()
	if sl != nil {
		return sl
	}
	sh.mu.Lock()
	sl = sh.slots[key]
	if sl == nil {
		sl = newSlot(newArrayContainer())
		sh.slots[key] = sl
	}
	sh.mu.Unlock()
	return sl
}

// slotAdd races a CAS to install a cloned container containing x.
// This is the conflict-arbitration core: exactly one competitor commits
// per round; losers observe the committed state and retry.
func slotAdd(sl *slot, x uint16) bool {
	for {
		old := atomic.LoadPointer(&sl.p)
		nc, changed := (*holder)(old).c.clone().add(x)
		if !changed {
			return false
		}
		if atomic.CompareAndSwapPointer(&sl.p, old, unsafe.Pointer(&holder{c: nc.optimize()})) {
			return true
		}
		runtime.Gosched()
	}
}

func slotRemove(sl *slot, x uint16) bool {
	for {
		old := atomic.LoadPointer(&sl.p)
		nc, changed := (*holder)(old).c.clone().remove(x)
		if !changed {
			return false
		}
		if atomic.CompareAndSwapPointer(&sl.p, old, unsafe.Pointer(&holder{c: nc.optimize()})) {
			return true
		}
		runtime.Gosched()
	}
}

// slotMerge races a CAS to install union(current, delta).
func slotMerge(sl *slot, delta container) {
	for {
		old := atomic.LoadPointer(&sl.p)
		nc := orContainers((*holder)(old).c, delta)
		if atomic.CompareAndSwapPointer(&sl.p, old, unsafe.Pointer(&holder{c: nc})) {
			return
		}
		runtime.Gosched()
	}
}

// Add inserts id, reporting whether it was newly added. Lock-free.
func (bm *Bitmap) Add(id uint64) bool {
	return slotAdd(bm.getOrCreateSlot(id>>16), uint16(id))
}

// Remove deletes id, reporting whether it existed. Lock-free.
func (bm *Bitmap) Remove(id uint64) bool {
	sl := bm.getSlot(id >> 16)
	if sl == nil {
		return false
	}
	return slotRemove(sl, uint16(id))
}

// Contains reports whether id is present. Lock-free read.
func (bm *Bitmap) Contains(id uint64) bool {
	sl := bm.getSlot(id >> 16)
	if sl == nil {
		return false
	}
	return sl.load().contains(uint16(id))
}

// AddRange inserts every id in [lo, hi] (inclusive). Dense ranges are
// stored as RLE intervals, so this is O(containers touched), not O(ids).
func (bm *Bitmap) AddRange(lo, hi uint64) {
	if hi < lo {
		return
	}
	k0, k1 := lo>>16, hi>>16
	for k := k0; ; k++ {
		var a, b uint16 = 0, 65535
		if k == k0 {
			a = uint16(lo)
		}
		if k == k1 {
			b = uint16(hi)
		}
		delta := &runContainer{runs: []interval{{start: a, length: b - a}}}
		slotMerge(bm.getOrCreateSlot(k), delta)
		if k == k1 {
			return
		}
	}
}

// Batch buffers a multi-key mutation that Commit applies atomically.
type Batch struct {
	adds    []uint64
	removes []uint64
}

// Batch returns an empty batch. Not safe for concurrent use.
func (bm *Bitmap) Batch() *Batch { return &Batch{} }

// Add buffers an insertion.
func (b *Batch) Add(id uint64) *Batch { b.adds = append(b.adds, id); return b }

// Remove buffers a deletion.
func (b *Batch) Remove(id uint64) *Batch { b.removes = append(b.removes, id); return b }

// Commit applies a batch atomically: no concurrent ContainsAll /
// CountPresent / Commit can observe a partially applied batch.
// Shard write locks are taken in strictly ascending order (deadlock-free).
// Removals are applied before additions.
func (bm *Bitmap) Commit(b *Batch) {
	idxs := batchShards(b)
	for _, i := range idxs {
		bm.shards[i].mu.Lock()
	}
	defer func() {
		for i := len(idxs) - 1; i >= 0; i-- {
			bm.shards[idxs[i]].mu.Unlock()
		}
	}()
	for _, id := range b.removes {
		key := id >> 16
		if sl := bm.shards[shardIndex(key)].slots[key]; sl != nil {
			slotRemove(sl, uint16(id))
		}
	}
	for _, id := range b.adds {
		key := id >> 16
		sh := &bm.shards[shardIndex(key)]
		sl := sh.slots[key]
		if sl == nil {
			sl = newSlot(newArrayContainer())
			sh.slots[key] = sl
		}
		slotAdd(sl, uint16(id))
	}
}

func batchShards(b *Batch) []int {
	set := make(map[int]struct{}, 4)
	for _, id := range b.adds {
		set[shardIndex(id>>16)] = struct{}{}
	}
	for _, id := range b.removes {
		set[shardIndex(id>>16)] = struct{}{}
	}
	idxs := make([]int, 0, len(set))
	for i := range set {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	return idxs
}

// groupByShard returns the sorted shard indices covering ids and a lookup
// from shard to its ids.
func groupByShard(ids []uint64) ([]int, map[int][]uint64) {
	byShard := make(map[int][]uint64, 4)
	for _, id := range ids {
		s := shardIndex(id >> 16)
		byShard[s] = append(byShard[s], id)
	}
	idxs := make([]int, 0, len(byShard))
	for s := range byShard {
		idxs = append(idxs, s)
	}
	sort.Ints(idxs)
	return idxs, byShard
}

// CountPresent atomically counts how many of ids are present, taking shard
// read locks in ascending order so the result is a consistent snapshot
// with respect to Commit.
func (bm *Bitmap) CountPresent(ids ...uint64) int {
	if len(ids) == 0 {
		return 0
	}
	idxs, byShard := groupByShard(ids)
	for _, i := range idxs {
		bm.shards[i].mu.RLock()
	}
	defer func() {
		for i := len(idxs) - 1; i >= 0; i-- {
			bm.shards[idxs[i]].mu.RUnlock()
		}
	}()
	n := 0
	for s, group := range byShard {
		sh := &bm.shards[s]
		for _, id := range group {
			if sl := sh.slots[id>>16]; sl != nil && sl.load().contains(uint16(id)) {
				n++
			}
		}
	}
	return n
}

// ContainsAll atomically reports whether every id is present.
func (bm *Bitmap) ContainsAll(ids ...uint64) bool {
	return bm.CountPresent(ids...) == len(ids)
}

// shardSnapshot copies the shard's slot table under its read lock; the
// holders themselves are immutable, so the copy is a consistent
// per-shard snapshot usable without holding any lock.
func (bm *Bitmap) shardSnapshot(idx int) map[uint64]container {
	sh := &bm.shards[idx]
	sh.mu.RLock()
	out := make(map[uint64]container, len(sh.slots))
	for k, sl := range sh.slots {
		out[k] = sl.load()
	}
	sh.mu.RUnlock()
	return out
}

// Cardinality returns the total number of IDs in the bitmap.
func (bm *Bitmap) Cardinality() uint64 {
	var n uint64
	for i := 0; i < shardCount; i++ {
		for _, c := range bm.shardSnapshot(i) {
			n += uint64(c.cardinality())
		}
	}
	return n
}

// ForEach visits every ID in ascending order, using per-shard snapshots.
func (bm *Bitmap) ForEach(fn func(id uint64)) {
	for i := 0; i < shardCount; i++ {
		snap := bm.shardSnapshot(i)
		keys := make([]uint64, 0, len(snap))
		for k := range snap {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(a, b int) bool { return keys[a] < keys[b] })
		for _, k := range keys {
			key := k
			snap[k].forEach(func(lo uint16) { fn(key<<16 | uint64(lo)) })
		}
	}
}

// Stats summarizes container usage.
type Stats struct {
	Containers       int
	ArrayContainers  int
	BitmapContainers int
	RunContainers    int
	Cardinality      uint64
}

// Stats returns per-container-type counts (a per-shard consistent read).
func (bm *Bitmap) Stats() Stats {
	var st Stats
	for i := 0; i < shardCount; i++ {
		for _, c := range bm.shardSnapshot(i) {
			if c.cardinality() == 0 {
				continue
			}
			st.Containers++
			switch c.(type) {
			case *arrayContainer:
				st.ArrayContainers++
			case *bitmapContainer:
				st.BitmapContainers++
			case *runContainer:
				st.RunContainers++
			}
			st.Cardinality += uint64(c.cardinality())
		}
	}
	return st
}

// Compact drops empty containers. Call only when no concurrent mutation is
// in flight (quiescent point), otherwise a racing Add could be lost.
func (bm *Bitmap) Compact() {
	for i := 0; i < shardCount; i++ {
		sh := &bm.shards[i]
		sh.mu.Lock()
		for k, sl := range sh.slots {
			if sl.load().cardinality() == 0 {
				delete(sh.slots, k)
			}
		}
		sh.mu.Unlock()
	}
}

// Node is a handle for one competing executor (a "node" in the arbitration
// model). Nodes share the underlying Bitmap; all conflict resolution is
// performed by the Bitmap's CAS arbitration and ordered-lock commit.
type Node struct {
	ID int
	bm *Bitmap
}

// Node returns a competing-executor handle bound to this bitmap.
func (bm *Bitmap) Node(id int) *Node { return &Node{ID: id, bm: bm} }

// Add inserts id through this node.
func (n *Node) Add(id uint64) bool { return n.bm.Add(id) }

// Remove deletes id through this node.
func (n *Node) Remove(id uint64) bool { return n.bm.Remove(id) }

// Commit atomically applies a batch through this node.
func (n *Node) Commit(b *Batch) { n.bm.Commit(b) }
