package peering

import "sort"

// DivergentKind explains why an entry on a peer log was discarded.
type DivergentKind string

const (
	// DivergentFork means the peer carried an entry at a version where the
	// authoritative log holds a different (or no) entry.
	DivergentFork DivergentKind = "divergent_fork"
	// DivergentNotAuth means the peer entry exists only on that peer and the
	// authoritative log never reached its version.
	DivergentNotAuth DivergentKind = "not_in_auth_log"
	// DivergentAfterLES means the entry was written after the peer's last
	// successful peering activation (last_epoch_started) while it was isolated
	// and could not form a min_size majority.
	DivergentAfterLES DivergentKind = "after_last_epoch_started"
)

// DivergentPrior is one log entry judged divergent and slated for rollback.
type DivergentPrior struct {
	OSD   OSDID
	Entry LogEntry
	Kind  DivergentKind
}

// RolledBackLog records the truncation/rollback applied to one peer OSD.
type RolledBackLog struct {
	OSD       OSDID
	Head      EVersion
	Divergent []DivergentPrior
}

// MergeResult is the outcome of merging every peer log onto the chosen
// authoritative log.
type MergeResult struct {
	AuthOSD    OSDID
	AuthLog    PGLog
	AuthLatest map[ObjectID]LogEntry
	Divergent  []DivergentPrior
	RolledBack map[OSDID]*RolledBackLog
	MergedLog  []LogEntry
}

// SelectAuthLog picks the OSD whose log becomes the authoritative log.
//
// The highest last_update must not win blindly: an isolated old primary can
// have advanced last_update with uncommitted minority writes. Candidate logs
// are therefore first restricted to those with the greatest
// last_epoch_started (real peered activations); among them the most complete
// log wins, using last_complete and finally last_update as tie breakers. A
// deterministic OSDID tie break keeps selection stable. Returns (0, false) if
// there are no replicas.
func SelectAuthLog(replicas []OSDState) (OSDID, bool) {
	if len(replicas) == 0 {
		return 0, false
	}

	var bestLES Epoch
	for _, replica := range replicas {
		if replica.Log.LastEpochStarted > bestLES {
			bestLES = replica.Log.LastEpochStarted
		}
	}

	candidates := make([]OSDState, 0, len(replicas))
	for _, replica := range replicas {
		if replica.Log.LastEpochStarted == bestLES {
			candidates = append(candidates, replica)
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i].Log, candidates[j].Log
		if cmp := left.LastComplete.Compare(right.LastComplete); cmp != 0 {
			return cmp > 0
		}
		if cmp := left.LastUpdate.Compare(right.LastUpdate); cmp != 0 {
			return cmp > 0
		}
		return candidates[i].ID < candidates[j].ID
	})
	return candidates[0].ID, true
}

// MergeLogs selects the authoritative log and reconciles every other log
// against it, returning the divergent entries, per-OSD rollbacks and the
// authoritative latest object version map.
func MergeLogs(replicas []OSDState) (*MergeResult, error) {
	authID, ok := SelectAuthLog(replicas)
	if !ok {
		return nil, errNoReplica
	}

	var authState OSDState
	byID := make(map[OSDID]OSDState, len(replicas))
	for _, replica := range replicas {
		byID[replica.ID] = replica
		if replica.ID == authID {
			authState = replica
		}
	}

	authLog := cloneLog(authState.Log)
	result := &MergeResult{
		AuthOSD:    authID,
		AuthLog:    authLog,
		AuthLatest: make(map[ObjectID]LogEntry),
		RolledBack: make(map[OSDID]*RolledBackLog),
	}

	for _, entry := range authLog.Entries {
		result.AuthLatest[entry.Object] = entry
	}

	authByVersion := make(map[EVersion]LogEntry, len(authLog.Entries))
	for _, entry := range authLog.Entries {
		authByVersion[entry.Version] = entry
	}

	for _, replica := range replicas {
		if replica.ID == authID {
			continue
		}
		rollback := &RolledBackLog{OSD: replica.ID}
		var keptHead EVersion
		haveKept := false

		for _, entry := range replica.Log.Entries {
			kind := classifyDivergent(entry, replica, authByVersion)
			if kind != "" {
				rollback.Divergent = append(rollback.Divergent, DivergentPrior{
					OSD:   replica.ID,
					Entry: entry,
					Kind:  kind,
				})
				continue
			}
			keptHead = entry.Version
			haveKept = true
		}

		if len(rollback.Divergent) > 0 {
			if !haveKept {
				keptHead = replica.Log.Tail
			}
			rollback.Head = keptHead
			result.RolledBack[replica.ID] = rollback
			result.Divergent = append(result.Divergent, rollback.Divergent...)
		}
	}

	sort.Slice(result.Divergent, func(i, j int) bool {
		if result.Divergent[i].OSD != result.Divergent[j].OSD {
			return result.Divergent[i].OSD < result.Divergent[j].OSD
		}
		return result.Divergent[i].Entry.Version.Compare(result.Divergent[j].Entry.Version) < 0
	})

	result.MergedLog = append([]LogEntry(nil), authLog.Entries...)
	return result, nil
}

func classifyDivergent(
	entry LogEntry,
	replica OSDState,
	authByVersion map[EVersion]LogEntry,
) DivergentKind {
	// An entry the peer could only have produced while isolated after its last
	// real peering activation is, by construction, an uncommitted minority
	// write and must be rolled back regardless of its version number.
	if entry.Version.Epoch > replica.Log.LastEpochStarted {
		return DivergentAfterLES
	}

	authEntry, present := authByVersion[entry.Version]
	if !present {
		return DivergentNotAuth
	}
	if authEntry.Object != entry.Object || authEntry.Op != entry.Op {
		return DivergentFork
	}
	return ""
}

func cloneLog(log PGLog) PGLog {
	clone := log
	if len(log.Entries) > 0 {
		clone.Entries = append([]LogEntry(nil), log.Entries...)
	}
	return clone
}

var errNoReplica = newPeeringError("no reachable replica answered the log probe")

type peeringError struct{ message string }

func (e *peeringError) Error() string { return e.message }

func newPeeringError(message string) error { return &peeringError{message: message} }
