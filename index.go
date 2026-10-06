// Package bboxindex implements a bounding-box index for moving objects.
//
// Mutations (Upsert/Delete) are recorded in a small delta buffer instead
// of touching the stable packed R-tree. Queries search both the stable
// tree and the delta buffer, deduplicating by object version so the
// newest state of each id wins. A background rebuild bulk-loads a new
// stable tree from a consistent snapshot and swaps it in without losing
// moves that arrived while the rebuild was running.
package bboxindex

import "sync"

// Index is a concurrency-safe bounding-box index.
type Index struct {
	mu sync.RWMutex

	root  *node            // immutable stable tree, replaced on rebuild
	delta map[uint64]entry // mutations newer than the stable tree

	version    uint64 // global mutation counter, orders delta entries
	generation uint64 // bumped on every rebuild swap
}

// New returns an empty index.
func New() *Index {
	return &Index{delta: make(map[uint64]entry)}
}

// Upsert inserts or moves an object. The mutation lands in the delta
// buffer; the stable tree is only refreshed by Rebuild.
func (idx *Index) Upsert(id uint64, b Box) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.version++
	idx.delta[id] = entry{id: id, box: b, version: idx.version}
}

// Delete removes an object by recording a tombstone in the delta buffer.
// Deleting an unknown id is a no-op semantically but still consumes a
// version, keeping the version stream gap-free for rebuild snapshots.
func (idx *Index) Delete(id uint64) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.version++
	idx.delta[id] = entry{id: id, version: idx.version, deleted: true}
}

// Query returns the ids of all live objects whose box intersects q.
// Delta entries shadow stable-tree entries for the same id, so each id
// is reported at most once and always reflects its newest version.
func (idx *Index) Query(q Box) []uint64 {
	if q.Empty() {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var out []uint64
	var hits []entry
	search(idx.root, q, &hits)
	for _, h := range hits {
		if _, shadowed := idx.delta[h.id]; !shadowed {
			out = append(out, h.id)
		}
	}
	for id, e := range idx.delta {
		if !e.deleted && e.box.Intersects(q) {
			out = append(out, id)
		}
	}
	return out
}

// Rebuild bulk-loads a fresh stable tree from the current merged view
// and swaps it in. Mutations that arrive after the snapshot was taken
// (version > snapshot version) are kept in the delta buffer, so no move
// is lost across the swap.
func (idx *Index) Rebuild() {
	items, snapVersion := idx.snapshot()
	root := buildTree(items) // expensive part, runs without holding the lock
	idx.commit(root, snapVersion)
}

// snapshot merges the stable tree and the delta buffer into a flat
// id -> box view and returns it together with the snapshot version.
func (idx *Index) snapshot() (map[uint64]Box, uint64) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	items := make(map[uint64]Box)
	var all []entry
	collect(idx.root, &all)
	for _, e := range all {
		items[e.id] = e.box
	}
	for id, e := range idx.delta {
		if e.deleted {
			delete(items, id)
		} else {
			items[id] = e.box
		}
	}
	return items, idx.version
}

// commit swaps in the rebuilt tree and drops delta entries that were
// already captured by the snapshot (version <= snapVersion). Entries
// with a newer version describe moves that happened during the rebuild
// and must survive the swap.
func (idx *Index) commit(root *node, snapVersion uint64) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.root = root
	idx.generation++
	for id, e := range idx.delta {
		if e.version <= snapVersion {
			delete(idx.delta, id)
		}
	}
}

// Generation returns the number of completed rebuilds.
func (idx *Index) Generation() uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.generation
}

// DeltaLen returns the number of buffered mutations not yet folded into
// the stable tree. Exposed for tests and monitoring.
func (idx *Index) DeltaLen() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.delta)
}
