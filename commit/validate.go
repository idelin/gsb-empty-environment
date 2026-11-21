package commit

import (
	"errors"
	"fmt"
)

func validate(inst *Instance) error {
	if inst == nil {
		return errors.New("commit: nil instance")
	}
	if inst.SectorSize <= 0 {
		return fmt.Errorf("commit: invalid sector size %d", inst.SectorSize)
	}
	if inst.K < 0 {
		return fmt.Errorf("commit: negative K %d", inst.K)
	}
	if inst.Cylinders == nil {
		return errors.New("commit: Cylinders map is required")
	}
	if inst.ReadCost == nil {
		return errors.New("commit: ReadCost function is required")
	}
	if len(inst.Targets) == 0 {
		return errors.New("commit: at least one target sector is required")
	}
	dup := map[SectorID]bool{}
	for t := range inst.Targets {
		tgt := &inst.Targets[t]
		if dup[tgt.Address] {
			return fmt.Errorf("commit: duplicate target address %d", tgt.Address)
		}
		dup[tgt.Address] = true
		cyl, ok := inst.Cylinders[tgt.Address]
		if !ok {
			return fmt.Errorf("commit: target %d cylinder unknown", tgt.Address)
		}
		if cyl != tgt.Sector {
			return fmt.Errorf("commit: target %d cylinder mismatch", tgt.Address)
		}
		total := 0
		for f := range tgt.Fragments {
			frag := &tgt.Fragments[f]
			if frag.Length <= 0 {
				return fmt.Errorf("commit: target %d fragment %d has non-positive length", tgt.Address, f)
			}
			total += frag.Length
			if frag.Source == nil {
				if len(frag.Constant) != frag.Length {
					return fmt.Errorf("commit: target %d constant fragment length mismatch", tgt.Address)
				}
				continue
			}
			if _, ok := inst.Cylinders[*frag.Source]; !ok {
				return fmt.Errorf("commit: source %d cylinder unknown", *frag.Source)
			}
			if frag.SourceOffset < 0 || frag.SourceOffset+frag.Length > inst.SectorSize {
				return fmt.Errorf("commit: source %d slice out of range", *frag.Source)
			}
		}
		if total != inst.SectorSize {
			return fmt.Errorf("commit: target %d fragments total %d != sector size %d",
				tgt.Address, total, inst.SectorSize)
		}
	}
	return nil
}
