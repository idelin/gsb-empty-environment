package peering

// BuildPastIntervals reconstructs the PastInterval history from an ordered
// sequence of OSDMap epochs.
//
// Consecutive epochs whose up set, acting set and min_size are identical are
// merged into one interval, mirroring Ceph's interval bookkeeping. An interval
// is authoritative writable (MaybeWentRW) only when it had reached active
// state and its acting set satisfied min_size. Those are the only intervals
// that could have accepted client-acknowledged writes and therefore the only
// ones that can later block peering when all of their members are gone.
func BuildPastIntervals(epochs []OSDMapEpoch) []PastInterval {
	var intervals []PastInterval
	for _, epoch := range epochs {
		if len(intervals) == 0 || !sameIntervalShape(intervals[len(intervals)-1], epoch) {
			intervals = append(intervals, PastInterval{
				First:   epoch.Epoch,
				Last:    epoch.Epoch,
				Up:      copyIDs(epoch.Up),
				Acting:  copyIDs(epoch.Acting),
				MinSize: epoch.MinSize,
				Active:  epoch.Active,
			})
		} else {
			intervals[len(intervals)-1].Last = epoch.Epoch
			intervals[len(intervals)-1].Active =
				intervals[len(intervals)-1].Active && epoch.Active
		}
	}

	for i := range intervals {
		intervals[i].MaybeWentRW =
			intervals[i].Active && len(intervals[i].Acting) >= intervals[i].MinSize
	}
	return intervals
}

func sameIntervalShape(interval PastInterval, epoch OSDMapEpoch) bool {
	return interval.MinSize == epoch.MinSize &&
		setsEqual(interval.Up, epoch.Up) &&
		setsEqual(interval.Acting, epoch.Acting)
}

// FindUnwitnessedInterval returns the first MaybeWentRW interval for which
// none of the acting members answered the current peering probe.
//
// queried is the set of OSDs that successfully responded to the primary's log
// probe. A writable interval without any reachable witness means a write may
// have been acknowledged and then lost with every replica, so peering must
// stop Incomplete rather than risk serving stale data or reusing a version.
// It returns nil when every writable interval has at least one witness.
func FindUnwitnessedInterval(intervals []PastInterval, queried []OSDID) *PastInterval {
	witnesses := osdSet(queried)
	for i := range intervals {
		interval := &intervals[i]
		if interval.MaybeWentRW && !intersects(interval.Acting, witnesses) {
			snapshot := *interval
			return &snapshot
		}
	}
	return nil
}

// buildProbeSet is the universe of OSDs the primary must probe: every member
// of the current up set plus every member that ever appeared in a historical
// acting set.
func buildProbeSet(currentUp []OSDID, intervals []PastInterval) map[OSDID]struct{} {
	probe := osdSet(currentUp)
	for _, interval := range intervals {
		for _, id := range interval.Acting {
			probe[id] = struct{}{}
		}
	}
	return probe
}
