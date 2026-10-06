// Package bboxidx provides a bounding-box index for moving objects.
//
// Mutations are recorded in a small delta buffer stamped with a global
// version; queries read both the immutable stable tree and the delta,
// deduplicating by object version. A background rebuild folds the delta
// into a new stable snapshot and swaps it in without losing moves that
// happen during the rebuild.
package bboxidx

import (
	"sort"
	"sync"
)

// entry is one versioned state of an object.
type entry struct {
	id      uint64
	box     Box
	version uint64
	deleted bool
}

// Index is a concurrent bounding-box index over moving objects.
//
// The stable tree is an immutable snapshot. The delta holds the latest
// operation per object id applied since the snapshot's version watermark.
// Every object state, in stable or delta, carries a global version so a
// query can keep only the newest state per id.
type Index struct {
	mu              sync.RWMutex
	stable          *tree
	stableWatermark uint64 // stable holds every mutation with version <= this
	delta           map[uint64]entry
	version         uint64 // global mutation counter, also the rebuild watermark source
	gen             uint64 // rebuild generation, incremented on every swap
}

// New returns an empty index.
func New() *Index {
	return &Index{
		stable: buildTree(nil),
		delta:  make(map[uint64]entry),
	}
}

// Upsert inserts or moves an object. Reusing a previously deleted id is
// allowed; the new state gets a higher version and shadows the old one.
func (idx *Index) Upsert(id uint64, b Box) {
	idx.mu.Lock()
	idx.version++
	idx.delta[id] = entry{id: id, box: b, version: idx.version}
	idx.mu.Unlock()
}

// Delete removes an object by recording a versioned tombstone.
func (idx *Index) Delete(id uint64) {
	idx.mu.Lock()
	idx.version++
	idx.delta[id] = entry{id: id, version: idx.version, deleted: true}
	idx.mu.Unlock()
}

// Query returns the ids of all live objects whose boxes intersect q.
// An empty query box matches nothing. The result is sorted by id.
func (idx *Index) Query(q Box) []uint64 {
	if q.IsEmpty() {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	best := make(map[uint64]uint64) // id -> newest matching version
	idx.stable.search(q, func(e entry) {
		if v, ok := best[e.id]; !ok || e.version > v {
			best[e.id] = e.version
		}
	})
	for id, e := range idx.delta {
		if v, ok := best[id]; ok && v >= e.version {
			continue // stable already holds a newer-or-equal state
		}
		// The delta state is the newest for this id: it decides.
		if e.deleted || !e.box.Intersects(q) {
			delete(best, id)
			continue
		}
		best[id] = e.version
	}

	ids := make([]uint64, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Rebuild folds the current delta into a new stable snapshot and swaps it
// in. It is safe to run concurrently with Upsert, Delete and Query.
//
// The merge is taken at a version watermark: mutations with version <=
// watermark are baked into the new stable tree, and on swap only delta
// entries with version > watermark are kept. Moves that happen while the
// new tree is being built therefore survive the swap.
func (idx *Index) Rebuild() {
	idx.mu.RLock()
	watermark := idx.version
	stable := idx.stable
	deltaCopy := make(map[uint64]entry, len(idx.delta))
	for id, e := range idx.delta {
		deltaCopy[id] = e
	}
	idx.mu.RUnlock()

	// Merge stable and delta, keeping the newest version per id and
	// dropping tombstones.
	merged := make(map[uint64]entry)
	stable.all(func(e entry) { merged[e.id] = e })
	for id, e := range deltaCopy {
		if cur, ok := merged[id]; !ok || e.version > cur.version {
			merged[id] = e
		}
	}
	items := make([]entry, 0, len(merged))
	for _, e := range merged {
		if !e.deleted {
			items = append(items, e)
		}
	}
	newStable := buildTree(items)

	idx.mu.Lock()
	// A concurrent rebuild may already have swapped in a newer snapshot;
	// never move the stable watermark backwards or moves would be lost.
	if watermark >= idx.stableWatermark {
		idx.stable = newStable
		idx.stableWatermark = watermark
		idx.gen++
	}
	// Delta entries at or below the stable watermark are fully reflected
	// in the stable tree and can be dropped; anything newer survives.
	for id, e := range idx.delta {
		if e.version <= idx.stableWatermark {
			delete(idx.delta, id)
		}
	}
	idx.mu.Unlock()
}

// Generation returns the number of completed rebuilds.
func (idx *Index) Generation() uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.gen
}

// DeltaLen returns the number of buffered delta entries.
func (idx *Index) DeltaLen() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.delta)
}
