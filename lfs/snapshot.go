package lfs

// CreateSnapshot pins the current NAT generation under name. Historical
// physical versions referenced by the snapshot stay alive even after the NAT
// moves on, until the snapshot is deleted.
func (fs *FS) CreateSnapshot(name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if name == "" {
		return ErrInvalidArgument
	}
	if _, exists := fs.snaps[name]; exists {
		return ErrSnapshotExists
	}
	mapping := make(map[NATKey]NATEntry, len(fs.nat))
	for key, entry := range fs.nat {
		mapping[key] = entry
	}
	fs.snaps[name] = &Snapshot{Name: name, Mapping: mapping}
	return nil
}

// DeleteSnapshot releases a snapshot. Blocks that were kept alive solely by
// this snapshot immediately become reclaimable garbage on the next clean.
func (fs *FS) DeleteSnapshot(name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, ok := fs.snaps[name]; !ok {
		return ErrSnapshotNotFound
	}
	delete(fs.snaps, name)
	return nil
}

// SnapshotEntry returns the physical version pinned by a snapshot for a
// logical block.
func (fs *FS) SnapshotEntry(name string, ino NodeID, blkIdx BlockIndex) (NATEntry, bool, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	snap, ok := fs.snaps[name]
	if !ok {
		return NATEntry{}, false, ErrSnapshotNotFound
	}
	entry, found := snap.Mapping[NATKey{ino, blkIdx}]
	return entry, found, nil
}

// AuditReport is the result of a whole-filesystem block conservation check.
type AuditReport struct {
	OccupiedBlocks int // frames physically present on device
	LiveBlocks     int // blocks reachable from NAT or an active snapshot
	DeadBlocks     int // occupied but unreachable: garbage awaiting cleaning
	NATBlocks      int // distinct addresses referenced by the NAT
	SnapshotBlocks int // distinct addresses referenced by snapshots
	FreeSegments   int // segments in the free pool
	Consistent     bool
}

// Audit verifies block conservation: occupied == live + dead, every NAT/snapshot
// pointer resolves to an occupied frame, and free segments are truly empty.
func (fs *FS) Audit() AuditReport {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	report := AuditReport{FreeSegments: len(fs.freeSegs) + len(fs.reserve)}
	referenced := map[PhysAddr]bool{}

	for _, entry := range fs.nat {
		referenced[entry.Addr] = true
	}
	report.NATBlocks = len(referenced)

	snapAddrs := map[PhysAddr]bool{}
	for _, snap := range fs.snaps {
		for _, entry := range snap.Mapping {
			snapAddrs[entry.Addr] = true
			referenced[entry.Addr] = true
		}
	}
	report.SnapshotBlocks = len(snapAddrs)
	report.LiveBlocks = len(referenced)

	for seg := 0; seg < len(fs.segments); seg++ {
		for slot := 0; slot < fs.blocks; slot++ {
			addr := PhysAddr{seg, slot}
			if !fs.dev.BlockOccupied(addr) {
				continue
			}
			report.OccupiedBlocks++
			if !referenced[addr] {
				report.DeadBlocks++
			}
		}
	}

	consistent := report.OccupiedBlocks == report.LiveBlocks+report.DeadBlocks
	for addr := range referenced {
		if !fs.dev.BlockOccupied(addr) {
			consistent = false // dangling pointer
		}
	}
	freeSet := map[int]bool{}
	for _, id := range append(append([]int(nil), fs.freeSegs...), fs.reserve...) {
		if freeSet[id] {
			consistent = false // segment free-listed twice
		}
		freeSet[id] = true
		if fs.usedSlots(fs.segments[id]) != 0 {
			consistent = false // free segment still holds data
		}
	}
	report.Consistent = consistent
	return report
}
