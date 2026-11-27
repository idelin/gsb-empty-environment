package ftl

import (
	"fmt"
	"sort"
)

type blockInfo struct {
	block   int
	live    int
	invalid int
	erase   uint64
	last    uint64
}

func (d *Device) blockInfosLocked() []blockInfo {
	infos := make([]blockInfo, 0, d.cfg.BlockCount)
	for block := 0; block < d.cfg.BlockCount; block++ {
		if d.wp[block] == 0 {
			continue
		}
		live := 0
		for _, valid := range d.valid[block] {
			if valid {
				live++
			}
		}
		infos = append(infos, blockInfo{
			block:   block,
			live:    live,
			invalid: d.wp[block] - live,
			erase:   d.flash.EraseCount(block),
			last:    d.lastSeq[block],
		})
	}
	return infos
}

func (d *Device) chooseFreeBlockLocked() int {
	best := -1
	var bestErase uint64
	for block := 0; block < d.cfg.BlockCount; block++ {
		if d.wp[block] != 0 {
			continue
		}
		erase := d.flash.EraseCount(block)
		if best < 0 || erase < bestErase || (erase == bestErase && block < best) {
			best = block
			bestErase = erase
		}
	}
	return best
}

// choosePartialBlockLocked returns a block that already has data but still has
// sequential room.  Reusing such a block before a pristine spare prevents
// free pages from being stranded when GC switches the active block.
func (d *Device) choosePartialBlockLocked() int {
	best := -1
	for block := 0; block < d.cfg.BlockCount; block++ {
		if d.wp[block] == 0 || d.wp[block] >= d.cfg.PagesPerBlock {
			continue
		}
		if best < 0 {
			best = block
			continue
		}
		if d.lastSeq[block] > d.lastSeq[best] {
			best = block
			continue
		}
		if d.lastSeq[block] == d.lastSeq[best] &&
			d.flash.EraseCount(block) < d.flash.EraseCount(best) {
			best = block
		}
	}
	return best
}

func (d *Device) eraseBlockLocked(block int) error {
	if err := d.flash.Erase(block); err != nil {
		return err
	}
	for page := 0; page < d.cfg.PagesPerBlock; page++ {
		d.valid[block][page] = false
	}
	d.wp[block] = 0
	d.lastSeq[block] = 0
	if d.active == block {
		d.active = -1
	}
	d.stats.Erases++
	return nil
}

func (d *Device) findDeadBlockLocked() int {
	for block := 0; block < d.cfg.BlockCount; block++ {
		if d.wp[block] == 0 {
			continue
		}
		live := false
		for _, valid := range d.valid[block] {
			if valid {
				live = true
				break
			}
		}
		if !live {
			return block
		}
	}
	return -1
}

func (d *Device) activeHasSpaceLocked() bool {
	return d.active >= 0 && d.active < d.cfg.BlockCount &&
		d.wp[d.active] < d.cfg.PagesPerBlock
}

func (d *Device) gcFeasibleLocked(live int, wear bool) bool {
	if d.activeHasSpaceLocked() && d.wp[d.active]+live <= d.cfg.PagesPerBlock {
		return true
	}
	free := d.freeBlocksLocked()
	if free == 0 {
		return false
	}
	if live < d.cfg.PagesPerBlock {
		return true
	}
	// A fully live cold block can still be migrated for wear leveling.  With
	// one spare we would fill it completely, but the victim becomes the new
	// spare immediately after erase.
	return wear || free > 1
}

func (d *Device) selectVictimLocked() (block int, wear bool, ok bool) {
	infos := d.blockInfosLocked()
	if len(infos) == 0 {
		return -1, false, false
	}
	activeHasSpace := d.activeHasSpaceLocked()
	candidates := infos[:0]
	for _, info := range infos {
		if activeHasSpace && info.block == d.active {
			continue
		}
		candidates = append(candidates, info)
	}
	if len(candidates) == 0 {
		return -1, false, false
	}

	wearUrgent := d.wearDeltaLocked() > d.cfg.WearLevelThreshold
	if wearUrgent {
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].erase != candidates[j].erase {
				return candidates[i].erase < candidates[j].erase
			}
			if candidates[i].last != candidates[j].last {
				return candidates[i].last < candidates[j].last
			}
			return candidates[i].block < candidates[j].block
		})
		for _, info := range candidates {
			if info.live == 0 {
				return info.block, true, true
			}
			if d.gcFeasibleLocked(info.live, true) {
				return info.block, true, true
			}
		}
	}

	best := -1
	bestInvalid := 0
	for index, info := range candidates {
		if info.invalid == 0 || !d.gcFeasibleLocked(info.live, false) {
			continue
		}
		if best < 0 ||
			info.invalid > bestInvalid ||
			(info.invalid == bestInvalid && info.erase < candidates[best].erase) ||
			(info.invalid == bestInvalid && info.erase == candidates[best].erase && info.last < candidates[best].last) {
			best = index
			bestInvalid = info.invalid
		}
	}
	if best >= 0 {
		return candidates[best].block, wearUrgent, true
	}
	return -1, wearUrgent, false
}

// ensureWriteBlockLocked guarantees that d.active points at a block with at
// least one free page, invoking GC before the reserved free-block pool is
// consumed.
func (d *Device) ensureWriteBlockLocked() error {
	for {
		if d.activeHasSpaceLocked() {
			return nil
		}
		if partial := d.choosePartialBlockLocked(); partial >= 0 {
			d.active = partial
			return nil
		}
		d.active = -1
		free := d.freeBlocksLocked()
		if free == 0 {
			progressed, err := d.gcOnceLocked()
			if err != nil {
				return err
			}
			if !progressed {
				return ErrNoSpace
			}
			continue
		}
		if free <= d.cfg.ReservedFreeBlocks {
			progressed, err := d.gcOnceLocked()
			if err != nil {
				return err
			}
			if !progressed {
				return ErrNoSpace
			}
			continue
		}
		block := d.chooseFreeBlockLocked()
		if block < 0 {
			return ErrNoSpace
		}
		d.active = block
		return nil
	}
}

// gcOnceLocked reclaims at most one victim block.  It never erases a block
// until every live page has a checksum-valid copy with a higher seq in the
// destination block.
func (d *Device) gcOnceLocked() (bool, error) {
	// When the erase-count spread is too large, static cold blocks take
	// priority over cheap dead-block reclaim.  Otherwise a hot workload can
	// keep erasing the same hot blocks while cold blocks are never migrated.
	if d.wearDeltaLocked() <= d.cfg.WearLevelThreshold {
		if dead := d.findDeadBlockLocked(); dead >= 0 {
			if err := d.eraseBlockLocked(dead); err != nil {
				return false, err
			}
			d.stats.GCs++
			return true, nil
		}
	}
	if d.freeBlocksLocked() == 0 {
		if dead := d.findDeadBlockLocked(); dead >= 0 {
			if err := d.eraseBlockLocked(dead); err != nil {
				return false, err
			}
			d.stats.GCs++
			return true, nil
		}
		return false, ErrNoSpace
	}

	victim, _, ok := d.selectVictimLocked()
	if !ok {
		// Falling back to a dead block is always safe and may be the only way
		// to make progress if the cold candidate is the current active block.
		if dead := d.findDeadBlockLocked(); dead >= 0 {
			if err := d.eraseBlockLocked(dead); err != nil {
				return false, err
			}
			d.stats.GCs++
			return true, nil
		}
		return false, nil
	}
	infos := d.blockInfosLocked()
	liveCount := 0
	for _, info := range infos {
		if info.block == victim {
			liveCount = info.live
			break
		}
	}
	if liveCount == 0 {
		if err := d.eraseBlockLocked(victim); err != nil {
			return false, err
		}
		d.stats.GCs++
		return true, nil
	}

	target := -1
	if d.activeHasSpaceLocked() && d.active != victim &&
		d.wp[d.active]+liveCount <= d.cfg.PagesPerBlock {
		target = d.active
	} else {
		target = d.chooseFreeBlockLocked()
	}
	if target < 0 {
		return false, ErrNoSpace
	}

	if d.active < 0 || d.active == victim {
		d.active = target
	}

	for page := 0; page < d.cfg.PagesPerBlock; page++ {
		if !d.valid[victim][page] {
			continue
		}
		source := PPA{Block: victim, Page: page}
		data, oob, programmed, err := d.flash.ReadRaw(victim, page)
		if err != nil || !programmed {
			return false, fmt.Errorf("%w: source %s", ErrCorruptMetadata, source)
		}
		meta, ok := decodePageMeta(oob, data, victim, page, d.cfg.PageSize)
		if !ok {
			return false, fmt.Errorf("%w: source %s", ErrCorruptMetadata, source)
		}
		if d.mapping[meta.LBA] != source {
			d.valid[victim][page] = false
			continue
		}
		targetPage := d.wp[target]
		seq := d.nextSeqLocked()
		err = d.programDataLocked(target, targetPage, meta.LBA, seq, meta.Length, data, true)
		if err != nil {
			d.restoreActiveLocked(target)
			return true, err
		}
		destination := PPA{Block: target, Page: targetPage}
		d.mapping[meta.LBA] = destination
		d.mapSeq[meta.LBA] = seq
		d.valid[victim][page] = false
		d.valid[target][targetPage] = true
		d.lastSeq[target] = seq
		d.stats.GCPages++
	}

	if err := d.eraseBlockLocked(victim); err != nil {
		return false, err
	}
	d.restoreActiveLocked(target)
	d.stats.GCs++
	return true, nil
}

func (d *Device) restoreActiveLocked(preferred int) {
	if preferred >= 0 && preferred < d.cfg.BlockCount &&
		d.wp[preferred] < d.cfg.PagesPerBlock {
		d.active = preferred
		return
	}
	d.active = -1
	for block := 0; block < d.cfg.BlockCount; block++ {
		if d.wp[block] >= d.cfg.PagesPerBlock {
			continue
		}
		if d.active < 0 || d.lastSeq[block] > d.lastSeq[d.active] {
			d.active = block
		}
	}
}

// GCOnce reclaims at most one block.
func (d *Device) GCOnce() (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ensurePoweredLocked(); err != nil {
		return false, err
	}
	return d.gcOnceLocked()
}

// CollectGarbage runs GC until no productive victim remains or the optional
// MaxGCPerWrite bound is reached.
func (d *Device) CollectGarbage() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ensurePoweredLocked(); err != nil {
		return err
	}
	limit := d.cfg.MaxGCPerWrite
	if limit <= 0 {
		limit = d.cfg.BlockCount * 2
	}
	for i := 0; i < limit; i++ {
		progressed, err := d.gcOnceLocked()
		if err != nil {
			if err == ErrNoSpace {
				return nil
			}
			return err
		}
		if !progressed {
			return nil
		}
	}
	return nil
}

// RunGC is an alias for CollectGarbage.
func (d *Device) RunGC() error { return d.CollectGarbage() }

// WearLeveling actively migrates static cold blocks until the erase-count
// spread is within the configured threshold or no further progress is
// possible.
func (d *Device) WearLeveling() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ensurePoweredLocked(); err != nil {
		return err
	}
	delta := d.wearDeltaLocked()
	limit := d.cfg.BlockCount * (int(delta) + 2) * 2
	if limit < d.cfg.BlockCount*4 {
		limit = d.cfg.BlockCount * 4
	}
	for i := 0; i < limit; i++ {
		if d.wearDeltaLocked() <= d.cfg.WearLevelThreshold {
			return nil
		}
		progressed, err := d.gcOnceLocked()
		if err != nil {
			if err == ErrNoSpace {
				return nil
			}
			return err
		}
		if !progressed {
			return nil
		}
	}
	return nil
}
