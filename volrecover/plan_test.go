package volrecover

import "testing"

// TestRevokeBeforeSpareHeader: a revocation that can become durable before
// the replacement header reaches a flush barrier is rejected; a correctly
// barrier-bounded plan keeps every crash image unlockable.
func TestRevokeBeforeSpareHeader(t *testing.T) {
	fx := newFixture(t)

	target := targetHeader(t, fx, []SlotSpec{{
		ID:    2,
		Range: Interval{Start: 0, End: 100},
		Units: []UnitRef{{Unit: 2, Version: 1, Commit: UnitCommit([]byte("replacement"))}},
	}}, []Grant{{Slot: 2, Segment: 0}}, 2, fx.old.ID)

	// Deliberately unsafe ordering: revocation sits in the same pre-flush
	// epoch as the header and spare unit, so a crash can persist the
	// revocation while dropping the replacement authorization.
	unsafeActions := []Action{
		{Kind: WriteUnit, Unit: 2, Record: Record{Unit: 2, Version: 1, Commit: UnitCommit([]byte("replacement"))}},
		{Kind: WriteHeader, Header: target},
		{Kind: WriteRevocation, Slot: 1, RevokeSeq: 1},
		{Kind: Flush},
		{Kind: WriteRoot, Root: target.ID},
		{Kind: Flush},
	}
	if reason := ValidateOrdering(unsafeActions); reason.Code != ReasonRevokeBeforeHeader {
		t.Fatalf("expected revoke-before-header rejection, got %v", reason)
	}

	states, unknown := ExhaustCrashes(fx.dev.View(), unsafeActions, DefaultEnumerationLimit)
	if unknown != nil {
		t.Fatalf("enumeration failed: %v", unknown)
	}
	witness := firstUnsafe(states, fx.root)
	if witness == nil {
		t.Fatalf("unsafe ordering must produce at least one un-unlockable crash image")
	}

	plan, ok := BuildPlan(PlanRequest{
		Initial:     fx.dev.View(),
		Root:        fx.root,
		Target:      target,
		SpareUnits:  []UnitID{2},
		RevokeSlots: []SlotID{1},
	}).(*Plan)
	if !ok {
		t.Fatalf("expected safe plan with post-barrier revocation")
	}
	if plan.Flushes != 2 {
		t.Fatalf("expected minimum 2 flushes, got %d", plan.Flushes)
	}
	safeStates, unknown := ExhaustCrashes(fx.dev.View(), plan.Actions, DefaultEnumerationLimit)
	if unknown != nil {
		t.Fatalf("safe plan enumeration failed: %v", unknown)
	}
	if w := firstUnsafe(safeStates, fx.root); w != nil {
		t.Fatalf("all crash images must stay unlockable, got %v", w.Verdict.FatalReason)
	}

	final := safeStates[len(safeStates)-1].View
	if final.RootPointer() != target.ID {
		t.Fatalf("final root must target the new header")
	}
	if _, revoked := final.Revocation(1); !revoked {
		t.Fatalf("slot 1 revocation must be durable in the final state")
	}
	finalVerdict := Analyze(final, fx.root)
	for _, id := range finalVerdict.Usable {
		h, _ := final.Header(id)
		for _, grant := range h.Grants {
			if grant.Slot == 1 {
				t.Fatalf("revoked slot 1 must not be usable through any header")
			}
		}
	}
}

// TestRootBeforeSpare: the root pointer may not move until every spare unit
// and the replacement header has crossed a flush barrier.
func TestRootBeforeSpare(t *testing.T) {
	fx := newFixture(t)
	target := targetHeader(t, fx, []SlotSpec{{
		ID:    2,
		Range: Interval{Start: 0, End: 100},
		Units: []UnitRef{{Unit: 2, Version: 1, Commit: UnitCommit([]byte("replacement"))}},
	}}, []Grant{{Slot: 2, Segment: 0}}, 2, fx.old.ID)

	bad := []Action{
		{Kind: WriteUnit, Unit: 2, Record: Record{Unit: 2, Version: 1, Commit: UnitCommit([]byte("replacement"))}},
		{Kind: WriteHeader, Header: target},
		{Kind: WriteRoot, Root: target.ID},
		{Kind: Flush},
	}
	if reason := ValidateOrdering(bad); reason.Code != ReasonRootBeforeSpare {
		t.Fatalf("expected root-before-spare rejection, got %v", reason)
	}
}

// TestRevokeOnlyWithDurableTarget covers the case where the target header is
// already stored and only the monotonic revocation register must move.
func TestRevokeOnlyWithDurableTarget(t *testing.T) {
	fx := newFixture(t)
	target := targetHeader(t, fx, []SlotSpec{{
		ID:    2,
		Range: Interval{Start: 0, End: 100},
		Units: []UnitRef{{Unit: 2, Version: 1, Commit: UnitCommit([]byte("replacement"))}},
	}}, []Grant{{Slot: 2, Segment: 0}}, 2, fx.old.ID)

	dev := fx.dev.Snapshot()
	dev.WriteRecord(Record{Unit: 2, Version: 1, Commit: UnitCommit([]byte("replacement"))})
	dev.PutHeader(target)
	dev.SetRootPointer(target.ID)
	root := NewTrustRoot(fx.pub, target.ID, nil)

	outcome := BuildPlan(PlanRequest{
		Initial:     dev.View(),
		Root:        root,
		Target:      target,
		SpareUnits:  []UnitID{},
		RevokeSlots: []SlotID{1},
	})
	plan, ok := outcome.(*Plan)
	if !ok {
		t.Fatalf("expected revoke-only Plan, got %#v", outcome)
	}
	// Revoke plus one flush barrier: the durable target needs no re-write.
	if plan.ActionCount != 2 || plan.Flushes != 1 {
		t.Fatalf("expected 2 actions and 1 flush, got count=%d flushes=%d", plan.ActionCount, plan.Flushes)
	}
	states, unknown := ExhaustCrashes(dev.View(), plan.Actions, DefaultEnumerationLimit)
	if unknown != nil {
		t.Fatalf("enumeration failed: %v", unknown)
	}
	if w := firstUnsafe(states, root); w != nil {
		t.Fatalf("revoke-only transition must stay unlockable, got %v", w.Verdict.FatalReason)
	}
}

// TestUnknownOnBudget: action budget exhaustion and enumeration limits return
// Unknown rather than claiming the transition is provably unsafe.
func TestUnknownOnBudget(t *testing.T) {
	fx := newFixture(t)

	slots := []SlotSpec{}
	spares := []UnitID{}
	for unit := UnitID(10); unit < 25; unit++ {
		slots = append(slots, SlotSpec{
			ID:    SlotID(unit),
			Range: Interval{Start: 0, End: 1000},
			Units: []UnitRef{{Unit: unit, Version: 1, Commit: UnitCommit([]byte{byte(unit), byte(unit)})}},
		})
		spares = append(spares, unit)
	}
	target := targetHeader(t, fx, slots, []Grant{{Slot: 10, Segment: 0}}, 2, fx.old.ID)

	outcome := BuildPlan(PlanRequest{
		Initial:    fx.dev.View(),
		Root:       fx.root,
		Target:     target,
		SpareUnits: spares,
	})
	unknown, ok := outcome.(*UnknownPlan)
	if !ok {
		t.Fatalf("expected UnknownPlan for exceeded action budget, got %T", outcome)
	}
	if unknown.Reason.Code != ReasonBudgetExceeded {
		t.Fatalf("expected budget exceeded reason, got %v", unknown.Reason)
	}

	// Enumeration budget exhaustion is also Unknown, never an unsafe verdict.
	simple := targetHeader(t, fx, []SlotSpec{{
		ID:    2,
		Range: Interval{Start: 0, End: 100},
		Units: []UnitRef{{Unit: 2, Version: 1, Commit: UnitCommit([]byte("replacement"))}},
	}}, []Grant{{Slot: 2, Segment: 0}}, 2, fx.old.ID)
	actions := []Action{
		{Kind: WriteUnit, Unit: 2, Record: Record{Unit: 2, Version: 1, Commit: UnitCommit([]byte("replacement"))}},
		{Kind: WriteHeader, Header: simple},
	}
	_, reason := ExhaustCrashes(fx.dev.View(), actions, 1)
	if reason == nil || reason.Code != ReasonEnumerationLimit {
		t.Fatalf("expected enumeration limit Unknown reason, got %v", reason)
	}
}

// TestFullUpgradeCrashCoverage exercises the complete two-flush upgrade and
// asserts every enumerated crash image retains either the old or the new
// complete trusted authorization.
func TestFullUpgradeCrashCoverage(t *testing.T) {
	fx := newFixture(t)

	target := targetHeader(t, fx, []SlotSpec{{
		ID:    2,
		Range: Interval{Start: 0, End: 100},
		Units: []UnitRef{
			{Unit: 2, Version: 1, Commit: UnitCommit([]byte("new-unit-a"))},
			{Unit: 3, Version: 1, Commit: UnitCommit([]byte("new-unit-b"))},
		},
	}}, []Grant{{Slot: 2, Segment: 0}}, 2, fx.old.ID)

	plan, ok := BuildPlan(PlanRequest{
		Initial:    fx.dev.View(),
		Root:       fx.root,
		Target:     target,
		SpareUnits: []UnitID{2, 3},
	}).(*Plan)
	if !ok {
		t.Fatalf("expected a safe full-upgrade plan")
	}
	if plan.Flushes != 2 || plan.ActionCount != 6 {
		t.Fatalf("expected 6 actions across 2 flushes, got count=%d flushes=%d", plan.ActionCount, plan.Flushes)
	}

	states, unknown := ExhaustCrashes(fx.dev.View(), plan.Actions, DefaultEnumerationLimit)
	if unknown != nil {
		t.Fatalf("enumeration failed: %v", unknown)
	}

	for _, state := range states {
		verdict := Analyze(state.View, fx.root)
		if !verdict.SafeToUnlock() {
			t.Fatalf("crash state epoch=%d unsafe: %v", state.Epoch, verdict.FatalReason)
		}
		if len(verdict.Usable) == 0 {
			t.Fatalf("crash state epoch=%d has no usable header", state.Epoch)
		}
	}

	// At least one pre-pointer state must still unlock through the old header,
	// proving old/new dual metadata are both honored.
	foundOld := false
	for _, state := range states {
		if state.View.RootPointer() == fx.old.ID {
			verdict := Analyze(state.View, fx.root)
			for _, id := range verdict.Usable {
				if id == fx.old.ID {
					foundOld = true
				}
			}
		}
	}
	if !foundOld {
		t.Fatalf("expected at least one crash image unlockable through the old header")
	}
}
