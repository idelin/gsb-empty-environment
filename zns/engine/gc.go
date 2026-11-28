package engine

import (
	"errors"
	"fmt"

	"prescreen.local/ps-3e28dd0313/zns/device"
)

// GCRun summarizes one garbage collection pass.
type GCRun struct {
	VictimZone   int
	Scanned      int64
	Migrated     int64
	SkippedStale int64
	Reset        bool
}

// zoneValidRatioLocked returns the fraction of programmed blocks in a FULL
// zone that still back live logical blocks. Used to pick the cheapest victim.
func (e *Engine) zoneValidRatioLocked(info device.ZoneInfo) float64 {
	if info.WrittenBlocks == 0 {
		return 1
	}
	owners := e.zoneOwners[info.ID]
	return float64(len(owners)) / float64(info.WrittenBlocks)
}

// pickVictimLocked selects the FULL zone with the lowest live-block ratio.
// hintZone >= 0 forces a specific zone and validates that it is FULL.
func (e *Engine) pickVictimLocked(hintZone int) (device.ZoneInfo, error) {
	if hintZone >= 0 {
		info, err := e.dev.Zone(hintZone)
		if err != nil {
			return device.ZoneInfo{}, err
		}
		if info.State != device.ZoneFull {
			return device.ZoneInfo{}, fmt.Errorf("%w: zone %d is %s, only FULL zones are reclaimable",
				ErrInvalidConfig, hintZone, info.State)
		}
		return info, nil
	}

	best := device.ZoneInfo{}
	bestRatio := 2.0
	found := false
	for _, info := range e.dev.Zones() {
		if info.State != device.ZoneFull {
			continue
		}
		found = true
		ratio := e.zoneValidRatioLocked(info)
		if ratio < bestRatio || (ratio == bestRatio && info.ID < best.ID) {
			bestRatio = ratio
			best = info
		}
	}
	if !found {
		return device.ZoneInfo{}, ErrNothingToGC
	}
	return best, nil
}

// snapshotVictimLocked copies the logical blocks currently mapped into the
// victim zone. Releasing the lock between snapshot and migration is safe because
// every relocation is re-validated with a compare-and-swap against the live map.
func (e *Engine) snapshotVictimLocked(zoneID int) []int64 {
	owners := e.zoneOwners[zoneID]
	out := make([]int64, 0, len(owners))
	for lba := range owners {
		out = append(out, lba)
	}
	return out
}

// GC reclaims one FULL zone. Pass hintZone < 0 to select the cheapest victim
// automatically. The pass is atomic with respect to concurrent overwrites:
// each relocation only lands if the logical block still points at the victim.
func (e *Engine) GC(hintZone int) (GCRun, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.gcLocked(hintZone)
}

func (e *Engine) gcLocked(hintZone int) (GCRun, error) {
	victim, err := e.pickVictimLocked(hintZone)
	if err != nil {
		return GCRun{}, err
	}

	live := e.snapshotVictimLocked(victim.ID)
	run := GCRun{VictimZone: victim.ID, Scanned: int64(len(live))}

	// Migrating the last FULL zone requires an EMPTY destination; if the victim
	// is the only available FULL zone and nothing is empty, fail loudly instead
	// of corrupting data. In normal pools WriteWithGC always preserves headroom.
	for _, lba := range live {
		cur, ok := e.mapping[lba]
		if !ok || cur.ZoneID != victim.ID {
			// A concurrent overwrite (or another GC pass) already moved/replaced
			// this block. Never relocate a stale version.
			run.SkippedStale++
			e.stats.GCBlocksStale++
			if e.hook != nil {
				e.hook(victim.ID, lba, true)
			}
			continue
		}

		// Test seam: fire before any relocation so an interleaved overwrite can
		// race the compare-and-swap below. Production hooks are usually nil.
		if e.hook != nil {
			e.hook(victim.ID, lba, false)
		}

		// Re-validate after the hook: the interleaved write may have changed the
		// authoritative mapping.
		cur, ok = e.mapping[lba]
		if !ok || cur.ZoneID != victim.ID {
			run.SkippedStale++
			e.stats.GCBlocksStale++
			if e.hook != nil {
				e.hook(victim.ID, lba, true)
			}
			continue
		}

		payload, readErr := e.readAtLocked(cur)
		if readErr != nil {
			return run, readErr
		}

		destZone, destHeat, openErr := e.openTargetLocked(cur.Heat, true)
		if openErr != nil {
			return run, openErr
		}
		assigned, appendErr := e.dev.ZoneAppend(destZone, 1, payload)
		if appendErr != nil {
			return run, appendErr
		}
		destInfo, infoErr := e.dev.Zone(destZone)
		if infoErr != nil {
			return run, infoErr
		}
		newLoc := Loc{
			ZoneID: destZone,
			Offset: assigned - destInfo.SLBA,
			Heat:   destHeat,
		}

		// Atomic compare-and-swap on the logical map: only install the migrated
		// location when the block still points at the exact victim location.
		again, ok := e.mapping[lba]
		if !ok || again != cur {
			// Overwrite raced between our read and the append. The just-written
			// destination block is orphaned (flash garbage for a future GC) but
			// the authoritative map keeps the newer version untouched.
			run.SkippedStale++
			e.stats.GCBlocksStale++
			if e.hook != nil {
				e.hook(victim.ID, lba, true)
			}
			continue
		}

		e.mapping[lba] = newLoc
		delete(e.zoneOwners[victim.ID], lba)
		e.zoneOwners[destZone][lba] = struct{}{}
		run.Migrated++
		e.stats.GCMigrations++
		// GC relocations are real flash programs and belong in the WAF numerator.
		e.stats.FlashWrites++
	}

	// Pre-reset safety gate: no live logical block may reference the victim.
	if len(e.zoneOwners[victim.ID]) != 0 {
		return run, fmt.Errorf("%w: zone %d still owns %d blocks",
			ErrGCInvariant, victim.ID, len(e.zoneOwners[victim.ID]))
	}

	if err := e.dev.Reset(victim.ID); err != nil {
		return run, err
	}
	delete(e.zoneOwners, victim.ID)
	delete(e.zoneHeat, victim.ID)
	for _, pool := range e.pools {
		if pool.target == victim.ID {
			pool.target = -1
		}
	}
	run.Reset = true
	e.stats.GCRuns++
	e.stats.ZonesReset++
	e.stats.ActiveMappings = len(e.mapping)
	return run, nil
}

// readAtLocked resolves a mapping to a device payload.
func (e *Engine) readAtLocked(loc Loc) ([]byte, error) {
	info, err := e.dev.Zone(loc.ZoneID)
	if err != nil {
		return nil, err
	}
	return e.dev.Read(loc.absoluteLBA(info.ZoneSize))
}

// GCOnce is a convenience wrapper returning ErrNothingToGC untouched so callers
// can distinguish "nothing to do" from real failures via errors.Is.
func (e *Engine) GCOnce(hintZone int) (GCRun, bool, error) {
	run, err := e.GC(hintZone)
	if errors.Is(err, ErrNothingToGC) {
		return GCRun{}, false, nil
	}
	if err != nil {
		return GCRun{}, false, err
	}
	return run, true, nil
}
