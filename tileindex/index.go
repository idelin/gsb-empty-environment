package tileindex

import (
	"sync"
	"sync/atomic"
)

// Snapshot is an immutable view of the available tile set. Snapshots are
// safe for concurrent use and stay valid forever: updating the Index never
// mutates a previously obtained Snapshot.
type Snapshot struct {
	tiles map[TileID]struct{}
}

// Has reports whether tile t is available in this snapshot.
func (s *Snapshot) Has(t TileID) bool {
	_, ok := s.tiles[t]
	return ok
}

// Len returns the number of available tiles.
func (s *Snapshot) Len() int { return len(s.tiles) }

// Cover resolves tile t against this snapshot. If t itself is available,
// it returns t with the full [0,1]x[0,1] interval. Otherwise it walks up
// the ancestor chain level by level (parent = halved coordinates) until
// an available ancestor is found, and returns that ancestor together with
// the exact rational sub-interval of its texture that covers t; the caller
// must clip the ancestor texture to this interval.
//
// If no ancestor (including the root) is available, ok is false and the
// caller must render nothing — a neighbouring tile is never substituted
// for a missing parent.
func (s *Snapshot) Cover(t TileID) (ancestor TileID, clip Rect, ok bool) {
	if !t.Valid() {
		return TileID{}, Rect{}, false
	}
	cur := t
	for {
		if s.Has(cur) {
			return cur, interval(t, cur), true
		}
		parent, has := cur.Parent()
		if !has {
			return TileID{}, Rect{}, false
		}
		cur = parent
	}
}

// interval computes the exact sub-rectangle of ancestor's texture that
// covers the descendant tile t, using the coordinate scaling formula.
// With shift = t.Z - ancestor.Z and scale = 2^shift:
//
//	u0 = (t.X - ancestor.X*scale) / scale
//	u1 = u0 + 1/scale
//
// and likewise for v. The result is exact; boundary tiles land exactly on
// 0 and 1.
func interval(t, ancestor TileID) Rect {
	shift := uint(t.Z - ancestor.Z)
	scale := int64(1) << shift
	u0 := int64(t.X) - int64(ancestor.X)*scale
	v0 := int64(t.Y) - int64(ancestor.Y)*scale
	return Rect{
		U0: NewRat(u0, scale),
		V0: NewRat(v0, scale),
		U1: NewRat(u0+1, scale),
		V1: NewRat(v0+1, scale),
	}
}

// Index holds the live available-tile set. Reads are lock-free; updates
// are serialised and swapped in atomically, so readers either see the
// whole old set or the whole new set, never a mixture.
type Index struct {
	snap atomic.Value // stores *Snapshot
	mu   sync.Mutex   // serialises Update
}

// NewIndex returns an Index containing exactly the given tiles.
// Invalid tile identifiers are ignored.
func NewIndex(tiles ...TileID) *Index {
	idx := &Index{}
	idx.snap.Store(&Snapshot{tiles: toSet(tiles)})
	return idx
}

// Snapshot returns the current immutable snapshot. The result stays valid
// and unchanged even after later Update calls.
func (idx *Index) Snapshot() *Snapshot {
	return idx.snap.Load().(*Snapshot)
}

// Update atomically replaces the available set with the current set plus
// add minus remove, and returns the new snapshot. Snapshots obtained
// before the call are unaffected.
func (idx *Index) Update(add, remove []TileID) *Snapshot {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	old := idx.Snapshot()
	merged := make(map[TileID]struct{}, len(old.tiles)+len(add))
	for t := range old.tiles {
		merged[t] = struct{}{}
	}
	for _, t := range add {
		if t.Valid() {
			merged[t] = struct{}{}
		}
	}
	for _, t := range remove {
		delete(merged, t)
	}
	next := &Snapshot{tiles: merged}
	idx.snap.Store(next)
	return next
}

func toSet(tiles []TileID) map[TileID]struct{} {
	set := make(map[TileID]struct{}, len(tiles))
	for _, t := range tiles {
		if t.Valid() {
			set[t] = struct{}{}
		}
	}
	return set
}
