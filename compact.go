package kvdb

import (
	"os"
	"path/filepath"
	"sort"
)

// Compact merges every sealed segment into one fresh segment, keeping
// only currently visible records: superseded versions are dropped and
// tombstones (whose whole history is being compacted away) vanish
// entirely. The new segment is written to a scratch file, fsynced,
// renamed into place, and only then committed through the MANIFEST, so a
// crash at any point is recoverable. Obsolete input segments are
// unlinked once the last reader pinning them finishes.
func (db *DB) Compact() error {
	db.compactMu.Lock()
	defer db.compactMu.Unlock()
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return ErrClosed
	}
	var victims []*segment
	for _, id := range db.order {
		if id != db.active.id {
			victims = append(victims, db.segments[id])
		}
	}
	if len(victims) == 0 {
		db.mu.Unlock()
		return nil
	}
	newID := db.nextID
	db.nextID++
	db.mu.Unlock()
	victimSet := make(map[uint64]bool, len(victims))
	victimByID := make(map[uint64]*segment, len(victims))
	for _, v := range victims {
		victimSet[v.id] = true
		victimByID[v.id] = v
	}
	db.mu.RLock()
	type pending struct {
		key string
		loc location
	}
	var items []pending
	for k, loc := range db.index {
		if victimSet[loc.segID] {
			items = append(items, pending{k, loc})
		}
	}
	db.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
	tmpPath := filepath.Join(db.dir, segmentFileName(newID)+compactSuffix)
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	cleanup := func(err error) error {
		out.Close()
		os.Remove(tmpPath)
		return err
	}
	type relocation struct {
		key    string
		oldLoc location
		newLoc location
	}
	var moved []relocation
	var off int64
	for _, it := range items {
		if it.loc.tombstone {
			continue // tombstones are dropped by compaction
		}
		rec := make([]byte, it.loc.length)
		if _, err := victimByID[it.loc.segID].file.ReadAt(rec, it.loc.offset); err != nil {
			return cleanup(err)
		}
		if _, _, _, err := decodeRecord(rec); err != nil {
			return cleanup(err)
		}
		if _, err := out.Write(rec); err != nil {
			return cleanup(err)
		}
		moved = append(moved, relocation{
			key:    it.key,
			oldLoc: it.loc,
			newLoc: location{segID: newID, offset: off, length: it.loc.length},
		})
		off += it.loc.length
	}
	if err := out.Sync(); err != nil {
		return cleanup(err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	// Publish the merged segment, then commit. A crash before the commit
	// leaves either the scratch file (cleaned up on recovery) or an
	// uncommitted .seg with id > committed max (kept and harmlessly
	// replayed, then merged away by the next compaction).
	finalPath := filepath.Join(db.dir, segmentFileName(newID))
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := syncDir(db.dir); err != nil {
		return err
	}
	if err := db.inject(InjectCompactBeforeCommit); err != nil {
		return err
	}
	nf, err := os.Open(finalPath)
	if err != nil {
		return err
	}
	newSeg := &segment{id: newID, path: finalPath, file: nf, size: off, sealed: true}
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		nf.Close()
		return ErrClosed
	}
	// Re-point only entries that have not moved since the snapshot;
	// concurrent writes always land in the active segment, so a changed
	// entry simply keeps its newer location.
	for _, mv := range moved {
		if cur, ok := db.index[mv.key]; ok && cur == mv.oldLoc {
			db.index[mv.key] = mv.newLoc
		}
	}
	for k, loc := range db.index {
		if loc.tombstone && victimSet[loc.segID] {
			delete(db.index, k)
		}
	}
	db.segments[newID] = newSeg
	var order []uint64
	for _, id := range db.order {
		if !victimSet[id] {
			order = append(order, id)
		}
	}
	order = append(order, newID)
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	db.order = order
	for _, v := range victims {
		v.obsolete = true
	}
	err = db.writeManifestLocked()
	db.mu.Unlock()
	if err != nil {
		return err
	}
	if err := db.inject(InjectCompactAfterCommit); err != nil {
		return err
	}
	db.mu.Lock()
	for _, v := range victims {
		db.sweepLocked(v)
	}
	db.mu.Unlock()
	return nil
}
