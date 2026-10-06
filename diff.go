package recur

// Change describes an instance present in both rule versions whose concrete
// time or adopted revision differs.
type Change struct {
	ID  string
	Old Instance
	New Instance
}

// Diff is the difference between two rule versions over their finite
// horizons, keyed by stable instance identity.
type Diff struct {
	Added   []Instance // in new, not in old
	Removed []Instance // in old, not in new
	Changed []Change   // in both, but start/end/revision differ
}

// DiffRules expands both rules over their finite horizons and compares
// instances by identity (rule ID + week index).
func DiffRules(oldRule, newRule *Rule) Diff {
	oldByID := map[string]Instance{}
	for _, inst := range oldRule.AllInstances() {
		oldByID[inst.ID] = inst
	}
	newByID := map[string]Instance{}
	for _, inst := range newRule.AllInstances() {
		newByID[inst.ID] = inst
	}
	var d Diff
	for id, ni := range newByID {
		oi, ok := oldByID[id]
		if !ok {
			d.Added = append(d.Added, ni)
			continue
		}
		if oi.Start != ni.Start || oi.End != ni.End || oi.Rev != ni.Rev || oi.Source != ni.Source {
			d.Changed = append(d.Changed, Change{ID: id, Old: oi, New: ni})
		}
	}
	for id, oi := range oldByID {
		if _, ok := newByID[id]; !ok {
			d.Removed = append(d.Removed, oi)
		}
	}
	sortInstances(d.Added)
	sortInstances(d.Removed)
	for i := 0; i+1 < len(d.Changed); i++ {
		for j := i + 1; j < len(d.Changed); j++ {
			if d.Changed[j].New.Seq < d.Changed[i].New.Seq {
				d.Changed[i], d.Changed[j] = d.Changed[j], d.Changed[i]
			}
		}
	}
	return d
}

func sortInstances(v []Instance) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j].Seq < v[j-1].Seq; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
