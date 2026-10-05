package schedule

import "sort"

// ChangeKind classifies a diff entry between two rule versions.
type ChangeKind string

const (
	Added   ChangeKind = "added"
	Removed ChangeKind = "removed"
	Changed ChangeKind = "changed"
)

// Change describes what happened to one instance identity between two
// versions of a rule.
type Change struct {
	Kind ChangeKind
	ID   string
	Week int
	Old  *Instance // nil for Added
	New  *Instance // nil for Removed
}

// DiffInstances compares two instance snapshots (keyed by identity) and
// reports added, removed and time-changed instances, ordered by week.
func DiffInstances(a, b []Instance) []Change {
	byA := make(map[string]Instance, len(a))
	for _, inst := range a {
		byA[inst.ID] = inst
	}
	byB := make(map[string]Instance, len(b))
	for _, inst := range b {
		byB[inst.ID] = inst
	}
	var changes []Change
	for id, na := range byA {
		nb, ok := byB[id]
		old := na
		if !ok {
			changes = append(changes, Change{Kind: Removed, ID: id, Week: na.Week, Old: &old})
			continue
		}
		if !na.Start.Equal(nb.Start) || !na.End.Equal(nb.End) {
			newInst := nb
			changes = append(changes, Change{Kind: Changed, ID: id, Week: na.Week, Old: &old, New: &newInst})
		}
	}
	for id, nb := range byB {
		if _, ok := byA[id]; !ok {
			newInst := nb
			changes = append(changes, Change{Kind: Added, ID: id, Week: nb.Week, New: &newInst})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Week != changes[j].Week {
			return changes[i].Week < changes[j].Week
		}
		return changes[i].Kind < changes[j].Kind
	})
	return changes
}
