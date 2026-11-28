package peering

import "sort"

// MissingEntry describes one object version a specific OSD must recover.
type MissingEntry struct {
	Object ObjectID
	Need   EVersion
	Have   EVersion
	NeedOp OpType
}

// MissingLocations maps each missing object version to the OSDs that hold it,
// i.e. the recovery sources (Ceph missing_locations).
type MissingLocations map[ObjectID][]OSDID

// RecoveryPlan captures up/acting reconciliation and the recovery bookkeeping
// produced during peering.
type RecoveryPlan struct {
	Up               []OSDID
	Acting           []OSDID
	PgTemp           []OSDID
	UsingTemp        bool
	Backfill         []OSDID
	Complete         []OSDID
	Missing          map[OSDID][]MissingEntry
	MissingLocations MissingLocations
	Unfound          []ObjectID
	ActiveReplicas   []OSDID
}

// PlanRecovery builds the recovery plan from the chosen current up set, the
// per-OSD runtime states and the merged authoritative log.
//
// New up members that cannot incrementally recover (flagged for backfill,
// empty, or whose log tail is older than the authoritative tail) are placed on
// the backfill path. While they sync, pg_temp restricts the acting set to the
// already-complete replicas so the PG can resume active+degraded service on
// the good copies instead of waiting for full backfill.
func PlanRecovery(
	up []OSDID,
	alive map[OSDID]OSDState,
	merge *MergeResult,
) *RecoveryPlan {
	plan := &RecoveryPlan{
		Up:               copyIDs(up),
		Missing:          make(map[OSDID][]MissingEntry),
		MissingLocations: make(MissingLocations),
	}

	for _, id := range up {
		state, reachable := alive[id]
		if !reachable {
			plan.Backfill = append(plan.Backfill, id)
			continue
		}
		if needsBackfill(state, merge.AuthLog) {
			plan.Backfill = append(plan.Backfill, id)
		} else {
			plan.Complete = append(plan.Complete, id)
			plan.ActiveReplicas = append(plan.ActiveReplicas, id)
		}
	}

	// pg_temp: only complete, alive replicas act until backfill finishes. When
	// every up member is complete the real up set is used and no temp is made.
	if len(plan.Backfill) > 0 && len(plan.Complete) > 0 {
		plan.UsingTemp = true
		plan.PgTemp = copyIDs(plan.Complete)
		plan.Acting = copyIDs(plan.Complete)
	} else if len(plan.Backfill) > 0 {
		// Every up member needs backfill: there is no complete replica to act,
		// so the acting set stays empty and the min_size gate blocks activation.
		plan.Acting = nil
	} else {
		plan.Acting = copyIDs(up)
	}

	// Per-member missing sets, computed against the authoritative latest
	// object version map.
	for _, id := range up {
		state, reachable := alive[id]
		if !reachable {
			continue
		}
		plan.Missing[id] = computeMissing(state, merge.AuthLatest)
	}

	// Global missing_locations and unfound detection.
	liveHolders := make(map[ObjectID]map[OSDID]struct{})
	for object := range merge.AuthLatest {
		liveHolders[object] = make(map[OSDID]struct{})
	}
	for id, state := range alive {
		if !containsID(up, id) {
			continue
		}
		for object, held := range state.Objects {
			latest, tracked := merge.AuthLatest[object]
			if tracked && held.Compare(latest.Version) == 0 {
				liveHolders[object][id] = struct{}{}
			}
		}
	}

	var unfound []ObjectID
	for object, holders := range liveHolders {
		if len(holders) == 0 {
			unfound = append(unfound, object)
			continue
		}
		plan.MissingLocations[object] = sortedOSDs(holders)
	}
	sortObjectIDs(unfound)
	plan.Unfound = unfound

	return plan
}

func needsBackfill(state OSDState, authLog PGLog) bool {
	if state.NeedsBackfill {
		return true
	}
	// An empty replica (no log at all) needs a full backfill.
	if state.Log.LastUpdate.IsZero() {
		return true
	}
	// The replica lost the log entries required to catch up incrementally: its
	// oldest retained entry is older than the authoritative tail, so the
	// intervening operations cannot be replayed and a full backfill is needed.
	if state.Log.Tail.Compare(authLog.Tail) < 0 {
		return true
	}
	return false
}

func computeMissing(state OSDState, authLatest map[ObjectID]LogEntry) []MissingEntry {
	var missing []MissingEntry
	objects := make(map[ObjectID]struct{}, len(authLatest)+len(state.Objects))
	for object := range authLatest {
		objects[object] = struct{}{}
	}
	for object := range state.Objects {
		objects[object] = struct{}{}
	}

	for object := range objects {
		latest, tracked := authLatest[object]
		held, have := state.Objects[object]
		switch {
		case !tracked:
			continue
		case !have:
			// Either a genuinely new object or a delete the replica has not
			// applied yet; both are recoverable log operations.
			missing = append(missing, MissingEntry{
				Object: object,
				Need:   latest.Version,
				NeedOp: latest.Op,
			})
		case held.Compare(latest.Version) < 0:
			missing = append(missing, MissingEntry{
				Object: object,
				Need:   latest.Version,
				Have:   held,
				NeedOp: latest.Op,
			})
		}
	}

	sort.Slice(missing, func(i, j int) bool {
		return missing[i].Need.Compare(missing[j].Need) < 0
	})
	return missing
}

func sortObjectIDs(ids []ObjectID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
