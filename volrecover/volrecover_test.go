package volrecover

import (
	"crypto/ed25519"
	"testing"
)

type testFixture struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	dev  *MemDevice
	root *TrustRoot
	old  *Header
}

func newFixture(t *testing.T) *testFixture {
	t.Helper()
	pub, priv, err := GenerateRootKey()
	if err != nil {
		t.Fatalf("key generation: %v", err)
	}

	old := signedHeader(t, priv, Header{
		Epoch:    1,
		Parent:   Digest{},
		Segments: []Interval{{Start: 1000, End: 2000}},
		Slots: []SlotSpec{{
			ID:    1,
			Range: Interval{Start: 0, End: 10},
			Units: []UnitRef{{Unit: 1, Version: 1, Commit: UnitCommit([]byte("old-slot-1"))}},
		}},
		Grants: []Grant{{Slot: 1, Segment: 0}},
	})

	dev := NewMemDevice(Digest{})
	dev.WriteRecord(Record{Unit: 1, Version: 1, Commit: UnitCommit([]byte("old-slot-1"))})
	dev.PutHeader(old)
	dev.SetRootPointer(old.ID)

	root := NewTrustRoot(pub, old.ID, nil)
	return &testFixture{pub: pub, priv: priv, dev: dev, root: root, old: old}
}

func signedHeader(t *testing.T, priv ed25519.PrivateKey, h Header) *Header {
	t.Helper()
	SignHeader(&h, priv)
	return &h
}

func targetHeader(t *testing.T, fx *testFixture, slots []SlotSpec, grants []Grant, epoch Epoch, parent Digest) *Header {
	t.Helper()
	return signedHeader(t, fx.priv, Header{
		Epoch:    epoch,
		Parent:   parent,
		Segments: []Interval{{Start: 1000, End: 2000}},
		Slots:    slots,
		Grants:   grants,
	})
}

// TestOverlappingSlots: geometric overlap is allowed, but a target that
// rewrites a physical unit still bound to the live old header is rejected, and
// a newer durable unit cannot be interpreted through the old version binding.
func TestOverlappingSlots(t *testing.T) {
	fx := newFixture(t)

	target := targetHeader(t, fx, []SlotSpec{{
		ID:    2,
		Range: Interval{Start: 0, End: 10},
		Units: []UnitRef{{Unit: 1, Version: 2, Commit: UnitCommit([]byte("new-slot-2"))}},
	}}, []Grant{{Slot: 2, Segment: 0}}, 2, fx.old.ID)

	outcome := BuildPlan(PlanRequest{
		Initial:    fx.dev.View(),
		Root:       fx.root,
		Target:     target,
		SpareUnits: []UnitID{1},
	})
	unsafe, ok := outcome.(*UnsafePlan)
	if !ok {
		t.Fatalf("expected UnsafePlan for overlapping physical unit, got %T", outcome)
	}
	if unsafe.Reason.Code != ReasonOverlapConfusion {
		t.Fatalf("expected overlap confusion, got %v", unsafe.Reason)
	}

	good := targetHeader(t, fx, []SlotSpec{{
		ID:    2,
		Range: Interval{Start: 0, End: 10},
		Units: []UnitRef{{Unit: 2, Version: 1, Commit: UnitCommit([]byte("new-slot-2"))}},
	}}, []Grant{{Slot: 2, Segment: 0}}, 2, fx.old.ID)

	goodReq := PlanRequest{Initial: fx.dev.View(), Root: fx.root, Target: good, SpareUnits: []UnitID{2}}
	plan, ok := BuildPlan(goodReq).(*Plan)
	if !ok {
		t.Fatalf("expected Plan for disjoint spare unit, got %#v", BuildPlan(goodReq))
	}
	if plan.Flushes != 2 {
		t.Fatalf("expected 2 flush barriers, got %d", plan.Flushes)
	}

	corrupt := fx.dev.Snapshot()
	corrupt.WriteRecord(Record{Unit: 1, Version: 2, Commit: UnitCommit([]byte("new-slot-2"))})
	verdict := Analyze(corrupt.View(), fx.root)
	if verdict.SafeToUnlock() {
		t.Fatalf("image with newer unit interpreted through old slot must not unlock")
	}
	if verdict.Headers[0].Reason.Code != ReasonVersionRollback {
		t.Fatalf("expected version rollback rejection, got %v", verdict.Headers[0].Reason)
	}
}

// TestSameGenerationFork: two distinct valid headers claiming one epoch are a
// fork; distinct epochs on one parent chain are accepted.
func TestSameGenerationFork(t *testing.T) {
	fx := newFixture(t)

	forkA := targetHeader(t, fx, []SlotSpec{{
		ID:    2,
		Range: Interval{Start: 0, End: 100},
		Units: []UnitRef{{Unit: 2, Version: 1, Commit: UnitCommit([]byte("fork-a"))}},
	}}, []Grant{{Slot: 2, Segment: 0}}, 2, fx.old.ID)
	forkB := targetHeader(t, fx, []SlotSpec{{
		ID:    3,
		Range: Interval{Start: 0, End: 100},
		Units: []UnitRef{{Unit: 3, Version: 1, Commit: UnitCommit([]byte("fork-b"))}},
	}}, []Grant{{Slot: 3, Segment: 0}}, 2, fx.old.ID)

	forked := fx.dev.Snapshot()
	forked.WriteRecord(Record{Unit: 2, Version: 1, Commit: UnitCommit([]byte("fork-a"))})
	forked.WriteRecord(Record{Unit: 3, Version: 1, Commit: UnitCommit([]byte("fork-b"))})
	forked.PutHeader(forkA)
	forked.PutHeader(forkB)

	verdict := Analyze(forked.View(), fx.root)
	if !verdict.Fork || verdict.FatalReason.Code != ReasonFork {
		t.Fatalf("expected same-generation fork, got fatal=%v fork=%v", verdict.FatalReason, verdict.Fork)
	}
	if verdict.SafeToUnlock() {
		t.Fatalf("forked image must not unlock")
	}

	linear := targetHeader(t, fx, []SlotSpec{{
		ID:    2,
		Range: Interval{Start: 0, End: 100},
		Units: []UnitRef{{Unit: 2, Version: 1, Commit: UnitCommit([]byte("linear"))}},
	}}, []Grant{{Slot: 2, Segment: 0}}, 2, fx.old.ID)
	okDev := fx.dev.Snapshot()
	okDev.WriteRecord(Record{Unit: 2, Version: 1, Commit: UnitCommit([]byte("linear"))})
	okDev.PutHeader(linear)
	okVerdict := Analyze(okDev.View(), fx.root)
	if !okVerdict.SafeToUnlock() || okVerdict.Fork {
		t.Fatalf("linear generation chain must be safe, got %v", okVerdict.FatalReason)
	}
}

// TestMixedGeneration: two valid headers each reaching genesis through
// distinct chains are mixed-generation and cannot unlock the volume.
func TestMixedGeneration(t *testing.T) {
	fx := newFixture(t)

	otherGenesis := signedHeader(t, fx.priv, Header{
		Epoch:    3,
		Parent:   Digest{},
		Segments: []Interval{{Start: 1000, End: 2000}},
		Slots: []SlotSpec{{
			ID:    9,
			Range: Interval{Start: 0, End: 100},
			Units: []UnitRef{{Unit: 9, Version: 1, Commit: UnitCommit([]byte("other-genesis"))}},
		}},
		Grants: []Grant{{Slot: 9, Segment: 0}},
	})
	separate := signedHeader(t, fx.priv, Header{
		Epoch:    4,
		Parent:   otherGenesis.ID,
		Segments: []Interval{{Start: 1000, End: 2000}},
		Slots: []SlotSpec{{
			ID:    2,
			Range: Interval{Start: 0, End: 100},
			Units: []UnitRef{{Unit: 2, Version: 1, Commit: UnitCommit([]byte("separate-chain"))}},
		}},
		Grants: []Grant{{Slot: 2, Segment: 0}},
	})

	dev := fx.dev.Snapshot()
	dev.WriteRecord(Record{Unit: 9, Version: 1, Commit: UnitCommit([]byte("other-genesis"))})
	dev.WriteRecord(Record{Unit: 2, Version: 1, Commit: UnitCommit([]byte("separate-chain"))})
	dev.PutHeader(otherGenesis)
	dev.PutHeader(separate)

	verdict := Analyze(dev.View(), fx.root)
	if verdict.SafeToUnlock() {
		t.Fatalf("image with a disconnected generation chain must not unlock")
	}
	if !verdict.MixedGen || verdict.FatalReason.Code != ReasonMixedGeneration {
		t.Fatalf("expected mixed generation reason, got %v", verdict.FatalReason)
	}
}

// TestDoubleWriteSameUnit: two writes to one unit inside a single flush epoch
// enumerate all persistence subsets (four masks, three distinct states) and
// the ordering guard rejects the ambiguous sequence.
func TestDoubleWriteSameUnit(t *testing.T) {
	fx := newFixture(t)

	actions := []Action{
		{Kind: WriteUnit, Unit: 7, Record: Record{Unit: 7, Version: 1, Commit: UnitCommit([]byte("v1"))}},
		{Kind: WriteUnit, Unit: 7, Record: Record{Unit: 7, Version: 2, Commit: UnitCommit([]byte("v2"))}},
	}
	if reason := ValidateOrdering(actions); reason.Code != ReasonRepeatedUnitWrite {
		t.Fatalf("expected repeated unit write rejection, got %v", reason)
	}

	states, unknown := ExhaustCrashes(fx.dev.View(), actions, DefaultEnumerationLimit)
	if unknown != nil {
		t.Fatalf("enumeration failed: %v", unknown)
	}
	versions := map[uint64]int{}
	for _, state := range states {
		if rec, ok := state.View.Unit(7); ok {
			versions[rec.Version]++
		} else {
			versions[0]++
		}
	}
	if len(states) != 3 {
		t.Fatalf("expected 3 distinct states (absent, v1, v2), got %d", len(states))
	}
	if versions[0] != 1 || versions[1] != 1 || versions[2] != 1 {
		t.Fatalf("expected one of each outcome, got %v", versions)
	}
}
