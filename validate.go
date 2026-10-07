package retention

import "fmt"

// Validate checks a retention report against the recovery graph.
// It returns one error per violation found; an empty slice means the
// report is consistent and safe to execute. Checks:
//
//  1. Every backup is accounted for exactly once (kept XOR delete set).
//  2. Every kept backup is a restorable recovery point or a dependency
//     of one (kept backups must have intact, uncorrupted chains).
//  3. The delete set preserves the full dependency chain of every kept
//     backup (no kept backup loses a dependency to deletion).
//  4. Every selected recovery point carries at least one rule rationale.
func (g *Graph) Validate(rep *Report) []error {
	var errs []error

	kept := make(map[string]bool)
	for _, d := range rep.Decisions {
		if d.Kept {
			kept[d.Backup.ID] = true
		}
		if d.Selected && len(d.Reasons) == 0 {
			errs = append(errs, fmt.Errorf("selected backup %q has no selection rationale", d.Backup.ID))
		}
		if d.Kept && d.Unavailable != "" {
			errs = append(errs, fmt.Errorf("kept backup %q is unavailable: %s", d.Backup.ID, d.Unavailable))
		}
	}

	deleted := make(map[string]bool)
	for _, id := range rep.DeleteSet {
		if deleted[id] {
			errs = append(errs, fmt.Errorf("delete set contains duplicate %q", id))
		}
		deleted[id] = true
	}

	for _, b := range g.Backups() {
		switch {
		case kept[b.ID] && deleted[b.ID]:
			errs = append(errs, fmt.Errorf("backup %q is both kept and deleted", b.ID))
		case !kept[b.ID] && !deleted[b.ID]:
			errs = append(errs, fmt.Errorf("backup %q is neither kept nor deleted", b.ID))
		}
	}

	for id := range kept {
		chain, err := g.Chain(id)
		if err != nil {
			errs = append(errs, fmt.Errorf("kept backup %q is not restorable: %v", id, err))
			continue
		}
		for _, dep := range chain {
			if deleted[dep.ID] {
				errs = append(errs, fmt.Errorf("kept backup %q loses dependency %q to the delete set", id, dep.ID))
			}
		}
	}
	return errs
}
