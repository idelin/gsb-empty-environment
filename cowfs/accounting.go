package cowfs

import "fmt"

// blockHolders describes every root that keeps a physical block alive.
//
// A block is held in two distinct ways:
//   - Visible blocks are referenced directly by a snapshot tree or the head.
//   - Dead blocks are retained by exactly one deadlist owner. A block on
//     snapshot S_i's deadlist died between S_{i-1} and S_i, so S_i is the
//     newest observer while every older snapshot whose txg >= birth keeps
//     observing it. The active head deadlist is owned by the newest snapshot.
func (e *Engine) blockHolders() map[PhysID]map[uint64]struct{} {
	holders := make(map[PhysID]map[uint64]struct{})

	mark := func(id PhysID, holder uint64) {
		set, ok := holders[id]
		if !ok {
			set = make(map[uint64]struct{})
			holders[id] = set
		}
		set[holder] = struct{}{}
	}

	// Visible blocks: direct tree references.
	for _, blocks := range e.head {
		for _, ptr := range blocks {
			mark(ptr.ID, headOwner)
		}
	}
	for _, snap := range e.snaps {
		for _, blocks := range snap.tree {
			for _, ptr := range blocks {
				mark(ptr.ID, snap.id)
			}
		}
	}

	// Dead blocks: a block on snapshot S_i's deadlist died between S_{i-1} and
	// S_i, so S_{i-1} (the previous snapshot) is its last observer and retains
	// it. Blocks on the active head deadlist died after the newest snapshot and
	// are retained by that newest snapshot.
	for _, snap := range e.snaps {
		for id := range snap.dead {
			if idx := e.indexOfSnapshot(snap); idx > 0 {
				mark(id, e.snaps[idx-1].id)
			} else {
				// The oldest snapshot's deadlist holds blocks no surviving
				// snapshot can see; they are reclaimed when it is deleted and
				// are attributable to no remaining snapshot.
				mark(id, snap.id)
			}
		}
	}
	if len(e.snaps) > 0 {
		newest := e.snaps[len(e.snaps)-1]
		for id := range e.headDead {
			mark(id, newest.id)
		}
	}

	return holders
}

// blockSizeOf returns the recorded size for a block held by a snapshot,
// preferring a live tree pointer and falling back to the store.
func (e *Engine) blockSizeOf(id PhysID) int {
	if b, ok := e.store.Read(id); ok {
		return b.Size
	}
	for _, snap := range e.snaps {
		for _, blocks := range snap.tree {
			for _, ptr := range blocks {
				if ptr.ID == id {
					return ptr.Size
				}
			}
		}
		if entry, ok := snap.dead[id]; ok {
			return entry.size
		}
	}
	if entry, ok := e.headDead[id]; ok {
		return entry.size
	}
	return 0
}

// computeUniqueUsed returns the number of bytes kept alive exclusively by this
// snapshot. It is the exact amount of physical space that deleting the snapshot
// can reclaim: blocks whose only holder is the snapshot itself.
func (e *Engine) computeUniqueUsed(snap *Snapshot) int {
	holders := e.blockHolders()
	unique := 0
	for id, set := range holders {
		if len(set) == 1 {
			if _, only := set[snap.id]; only {
				unique += e.blockSizeOf(id)
			}
		}
	}
	return unique
}

// Validate recomputes every accounting invariant from first principles and
// compares it against the maintained state. It is O(total blocks) and intended
// for tests and diagnostics.
func (e *Engine) Validate() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	bruteReferenced := make(map[uint64]int)
	for _, snap := range e.snaps {
		ref := 0
		for _, blocks := range snap.tree {
			for _, ptr := range blocks {
				ref += ptr.Size
			}
		}
		bruteReferenced[snap.id] = ref
		if snap.referenced != ref {
			return fmt.Errorf("cowfs: snapshot %q referenced %d != %d",
				snap.name, snap.referenced, ref)
		}
	}

	holders := e.blockHolders()
	bruteUnique := make(map[uint64]int)
	for id, set := range holders {
		if len(set) == 1 {
			var sole uint64
			for h := range set {
				sole = h
			}
			if sole != headOwner {
				bruteUnique[sole] += e.blockSizeOf(id)
			}
		}
	}

	for _, snap := range e.snaps {
		want := bruteUnique[snap.id]
		got := e.computeUniqueUsed(snap)
		if got != want {
			return fmt.Errorf("cowfs: snapshot %q recomputed unique %d != %d",
				snap.name, got, want)
		}
		if got < 0 {
			return fmt.Errorf("cowfs: snapshot %q negative unique_used", snap.name)
		}
	}

	if err := e.validateDeadlists(); err != nil {
		return err
	}
	if err := e.validatePhysicalFootprint(); err != nil {
		return err
	}
	return e.arc.CheckInvariants()
}

// validateDeadlists ensures deadlisted blocks stay physically live and carry
// the birth txg and size recorded at displacement time. A deadlisted block may
// still be reachable from snapshots older than the deadlist boundary; that is
// precisely why it cannot be freed yet.
func (e *Engine) validateDeadlists() error {
	checkList := func(where string, dead map[PhysID]deadEntry) error {
		for id, entry := range dead {
			b, ok := e.store.Read(id)
			if !ok {
				return fmt.Errorf("cowfs: %s deadlist references freed block %d",
					where, id)
			}
			if b.BirthTxg != entry.birthTxg || b.Size != entry.size {
				return fmt.Errorf("cowfs: deadlist metadata mismatch for block %d", id)
			}
		}
		return nil
	}

	for _, snap := range e.snaps {
		if err := checkList("snapshot "+snap.name, snap.dead); err != nil {
			return err
		}
	}
	return checkList("head", e.headDead)
}

// validatePhysicalFootprint guarantees zero leaks: every live physical block is
// either reachable from a retained root or held on exactly one deadlist.
func (e *Engine) validatePhysicalFootprint() error {
	accounted := make(map[PhysID]struct{})

	for _, snap := range e.snaps {
		for _, blocks := range snap.tree {
			for _, ptr := range blocks {
				accounted[ptr.ID] = struct{}{}
			}
		}
	}
	for _, blocks := range e.head {
		for _, ptr := range blocks {
			accounted[ptr.ID] = struct{}{}
		}
	}

	deadSeen := make(map[PhysID]struct{})
	countDead := func(dead map[PhysID]deadEntry) error {
		for id := range dead {
			if _, dup := deadSeen[id]; dup {
				return fmt.Errorf("cowfs: block %d present on multiple deadlists", id)
			}
			deadSeen[id] = struct{}{}
			accounted[id] = struct{}{}
		}
		return nil
	}
	for _, snap := range e.snaps {
		if err := countDead(snap.dead); err != nil {
			return err
		}
	}
	if err := countDead(e.headDead); err != nil {
		return err
	}

	liveCount := e.store.LiveBlocks()
	if len(accounted) != liveCount {
		return fmt.Errorf("cowfs: physical leak, accounted blocks %d != live %d",
			len(accounted), liveCount)
	}

	bytes := 0
	for id := range accounted {
		b, ok := e.store.Read(id)
		if !ok {
			return fmt.Errorf("cowfs: accounted block %d is not live", id)
		}
		bytes += b.Size
	}
	if bytes != e.store.AllocatedBytes() {
		return fmt.Errorf("cowfs: accounted bytes %d != allocated %d",
			bytes, e.store.AllocatedBytes())
	}
	return nil
}
