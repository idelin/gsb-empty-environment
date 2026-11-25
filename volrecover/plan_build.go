package volrecover

import "sort"

func buildCanonicalActions(req PlanRequest) []Action {
	actions := []Action{}

	// Epoch 1: write every spare unit carrying new target content, then the
	// successor header itself. Nothing here can revoke or repoint anything.
	unitRefs := sortedTargetRefs(req.Target)
	for _, ref := range unitRefs {
		existing, ok := req.Initial.Unit(ref.Unit)
		if ok && existing.Version == ref.Version && existing.Commit == ref.Commit {
			continue
		}
		actions = append(actions, Action{
			Kind:    WriteUnit,
			Unit:    ref.Unit,
			Version: ref.Version,
			Record:  Record{Unit: ref.Unit, Version: ref.Version, Commit: ref.Commit},
		})
	}
	_, headerAlreadyDurable := req.Initial.Header(req.Target.ID)
	if !headerAlreadyDurable {
		actions = append(actions, Action{Kind: WriteHeader, Header: req.Target})
	}

	rootChanges := req.Initial.RootPointer() != req.Target.ID
	needRevoke := pendingRevocations(req)

	// The first barrier is required whenever new content lands, whenever the
	// pointer must move, or whenever a revocation needs the replacement
	// authorization to be provably durable first. An already-stored target
	// header counts as durable without another flush.
	if rootChanges || len(actions) > 0 || len(needRevoke) > 0 {
		// First barrier makes every spare unit and the replacement header
		// durable before any root pointer or revocation operation.
		actions = append(actions, Action{Kind: Flush})
	}

	if len(needRevoke) > 0 {
		seq := nextRevocationSeq(req.Root)
		for _, slot := range needRevoke {
			actions = append(actions, Action{Kind: WriteRevocation, Slot: slot, RevokeSeq: seq})
			seq++
		}
	}

	if rootChanges {
		actions = append(actions, Action{Kind: WriteRoot, Root: req.Target.ID})
	}

	if rootChanges {
		// Second barrier commits the root pointer so the final durable state
		// survives. The revocation register is itself reliable and monotonic,
		// so a revocation-only transition needs no ordinary block flush.
		actions = append(actions, Action{Kind: Flush})
	}
	return actions
}

func pendingRevocations(req PlanRequest) []SlotID {
	out := []SlotID{}
	seen := map[SlotID]bool{}
	for _, slot := range req.RevokeSlots {
		if seen[slot] || req.Root.IsRevoked(slot) {
			continue
		}
		seen[slot] = true
		out = append(out, slot)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func nextRevocationSeq(root *TrustRoot) uint64 {
	var max uint64
	for _, seq := range root.Revoked {
		if seq > max {
			max = seq
		}
	}
	return max + 1
}

func sortedTargetRefs(h *Header) []UnitRef {
	refs := []UnitRef{}
	seen := map[UnitID]bool{}
	for _, slot := range h.Slots {
		for _, ref := range slot.Units {
			if seen[ref.Unit] {
				continue
			}
			seen[ref.Unit] = true
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Unit < refs[j].Unit })
	return refs
}

func liveReferencedUnits(view ImageView) map[UnitID]bool {
	out := map[UnitID]bool{}
	for _, h := range view.Headers() {
		// Only units referenced by the current root header are protected:
		// unrelated stale headers cannot block spare allocation.
		if h.ID != view.RootPointer() {
			continue
		}
		for _, slot := range h.Slots {
			for _, ref := range slot.Units {
				out[ref.Unit] = true
			}
		}
	}
	return out
}

// ValidateOrdering enforces the barrier invariants independently of the
// planner. It is exported so a host can validate externally supplied plans.
// durableHeaders lists header ids that are already committed in the image
// before the first action; such headers need no extra pre-revocation flush.
func ValidateOrdering(actions []Action, durableHeaders ...Digest) Reason {
	flushedSpare := false
	flushedHeader := len(durableHeaders) > 0
	sawRoot := false
	seenUnitWrites := map[UnitID]int{}

	for index, action := range actions {
		switch action.Kind {
		case WriteUnit:
			if sawRoot {
				return Reason{Code: ReasonRootBeforeSpare, Detail: "unit write after root pointer update", Witness: "index=" + u64Name(uint64(index))}
			}
			if prior, ok := seenUnitWrites[action.Unit]; ok {
				return Reason{Code: ReasonRepeatedUnitWrite, Detail: "unit written twice without an intervening barrier", Witness: "unit=" + u64Name(uint64(action.Unit)) + " first=" + u64Name(uint64(prior))}
			}
			seenUnitWrites[action.Unit] = index
		case WriteHeader:
			if sawRoot {
				return Reason{Code: ReasonRootBeforeSpare, Detail: "header write after root pointer update", Witness: "index=" + u64Name(uint64(index))}
			}
		case WriteRevocation:
			if !flushedHeader {
				return Reason{Code: ReasonRevokeBeforeHeader, Detail: "revocation registered before replacement header reached a flush barrier", Witness: "slot=" + u64Name(uint64(action.Slot))}
			}
		case WriteRoot:
			if !flushedSpare || !flushedHeader {
				return Reason{Code: ReasonRootBeforeSpare, Detail: "root pointer updated before spare units and header are durable"}
			}
			sawRoot = true
		case Flush:
			flushedSpare = true
			if wroteHeader(actions[:index+1]) {
				flushedHeader = true
			}
			seenUnitWrites = map[UnitID]int{}
		}
	}
	return Reason{}
}

func wroteHeader(actions []Action) bool {
	for _, action := range actions {
		if action.Kind == WriteHeader {
			return true
		}
	}
	return false
}
