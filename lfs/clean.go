package lfs

import "sort"

// Candidate is a non-active segment considered for cleaning, with its
// benefit-age ranking inputs.
type Candidate struct {
	SegID int
	// Used is the number of occupied physical blocks.
	Used int
	// Live is the number of occupied blocks still referenced by NAT/snapshots.
	Live int
	// Utilization u = Live / B.
	Utilization float64
	// AgeNs is the age of the segment's youngest block.
	AgeNs int64
	// Score is ((1-u)*age)/(1+u). Fully live segments score 0 and are skipped.
	Score float64
}

// CleanStats reports what one cleaning pass did.
type CleanStats struct {
	// ReclaimedEmpty are empty segments (u == 0) recycled without migration.
	ReclaimedEmpty int
	// Victim is the migrated segment id, -1 when no migration happened.
	Victim int
	// Migrated is the number of live blocks moved to hot/cold targets.
	Migrated int
	// MigratedHot / MigratedCold split migrated blocks by temperature class.
	MigratedHot  int
	MigratedCold int
	// FreedSegment is true when the victim was returned to the free pool.
	FreedSegment bool
	// Emergency is true when the pass ran at/below the emergency watermark.
	Emergency bool
}

// benefitAgeScore implements score = ((1-u)*age)/(1+u).
func benefitAgeScore(utilization float64, age int64) float64 {
	if utilization >= 1.0 {
		return 0
	}
	return ((1.0 - utilization) * float64(age)) / (1.0 + utilization)
}

// RankCandidates scores every non-active, non-empty segment in descending
// benefit-age order (ties broken by lower segment id for determinism).
func (fs *FS) RankCandidates() []Candidate {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.rankCandidatesLocked()
}

func (fs *FS) rankCandidatesLocked() []Candidate {
	live := fs.liveAddrs()
	var out []Candidate
	for id, seg := range fs.segments {
		if seg.Active {
			continue
		}
		used := 0
		liveCount := 0
		for slot := 0; slot < fs.blocks; slot++ {
			addr := PhysAddr{id, slot}
			if !fs.dev.BlockOccupied(addr) {
				continue
			}
			used++
			if live[addr] {
				liveCount++
			}
		}
		if used == 0 {
			continue // empty segments are recycled directly, never ranked
		}
		u := float64(liveCount) / float64(fs.blocks)
		out = append(out, Candidate{
			SegID:       id,
			Used:        used,
			Live:        liveCount,
			Utilization: u,
			AgeNs:       fs.segmentAge(seg),
			Score:       benefitAgeScore(u, fs.segmentAge(seg)),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		// Among equal scores (the all-zero edge at age 0), a zero-live segment
		// is immediately reclaimable while a fully-live segment is hopeless;
		// rank the reclaimable one first so cleaning never wedges.
		if (out[i].Live == 0) != (out[j].Live == 0) {
			return out[i].Live == 0
		}
		return out[i].SegID < out[j].SegID
	})
	return out
}

// reclaimEmptyLocked immediately returns every non-active empty segment to the
// free pool. Empty segments need no block migration at all.
func (fs *FS) reclaimEmptyLocked(stats *CleanStats) {
	freeSet := make(map[int]bool, len(fs.freeSegs))
	for _, id := range fs.freeSegs {
		freeSet[id] = true
	}
	for id, seg := range fs.segments {
		if seg.Active || freeSet[id] {
			continue
		}
		if fs.usedSlots(seg) == 0 {
			fs.dev.resetSegment(id)
			fs.segments[id] = fs.newSegment(id)
			fs.depositFreed(id)
			stats.ReclaimedEmpty++
			freeSet[id] = true
		}
	}
}

// Clean performs one cleaning pass: empty segments are reclaimed for free, then
// the highest benefit-age non-active segment has its live blocks migrated to
// fresh hot/cold target segments with atomic NAT/snapshot repointing.
func (fs *FS) Clean() (CleanStats, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.cleanLocked()
}

// CleanUntilStable repeatedly runs cleaning passes until no further progress
// is possible (no reclaimable or migratable non-active segment remains).
// The loop is bounded by the segment count, so it can never spin forever.
func (fs *FS) CleanUntilStable() (int, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	passes := 0
	for attempt := 0; attempt <= len(fs.segments); attempt++ {
		stats, err := fs.cleanLocked()
		if err != nil {
			if err == ErrNoCandidate {
				return passes, nil
			}
			return passes, err
		}
		madeProgress := stats.ReclaimedEmpty > 0 || stats.FreedSegment
		if !madeProgress {
			return passes, nil
		}
		passes++
	}
	return passes, nil
}

func (fs *FS) cleanLocked() (CleanStats, error) {
	stats := CleanStats{Victim: -1}
	fs.reclaimEmptyLocked(&stats)

	candidates := fs.rankCandidatesLocked()
	if len(candidates) == 0 {
		if len(fs.freeSegs) == 0 {
			return stats, ErrNoSpace
		}
		if stats.ReclaimedEmpty > 0 {
			return stats, nil
		}
		return stats, ErrNoCandidate
	}

	best := candidates[0]
	// A segment with zero live blocks (u == 0) needs no migration regardless of
	// its score (the age factor can be 0 for a just-sealed segment). Reclaim it
	// immediately; this is what guarantees forward progress under pressure.
	if best.Live == 0 {
		for _, c := range candidates {
			if c.Live != 0 {
				continue
			}
			fs.dev.resetSegment(c.SegID)
			fs.segments[c.SegID] = fs.newSegment(c.SegID)
			fs.depositFreed(c.SegID)
			stats.ReclaimedEmpty++
		}
		return stats, nil
	}
	if best.Score <= 0 {
		// Every occupied candidate is fully live (u == 1.0 => score 0): no
		// garbage exists anywhere, so cleaning genuinely cannot free anything.
		if len(fs.freeSegs) == 0 {
			return stats, ErrNoSpace
		}
		if stats.ReclaimedEmpty > 0 {
			return stats, nil
		}
		return stats, ErrNoCandidate
	}

	emergency := len(fs.freeSegs) <= fs.emergWm
	stats.Emergency = emergency

	if err := fs.migrateSegmentLocked(best.SegID, &stats); err != nil {
		return stats, err
	}
	return stats, nil
}

// migration records one live-block relocation so NAT and every snapshot can be
// repointed atomically after all target writes have succeeded.
type migration struct {
	oldAddr PhysAddr
	newAddr PhysAddr
	hot     bool
	ino     NodeID
	blkIdx  BlockIndex
}

func (fs *FS) migrateSegmentLocked(segID int, stats *CleanStats) error {
	live := fs.liveAddrs()
	var moves []migration
	affectedInodes := map[NodeID]bool{}

	for slot := 0; slot < fs.blocks; slot++ {
		oldAddr := PhysAddr{segID, slot}
		if !fs.dev.BlockOccupied(oldAddr) {
			continue
		}
		node, err := fs.dev.ReadNode(oldAddr)
		if err != nil {
			return err
		}
		// Fsync markers and dead blocks are simply dropped; markers are
		// re-emitted below for inodes that still need durability.
		if node.Kind != KindData || !live[oldAddr] {
			continue
		}
		newAddr, err := fs.allocBlockMode(node.Hot, true)
		if err != nil {
			return err
		}
		moved := Node{
			Kind:   KindData,
			Seq:    fs.nextSeq,
			Ver:    node.Ver,
			Ino:    node.Ino,
			BlkIdx: node.BlkIdx,
			Hot:    node.Hot,
			TimeNs: fs.clock(),
			Data:   append([]byte(nil), node.Data...),
		}
		fs.nextSeq++
		if err := fs.dev.WriteNode(newAddr, moved); err != nil {
			return err
		}
		target := fs.segments[newAddr.Seg]
		target.Summary[newAddr.Slot] = Summary{
			Ino: node.Ino, BlkIdx: node.BlkIdx, Ver: node.Ver, Hot: node.Hot,
		}
		target.WriteTimeNs[newAddr.Slot] = moved.TimeNs
		fs.sealIfFull(newAddr.Seg)
		moves = append(moves, migration{
			oldAddr: oldAddr, newAddr: newAddr, hot: node.Hot,
			ino: node.Ino, blkIdx: node.BlkIdx,
		})
		affectedInodes[node.Ino] = true
		if node.Hot {
			stats.MigratedHot++
		} else {
			stats.MigratedCold++
		}
	}

	// Re-emit fsync commit markers for migrated inodes whose durability extends
	// past the latest checkpoint, so a post-clean crash still replays their
	// acknowledged fsync chains instead of resurrecting nothing.
	for ino := range affectedInodes {
		if fs.lastSync[ino] > fs.cpLastSeq {
			if _, err := fs.fsyncMode(ino, true); err != nil {
				return err
			}
		}
	}

	// Atomic pointer switch: only after every target block is durably written.
	for _, mv := range moves {
		key := NATKey{Ino: mv.ino, BlkIdx: mv.blkIdx}
		if entry, ok := fs.nat[key]; ok && entry.Addr == mv.oldAddr {
			entry.Addr = mv.newAddr
			fs.nat[key] = entry
		}
		for _, snap := range fs.snaps {
			for k, entry := range snap.Mapping {
				if entry.Addr == mv.oldAddr {
					entry.Addr = mv.newAddr
					snap.Mapping[k] = entry
				}
			}
		}
	}

	// Reclaim the victim only after all live blocks are reachable elsewhere.
	fs.dev.resetSegment(segID)
	fs.segments[segID] = fs.newSegment(segID)
	fs.depositFreed(segID)

	stats.Victim = segID
	stats.Migrated = len(moves)
	stats.FreedSegment = true
	return nil
}
