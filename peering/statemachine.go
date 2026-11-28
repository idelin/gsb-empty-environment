package peering

// Phase labels for the peering state-machine steps, useful for observability
// and tests that want to assert exactly where peering stopped.
type Phase string

const (
	PhaseHistory     Phase = "build_past_intervals"
	PhaseProbe       Phase = "probe"
	PhaseHistoryGate Phase = "maybe_went_rw_gate"
	PhaseAuthLog     Phase = "select_auth_log"
	PhaseMergeLogs   Phase = "merge_logs"
	PhaseRecovery    Phase = "plan_recovery"
	PhaseMinSizeGate Phase = "min_size_gate"
	PhaseActive      Phase = "active"
	PhaseIncomplete  Phase = "incomplete"
)

// Result is the terminal outcome of one peering round.
type Result struct {
	State         PGState
	Phase         Phase
	BlockedReason string
	Queried       []OSDID
	Intervals     []PastInterval
	Unwitnessed   *PastInterval
	Merge         *MergeResult
	Recovery      *RecoveryPlan
	MinSize       int
	CanWrite      bool
	Degraded      bool
	Unfound       []ObjectID
}

// ObjectIOAllowed reports whether object may accept IO in the resulting PG.
// An unfound object is individually blocked even on an otherwise active PG.
func (r *Result) ObjectIOAllowed(object ObjectID) bool {
	if r.State != StateActive {
		return false
	}
	for _, unfound := range r.Unfound {
		if unfound == object {
			return false
		}
	}
	return true
}

// PG is a single placement group being peered.
type PG struct {
	// ID is an opaque PG identifier used only for diagnostics.
	ID        string
	MinSize   int
	CurrentUp []OSDID
	History   []OSDMapEpoch
	Replicas  map[OSDID]*OSDState
}

// NewPG constructs a PG. History holds the closed OSDMap epochs and currentUp
// is the up set of the epoch in which peering is initiated.
func NewPG(id string, minSize int, currentUp []OSDID, history []OSDMapEpoch, replicas ...OSDState) *PG {
	pg := &PG{
		ID:        id,
		MinSize:   minSize,
		CurrentUp: copyIDs(currentUp),
		History:   append([]OSDMapEpoch(nil), history...),
		Replicas:  make(map[OSDID]*OSDState),
	}
	for i := range replicas {
		replica := replicas[i]
		pg.Replicas[replica.ID] = &replica
	}
	return pg
}

// Peer runs the full peering state machine.
//
// The routine never silently activates an unsafe PG: a missing history
// witness, the absence of any reachable replica, or fewer than min_size
// recoverable replicas all terminate Incomplete with a precise BlockedReason.
func (pg *PG) Peer() *Result {
	result := &Result{State: StatePeering, MinSize: pg.MinSize}

	// 1. Reconstruct authoritative interval history.
	result.Intervals = BuildPastIntervals(pg.History)
	result.Phase = PhaseHistory

	// 2. Probe every current up member plus every historical acting member.
	probeSet := buildProbeSet(pg.CurrentUp, result.Intervals)
	var queried []OSDID
	var aliveReplicas []OSDState
	for id := range probeSet {
		state, known := pg.Replicas[id]
		if known && state.Alive {
			queried = append(queried, id)
			aliveReplicas = append(aliveReplicas, *state)
		}
	}
	sortOSDIDs(queried)
	result.Queried = queried
	result.Phase = PhaseProbe

	// 3. maybe_went_rw gate: every writable interval needs a reachable witness.
	result.Unwitnessed = FindUnwitnessedInterval(result.Intervals, queried)
	result.Phase = PhaseHistoryGate
	if result.Unwitnessed != nil {
		return pg.block(result, PhaseHistoryGate, ReasonHistoryGap)
	}

	if len(aliveReplicas) == 0 {
		return pg.block(result, PhaseProbe, ReasonNoReplica)
	}

	// 4. Authoritative log selection and divergent log rollback.
	result.Phase = PhaseAuthLog
	merge, err := MergeLogs(aliveReplicas)
	if err != nil {
		return pg.block(result, PhaseAuthLog, err.Error())
	}
	result.Merge = merge
	result.Phase = PhaseMergeLogs

	// 5. up/acting reconciliation, pg_temp, missing and missing_locations.
	result.Phase = PhaseRecovery
	aliveMap := make(map[OSDID]OSDState, len(aliveReplicas))
	for _, replica := range aliveReplicas {
		aliveMap[replica.ID] = replica
	}
	recovery := PlanRecovery(pg.CurrentUp, aliveMap, merge)
	result.Recovery = recovery
	result.Unfound = append([]ObjectID(nil), recovery.Unfound...)

	// 6. min_size write gate on recoverable (complete, alive) replicas.
	result.Phase = PhaseMinSizeGate
	if len(recovery.ActiveReplicas) < pg.MinSize {
		return pg.block(result, PhaseMinSizeGate, ReasonMinSize)
	}

	// 7. Activate. Degraded when fewer than the full up set can serve or any
	// object is still missing; writes require min_size complete replicas.
	result.State = StateActive
	result.Phase = PhaseActive
	result.CanWrite = len(recovery.ActiveReplicas) >= pg.MinSize
	result.Degraded =
		len(recovery.ActiveReplicas) < len(pg.CurrentUp) ||
			len(recovery.MissingLocations) > 0 ||
			len(recovery.Unfound) > 0
	return result
}

func (pg *PG) block(result *Result, phase Phase, reason string) *Result {
	result.State = StateIncomplete
	result.Phase = phase
	result.BlockedReason = reason
	result.CanWrite = false
	return result
}
