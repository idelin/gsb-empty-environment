package volrecover

// ActionKind enumerates the operations a host may be asked to perform.
type ActionKind int

const (
	// WriteUnit writes one independent unit with an exact version and record.
	WriteUnit ActionKind = iota
	// WriteHeader stores a fully signed header addressed by its id.
	WriteHeader
	// WriteRoot atomically updates the committed root header pointer.
	WriteRoot
	// WriteRevocation appends one slot to the reliable monotonic register.
	WriteRevocation
	// Flush is a durable barrier. Writes before a flush may survive as any
	// subset on crash; writes after a flush cannot persist without everything
	// before the flush persisting.
	Flush
)

// Action is one planned host operation. Revocation writes and flushes both
// count against the 16-action budget.
type Action struct {
	Kind      ActionKind
	Unit      UnitID
	Version   uint64
	Payload   []byte
	Record    Record
	Header    *Header
	Root      Digest
	Slot      SlotID
	RevokeSeq uint64
}

// MaxActions is the inclusive host action budget.
const MaxActions = 16

// DefaultEnumerationLimit bounds the number of distinct crash states that are
// materialized. Crossing it returns Unknown rather than an unsafe verdict.
const DefaultEnumerationLimit = 1 << 16

// PlanRequest describes the desired upgrade.
type PlanRequest struct {
	// Initial is the read-only image the host starts from.
	Initial ImageView
	// Root is the currently selected trust root and revocation register.
	Root *TrustRoot
	// Target is the fully signed successor header to commit.
	Target *Header
	// SpareUnits are the only physical units the plan may write.
	SpareUnits []UnitID
	// RevokeSlots are slots that must become irrevocably revoked. They must
	// not be granted by the target header.
	RevokeSlots []SlotID
	// ActionBudget overrides the default 16-action budget when positive.
	ActionBudget int
	// EnumerationLimit overrides the default crash-state limit when positive.
	EnumerationLimit int
}

// Plan is a verified minimal-flush action sequence.
type Plan struct {
	Actions     []Action
	Flushes     int
	ActionCount int
}

// PlanOutcome is the result of BuildPlan. Callers type-assert to *Plan,
// *UnsafePlan, or *UnknownPlan.
type PlanOutcome interface {
	isPlanOutcome()
}

// UnsafePlan means no safe plan exists for a concrete, checkable reason.
type UnsafePlan struct {
	Reason Reason
}

func (*UnsafePlan) isPlanOutcome() {}

// UnknownPlan means available state/action budget is insufficient to establish
// safety, not that the transition is known to be unsafe.
type UnknownPlan struct {
	Reason Reason
}

func (*UnknownPlan) isPlanOutcome() {}

func (*Plan) isPlanOutcome() {}

// BuildPlan constructs the minimum-flush action sequence and proves the
// sequence safe by exhaustively enumerating every crash subset.
func BuildPlan(req PlanRequest) PlanOutcome {
	budget := req.ActionBudget
	if budget <= 0 {
		budget = MaxActions
	}
	limit := req.EnumerationLimit
	if limit <= 0 {
		limit = DefaultEnumerationLimit
	}

	if req.Root == nil || req.Initial == nil {
		return &UnsafePlan{Reason: Reason{Code: ReasonMalformedHeader, Detail: "missing trust root or initial image"}}
	}
	if reason := VerifyHeader(req.Target, req.Root); !reason.OK() {
		return &UnsafePlan{Reason: reason}
	}

	spare := make(map[UnitID]bool, len(req.SpareUnits))
	for _, unit := range req.SpareUnits {
		if spare[unit] {
			return &UnsafePlan{Reason: Reason{Code: ReasonRangeConflict, Detail: "duplicate spare unit", Witness: "unit=" + u64Name(uint64(unit))}}
		}
		spare[unit] = true
	}

	// Static precondition: every target unit must be writable through the
	// provided spare set and must not rewrite a unit still required by the
	// currently committed root header. Overlapping geometric ranges are fine;
	// reinterpreting rewritten physical units through an old slot is not.
	liveUnits := liveReferencedUnits(req.Initial)
	plannedUnits := map[UnitID]bool{}
	for _, slot := range req.Target.Slots {
		for _, ref := range slot.Units {
			if plannedUnits[ref.Unit] {
				return &UnsafePlan{Reason: Reason{Code: ReasonRangeConflict, Detail: "target header binds one unit twice", Witness: "unit=" + u64Name(uint64(ref.Unit))}}
			}
			plannedUnits[ref.Unit] = true
			// A unit already durable with exactly the target content needs
			// no write, so the spare allow list and live-rewrite guard apply
			// only to units the plan will actually change.
			if existing, ok := req.Initial.Unit(ref.Unit); ok && existing.Version == ref.Version && existing.Commit == ref.Commit {
				continue
			}
			if !spare[ref.Unit] {
				return &UnsafePlan{Reason: Reason{Code: ReasonOverlapConfusion, Detail: "target unit is not in the spare set", Witness: "unit=" + u64Name(uint64(ref.Unit))}}
			}
			if liveUnits[ref.Unit] {
				return &UnsafePlan{Reason: Reason{Code: ReasonOverlapConfusion, Detail: "target rewrites a unit bound to a live old slot", Witness: "unit=" + u64Name(uint64(ref.Unit))}}
			}
			if existing, ok := req.Initial.Unit(ref.Unit); ok && (existing.Version != ref.Version || existing.Commit != ref.Commit) {
				return &UnsafePlan{Reason: Reason{Code: ReasonVersionRollback, Detail: "spare unit already contains newer or conflicting content", Witness: "unit=" + u64Name(uint64(ref.Unit))}}
			}
		}
	}

	for _, slot := range req.RevokeSlots {
		if req.Root.IsRevoked(slot) {
			continue
		}
		for _, grant := range req.Target.Grants {
			if grant.Slot == slot {
				return &UnsafePlan{Reason: Reason{Code: ReasonRevokedSlot, Detail: "target header still grants a slot requested for revocation", Witness: "slot=" + u64Name(uint64(slot))}}
			}
		}
	}

	actions := buildCanonicalActions(req)
	if len(actions) > budget {
		return &UnknownPlan{Reason: Reason{Code: ReasonBudgetExceeded, Detail: "minimum action sequence exceeds host budget", Witness: "actions=" + u64Name(uint64(len(actions)))}}
	}
	_, targetDurable := req.Initial.Header(req.Target.ID)
	var durable []Digest
	if targetDurable {
		durable = []Digest{req.Target.ID}
	}
	if reason := ValidateOrdering(actions, durable...); !reason.OK() {
		return &UnsafePlan{Reason: reason}
	}

	states, unknown := ExhaustCrashes(req.Initial, actions, limit)
	if unknown != nil {
		return &UnknownPlan{Reason: *unknown}
	}
	if witness := firstUnsafe(states, req.Root); witness != nil {
		return &UnsafePlan{Reason: reasonFromWitness(witness)}
	}

	flushes := 0
	for _, action := range actions {
		if action.Kind == Flush {
			flushes++
		}
	}
	return &Plan{Actions: actions, Flushes: flushes, ActionCount: len(actions)}
}
