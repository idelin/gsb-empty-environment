package taskpkg

import (
	"context"
	"errors"
	"math/big"
	"slices"
	"testing"
)

func TestLegitimateDropoutRecoversAggregate(t *testing.T) {
	h := newHarness(t)
	phase, err := NewPhase(h.config(3))
	if err != nil {
		t.Fatal(err)
	}
	completed := []DeviceID{"dev-a", "dev-b", "dev-c"}
	digest := RosterDigest(testRound, h.prime, completed)

	h.submit(phase, "dev-a", digest, SharePersonal)
	h.submit(phase, "dev-b", digest, SharePairwise)
	h.submit(phase, "dev-c", digest, SharePersonal)
	phase.Seal()

	plan, err := phase.BuildRecoveryPlan(digest, phase.CompletionSet(digest))
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.PersonalTargets) != 3 || len(plan.PairTargets) != 6 {
		t.Fatalf("unexpected plan size personal=%d pair=%d",
			len(plan.PersonalTargets), len(plan.PairTargets))
	}

	rec := newObservable(h)
	result, err := phase.Finalize(context.Background(), plan, rec)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if result.Confirmations != 3 {
		t.Fatalf("confirmations = %d, want 3", result.Confirmations)
	}
	if !slices.Equal(result.Completed, completed) ||
		!slices.Equal(result.Dropped, []DeviceID{"dev-d", "dev-e"}) {
		t.Fatalf("unexpected sets completed=%v dropped=%v", result.Completed, result.Dropped)
	}
	if !bytesEqual(result.Digest, digest) {
		t.Fatal("result digest does not identify the completion set")
	}
	if result.Aggregate.Cmp(h.expectedSum(completed)) != 0 {
		t.Fatalf("aggregate = %s, want %s", result.Aggregate, h.expectedSum(completed))
	}
	rec.assertPrivacy(t, completed)

	again, err := phase.Finalize(context.Background(), plan, newObservable(h))
	if err != nil {
		t.Fatalf("second finalize: %v", err)
	}
	if again.Aggregate.Cmp(result.Aggregate) != 0 || !bytesEqual(again.Digest, result.Digest) {
		t.Fatal("finalization is not deterministic across recoverers")
	}
}

func TestAllCompletedLeavesOnlyPersonalRemoval(t *testing.T) {
	h := newHarness(t)
	phase, _ := NewPhase(h.config(3))
	digest := RosterDigest(testRound, h.prime, h.roster)
	for i, id := range h.roster {
		share := SharePersonal
		if i%2 == 1 {
			share = SharePairwise
		}
		h.submit(phase, id, digest, share)
	}
	phase.Seal()

	plan, err := phase.BuildRecoveryPlan(digest, phase.CompletionSet(digest))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.PairTargets) != 0 || len(plan.Dropped) != 0 {
		t.Fatalf("full completion must request no pairwise seeds, got %d", len(plan.PairTargets))
	}
	rec := newObservable(h)
	result, err := phase.Finalize(context.Background(), plan, rec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Aggregate.Cmp(h.expectedSum(h.roster)) != 0 {
		t.Fatalf("aggregate = %s, want %s", result.Aggregate, h.expectedSum(h.roster))
	}
	if len(rec.pairCalls) != 1 || len(rec.pairCalls[0]) != 0 {
		t.Fatal("pairwise callback still invoked with edges")
	}
}

func TestSplitViewRejected(t *testing.T) {
	h := newHarness(t)
	phase, _ := NewPhase(h.config(2))
	// The roster only has five devices: a 3/3 split cannot be perfectly disjoint,
	// so sign two distinct three-device views with distinct trusted identities by
	// letting the middle device attest both competing membership claims.
	viewOne := RosterDigest(testRound, h.prime, []DeviceID{"dev-a", "dev-b", "dev-c"})
	viewTwo := RosterDigest(testRound, h.prime, []DeviceID{"dev-c", "dev-d", "dev-e"})

	h.submit(phase, "dev-a", viewOne, SharePersonal)
	h.submit(phase, "dev-b", viewOne, SharePairwise)
	h.submit(phase, "dev-c", viewOne, SharePersonal)
	h.submit(phase, "dev-d", viewTwo, SharePersonal)
	h.submit(phase, "dev-e", viewTwo, SharePairwise)
	phase.Seal()

	plan, err := phase.BuildRecoveryPlan(viewOne, phase.CompletionSet(viewOne))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := phase.Finalize(context.Background(), plan, newObservable(h)); !errors.Is(err, ErrSplitView) {
		t.Fatalf("error = %v, want ErrSplitView", err)
	}
}

func TestForeignDigestPlanRejected(t *testing.T) {
	h := newHarness(t)
	phase, _ := NewPhase(h.config(3))
	completed := []DeviceID{"dev-a", "dev-b", "dev-c"}
	digest := RosterDigest(testRound, h.prime, completed)
	h.submit(phase, "dev-a", digest, SharePersonal)
	h.submit(phase, "dev-b", digest, SharePairwise)
	h.submit(phase, "dev-c", digest, SharePersonal)
	phase.Seal()

	foreign := RosterDigest(testRound, h.prime, []DeviceID{"dev-a", "dev-b", "dev-c", "dev-d"})
	plan, err := phase.BuildRecoveryPlan(foreign, phase.CompletionSet(foreign))
	if err == nil {
		_, err = phase.Finalize(context.Background(), plan, newObservable(h))
	}
	if !errors.Is(err, ErrDigestMismatch) && !errors.Is(err, ErrInsufficientConfirmations) {
		t.Fatalf("error = %v, want digest/confirmation rejection", err)
	}
}

func TestMinorityDigestIsDroppedNotSplit(t *testing.T) {
	h := newHarness(t)
	phase, _ := NewPhase(h.config(3))
	completed := []DeviceID{"dev-a", "dev-b", "dev-c"}
	winner := RosterDigest(testRound, h.prime, completed)
	loser := RosterDigest(testRound, h.prime, []DeviceID{"dev-a", "dev-b", "dev-c", "dev-d"})

	h.submit(phase, "dev-a", winner, SharePersonal)
	h.submit(phase, "dev-b", winner, SharePersonal)
	h.submit(phase, "dev-c", winner, SharePersonal)
	h.submit(phase, "dev-d", loser, SharePairwise)
	phase.Seal()

	plan, err := phase.BuildRecoveryPlan(winner, phase.CompletionSet(winner))
	if err != nil {
		t.Fatal(err)
	}
	result, err := phase.Finalize(context.Background(), plan, newObservable(h))
	if err != nil {
		t.Fatalf("minority view should not block the winner: %v", err)
	}
	if !slices.Equal(result.Completed, completed) {
		t.Fatalf("completed = %v, want %v", result.Completed, completed)
	}
	if result.Aggregate.Cmp(h.expectedSum(completed)) != 0 {
		t.Fatalf("aggregate = %s, want %s", result.Aggregate, h.expectedSum(completed))
	}
}

func TestDuplicateIdentityRejected(t *testing.T) {
	h := newHarness(t)
	phase, _ := NewPhase(h.config(3))
	digest := RosterDigest(testRound, h.prime, []DeviceID{"dev-a", "dev-b", "dev-c"})
	h.submit(phase, "dev-a", digest, SharePersonal)
	err := phase.Submit(Submission{
		Device:      "dev-a",
		Digest:      digest,
		Signature:   h.sign("dev-a", digest),
		MaskedValue: big.NewInt(1),
		ShareType:   SharePairwise,
	})
	if !errors.Is(err, ErrDuplicateIdentity) {
		t.Fatalf("error = %v, want ErrDuplicateIdentity", err)
	}
}

func TestLateSubmissionRejected(t *testing.T) {
	h := newHarness(t)
	phase, _ := NewPhase(h.config(3))
	digest := RosterDigest(testRound, h.prime, []DeviceID{"dev-a", "dev-b", "dev-c"})
	phase.Seal()
	err := phase.Submit(Submission{
		Device:      "dev-a",
		Digest:      digest,
		Signature:   h.sign("dev-a", digest),
		MaskedValue: big.NewInt(1),
		ShareType:   SharePersonal,
	})
	if !errors.Is(err, ErrLateSubmission) {
		t.Fatalf("error = %v, want ErrLateSubmission", err)
	}

	// Finalization before any confirmed view also fails.
	plan := RecoveryPlan{
		Round:     testRound,
		Prime:     h.prime,
		Digest:    digest,
		Completed: nil,
		Dropped:   append([]DeviceID(nil), h.roster...),
	}
	_, err = phase.Finalize(context.Background(), plan, newObservable(h))
	if !errors.Is(err, ErrInsufficientConfirmations) {
		t.Fatalf("error = %v, want ErrInsufficientConfirmations", err)
	}
}

func TestInvalidAndUntrustedSignatures(t *testing.T) {
	h := newHarness(t)
	phase, _ := NewPhase(h.config(3))
	digest := RosterDigest(testRound, h.prime, []DeviceID{"dev-a", "dev-b", "dev-c"})

	bad := Submission{
		Device:      "dev-b",
		Digest:      digest,
		Signature:   make([]byte, 64),
		MaskedValue: big.NewInt(1),
		ShareType:   SharePersonal,
	}
	if err := phase.Submit(bad); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("error = %v, want ErrInvalidSignature", err)
	}

	unknown := Submission{
		Device:      "dev-x",
		Digest:      digest,
		Signature:   make([]byte, 64),
		MaskedValue: big.NewInt(1),
		ShareType:   SharePersonal,
	}
	if err := phase.Submit(unknown); !errors.Is(err, ErrUntrustedDevice) {
		t.Fatalf("error = %v, want ErrUntrustedDevice", err)
	}
}

func TestThresholdEdge(t *testing.T) {
	h := newHarness(t)
	phase, _ := NewPhase(h.config(3))
	digest := RosterDigest(testRound, h.prime, []DeviceID{"dev-a", "dev-b"})
	h.submit(phase, "dev-a", digest, SharePersonal)
	h.submit(phase, "dev-b", digest, SharePersonal)
	phase.Seal()

	plan, err := phase.BuildRecoveryPlan(digest, phase.CompletionSet(digest))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := phase.Finalize(context.Background(), plan, newObservable(h)); !errors.Is(err, ErrInsufficientConfirmations) {
		t.Fatalf("error = %v, want ErrInsufficientConfirmations", err)
	}
}
