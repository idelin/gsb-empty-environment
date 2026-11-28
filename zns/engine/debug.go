package engine

import (
	"fmt"

	"prescreen.local/ps-3e28dd0313/zns/device"
)

// CheckInvariants cross-validates the logical map, per-zone owner sets and the
// device descriptors. It is intended for tests and post-GC assertions; it
// returns the first inconsistency found or nil when the engine is coherent.
func (e *Engine) CheckInvariants() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	seen := make(map[int]map[int64]struct{})
	for lba, loc := range e.mapping {
		info, err := e.dev.Zone(loc.ZoneID)
		if err != nil {
			return fmt.Errorf("lba %d maps to unknown zone %d: %w", lba, loc.ZoneID, err)
		}
		if loc.Offset < 0 || loc.Offset >= info.ZoneCapacity {
			return fmt.Errorf("lba %d maps outside capacity: %+v", lba, loc)
		}
		owners, ok := e.zoneOwners[loc.ZoneID]
		if !ok {
			return fmt.Errorf("zone %d has no owner set but hosts lba %d", loc.ZoneID, lba)
		}
		if _, ok := owners[lba]; !ok {
			return fmt.Errorf("zone %d owner set missing lba %d", loc.ZoneID, lba)
		}
		if seen[loc.ZoneID] == nil {
			seen[loc.ZoneID] = make(map[int64]struct{})
		}
		if _, dup := seen[loc.ZoneID][lba]; dup {
			return fmt.Errorf("lba %d double counted in zone %d", lba, loc.ZoneID)
		}
		seen[loc.ZoneID][lba] = struct{}{}
	}

	for zoneID, owners := range e.zoneOwners {
		info, err := e.dev.Zone(zoneID)
		if err != nil {
			return fmt.Errorf("owner set for unknown zone %d: %w", zoneID, err)
		}
		if info.State == device.ZoneEmpty && len(owners) != 0 {
			return fmt.Errorf("EMPTY zone %d still has %d owners", zoneID, len(owners))
		}
		if len(owners) > int(info.WrittenBlocks) {
			return fmt.Errorf("zone %d has %d owners but only %d written blocks",
				zoneID, len(owners), info.WrittenBlocks)
		}
		want, ok := seen[zoneID]
		if !ok {
			if len(owners) != 0 {
				return fmt.Errorf("zone %d owner set diverges from global map", zoneID)
			}
			continue
		}
		if len(want) != len(owners) {
			return fmt.Errorf("zone %d owner/map size mismatch %d != %d",
				zoneID, len(owners), len(want))
		}
	}
	return nil
}

// ZoneLiveCount returns the number of live logical blocks currently mapped
// into the given zone.
func (e *Engine) ZoneLiveCount(zoneID int) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.zoneOwners[zoneID])
}
