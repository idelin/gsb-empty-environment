package readrepair

// Version is a single versioned value (or tombstone) of a key.
type Version struct {
	Value     string
	Clock     VClock
	Tombstone bool
}

// mergeVersions combines two version sets, keeping every version that is not
// strictly dominated by another. Concurrent versions are all preserved as
// siblings; duplicates (identical clocks) are collapsed deterministically,
// preferring tombstones so a delete is never silently dropped.
func mergeVersions(a, b []Version) []Version {
	all := make([]Version, 0, len(a)+len(b))
	all = append(all, a...)
	all = append(all, b...)

	kept := make([]Version, 0, len(all))
	for i, v := range all {
		dominated := false
		duplicate := false
		for j, w := range all {
			if i == j {
				continue
			}
			switch v.Clock.Compare(w.Clock) {
			case -1: // w strictly dominates v
				dominated = true
			case 0: // same clock: keep a single canonical representative
				if j < i || (w.Tombstone && !v.Tombstone) ||
					(w.Tombstone == v.Tombstone && w.Value < v.Value) {
					duplicate = true
				}
			}
			if dominated || duplicate {
				break
			}
		}
		if !dominated && !duplicate {
			kept = append(kept, v)
		}
	}
	return kept
}
