package zplanner

import "fmt"

// ValidatePlan replays plan against the device rules and reports the first
// violation. It also checks the recorded Cost against the replayed cost.
func ValidatePlan(init InitialState, ops []Op, plan *CommitPlan) error {
	if plan == nil {
		return fmt.Errorf("zplanner: nil plan")
	}
	fill := init.Fill
	loc := cloneLoc(init.Table.Loc)
	roots := cloneRoots(init.Table.Roots)
	snaps := cloneSnaps(init.Table.Snaps)
	staged := map[CID]Phys{}
	gen := init.Table.Generation
	lastOp := init.Table.LastOp
	opIdx := 0
	var cost Cost

	pinSet := func() map[ZoneID]bool {
		out := map[ZoneID]bool{}
		for _, p := range loc {
			out[p.Zone] = true
		}
		for _, p := range staged {
			out[p.Zone] = true
		}
		return out
	}

	for si, step := range plan.Steps {
		switch step.Kind {
		case StepWrite, StepCopy:
			if step.Zone >= MaxZones {
				return fmt.Errorf("step %d: bad zone", si)
			}
			if fill[step.Zone] >= PagesPerZone {
				return fmt.Errorf("step %d: append beyond zone capacity", si)
			}
			if _, dup := staged[step.CID]; dup {
				return fmt.Errorf("step %d: duplicate staged CID %d", si, step.CID)
			}
			if step.Kind == StepCopy {
				p, ok := loc[step.CID]
				if !ok {
					return fmt.Errorf("step %d: copy of unknown CID %d", si, step.CID)
				}
				if p.Zone == step.Zone {
					return fmt.Errorf("step %d: copy within same zone", si)
				}
				cost.Copies++
			}
			staged[step.CID] = Phys{Zone: step.Zone, Off: uint8(fill[step.Zone])}
			fill[step.Zone]++
		case StepReset:
			if pinSet()[step.Zone] {
				return fmt.Errorf("step %d: reset of pinned zone %d", si, step.Zone)
			}
			if fill[step.Zone] == 0 {
				return fmt.Errorf("step %d: reset of empty zone", si)
			}
			fill[step.Zone] = 0
			cost.Erases++
		case StepPublish:
			gen++
			if step.Table == nil || step.Table.Generation != gen {
				return fmt.Errorf("step %d: bad table generation", si)
			}
			for id, p := range staged {
				loc[id] = p
			}
			staged = map[CID]Phys{}
			if step.OpID == -1 {
				loc = pruneLoc(loc, referenced(roots, snaps))
				if !sameTable(loc, roots, snaps, step.Table) {
					return fmt.Errorf("step %d: relocation publish changed logical state", si)
				}
				if step.Table.LastOp != lastOp {
					return fmt.Errorf("step %d: relocation publish moved lastOp", si)
				}
				continue
			}
			if opIdx >= len(ops) || ops[opIdx].ID != step.OpID {
				return fmt.Errorf("step %d: unexpected client publish", si)
			}
			op := ops[opIdx]
			switch op.Kind {
			case OpWrite:
				for page, id := range op.Writes {
					if _, ok := loc[id]; !ok {
						return fmt.Errorf("step %d: write of unpublished CID", si)
					}
					roots[page] = id
				}
			case OpSnapshot:
				cp := map[string]CID{}
				for page, id := range roots {
					cp[page] = id
				}
				snaps[op.Name] = cp
			case OpDeleteSnapshot:
				delete(snaps, op.Name)
			}
			for id, p := range staged {
				loc[id] = p
			}
			staged = map[CID]Phys{}
			loc = pruneLoc(loc, referenced(roots, snaps))
			if !sameTable(loc, roots, snaps, step.Table) {
				return fmt.Errorf("step %d: published table mismatch", si)
			}
			if step.Table.LastOp != op.ID {
				return fmt.Errorf("step %d: bad LastOp", si)
			}
			lastOp = op.ID
			opIdx++
		default:
			return fmt.Errorf("step %d: unknown kind", si)
		}
	}
	if opIdx != len(ops) {
		return fmt.Errorf("zplanner: plan finalized only %d of %d operations", opIdx, len(ops))
	}
	if cost != plan.Cost {
		return fmt.Errorf("zplanner: recorded cost %+v != replayed %+v", plan.Cost, cost)
	}
	return nil
}

func sameTable(loc map[CID]Phys, roots map[string]CID, snaps map[string]map[string]CID, t *Table) bool {
	if len(loc) != len(t.Loc) || len(roots) != len(t.Roots) || len(snaps) != len(t.Snaps) {
		return false
	}
	for id, p := range loc {
		if t.Loc[id] != p {
			return false
		}
	}
	for page, id := range roots {
		if t.Roots[page] != id {
			return false
		}
	}
	for name, snap := range snaps {
		ts, ok := t.Snaps[name]
		if !ok || len(ts) != len(snap) {
			return false
		}
		for page, id := range snap {
			if ts[page] != id {
				return false
			}
		}
	}
	return true
}
