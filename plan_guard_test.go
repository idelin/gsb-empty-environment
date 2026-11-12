package taskpkg

import (
	"context"
	"errors"
	"math/big"
	"slices"
	"testing"
	"time"
)

func basePlan(t *testing.T, h *testHarness) (*Phase, RecoveryPlan) {
	t.Helper()
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
		t.Fatal(err)
	}
	return phase, plan
}

func TestPlanRejectsIntraSetPair(t *testing.T) {
	h := newHarness(t)
	phase, plan := basePlan(t, h)
	plan.PairTargets = append(plan.PairTargets, NewPairID("dev-a", "dev-b"))
	err := phase.ValidatePlan(plan)
	if !errors.Is(err, ErrIntraSetPair) {
		t.Fatalf("error = %v, want ErrIntraSetPair", err)
	}
}

func TestPlanRejectsDroppedDevicePersonalSeed(t *testing.T) {
	h := newHarness(t)
	phase, plan := basePlan(t, h)
	// Adding a dropped device's personal seed on top of the legitimate set is the
	// double-unmask primitive: combined with a cross-set pair it exposes a value.
	plan.PersonalTargets = append(plan.PersonalTargets, "dev-d")
	err := phase.ValidatePlan(plan)
	if !errors.Is(err, ErrDoubleUnmask) {
		t.Fatalf("error = %v, want ErrDoubleUnmask", err)
	}
}

func TestPlanRejectsPairWithoutSurvivor(t *testing.T) {
	h := newHarness(t)
	phase, plan := basePlan(t, h)
	plan.PairTargets = append(plan.PairTargets, NewPairID("dev-d", "dev-e"))
	err := phase.ValidatePlan(plan)
	if !errors.Is(err, ErrEndpointNotCompleted) {
		t.Fatalf("error = %v, want ErrEndpointNotCompleted", err)
	}
}

func TestPlanRejectsWrongOrientationAndDigest(t *testing.T) {
	h := newHarness(t)
	phase, plan := basePlan(t, h)

	reversed := plan.PairTargets[0]
	reversed.Low, reversed.High = reversed.High, reversed.Low
	plan.PairTargets[0] = reversed
	if err := phase.ValidatePlan(plan); !errors.Is(err, ErrPairOrientation) {
		t.Fatalf("error = %v, want ErrPairOrientation", err)
	}

	_, good := basePlan(t, h)
	good.Digest[0] ^= 0x01
	if err := phase.ValidatePlan(good); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("error = %v, want ErrDigestMismatch", err)
	}
}

func TestPlanRejectsDuplicateTargets(t *testing.T) {
	h := newHarness(t)
	phase, plan := basePlan(t, h)

	personalDup := plan
	personalDup.PersonalTargets = append(append([]DeviceID(nil), plan.PersonalTargets...), "dev-a")
	if err := phase.ValidatePlan(personalDup); !errors.Is(err, ErrRecoveryPlan) {
		t.Fatalf("error = %v, want ErrRecoveryPlan", err)
	}

	pairDup := plan
	pairDup.PairTargets = append(append([]PairID(nil), plan.PairTargets...), plan.PairTargets[0])
	if err := phase.ValidatePlan(pairDup); !errors.Is(err, ErrRecoveryPlan) {
		t.Fatalf("error = %v, want ErrRecoveryPlan", err)
	}
}

type mismatchRecoverer struct {
	kind string
}

func (m mismatchRecoverer) RecoverPersonal(_ context.Context, devices []DeviceID) (PersonalRecovery, error) {
	out := make(PersonalRecovery)
	if m.kind == "extra" {
		for _, id := range devices {
			out[id] = big.NewInt(1)
		}
		out["ghost"] = big.NewInt(1)
		return out, nil
	}
	for _, id := range devices[:len(devices)-1] {
		out[id] = big.NewInt(1)
	}
	return out, nil
}

func (m mismatchRecoverer) RecoverPairwise(_ context.Context, pairs []PairID) (PairwiseRecovery, error) {
	out := make(PairwiseRecovery, len(pairs))
	for _, pair := range pairs {
		out[pair] = PairSeed{LowMask: big.NewInt(1), HighMask: big.NewInt(1)}
	}
	return out, nil
}

func TestRecoveryMismatchRejected(t *testing.T) {
	h := newHarness(t)
	phase, plan := basePlan(t, h)

	if _, err := phase.Finalize(context.Background(), plan, mismatchRecoverer{kind: "missing"}); !errors.Is(err, ErrRecoveryMismatch) {
		t.Fatalf("error = %v, want ErrRecoveryMismatch", err)
	}
	if _, err := phase.Finalize(context.Background(), plan, mismatchRecoverer{kind: "extra"}); !errors.Is(err, ErrRecoveryMismatch) {
		t.Fatalf("error = %v, want ErrRecoveryMismatch", err)
	}
}

// TestPairwiseSignConvention directly checks that an additive low endpoint gets
// subtracted back and a subtractive high endpoint gets added back.
func TestPairwiseSignConvention(t *testing.T) {
	h := newHarness(t)
	phase, plan := basePlan(t, h)

	rec := newObservable(h)
	result, err := phase.Finalize(context.Background(), plan, rec)
	if err != nil {
		t.Fatal(err)
	}

	// Independent recomputation with the documented low-add/high-subtract rule.
	sum := big.NewInt(0)
	for _, id := range plan.Completed {
		sum.Add(sum, phase.submissions[id].MaskedValue)
	}
	for _, id := range plan.Completed {
		sum.Sub(sum, h.self[id])
	}
	for _, pair := range plan.PairTargets {
		scalar := h.pair[pair]
		if slices.Contains(plan.Completed, pair.Low) {
			sum.Sub(sum, scalar)
		} else {
			sum.Add(sum, scalar)
		}
	}
	sum.Mod(sum, h.prime)
	if sum.Cmp(result.Aggregate) != 0 {
		t.Fatalf("manual correction = %s, package aggregate = %s", sum, result.Aggregate)
	}
	if result.Aggregate.Cmp(h.expectedSum(plan.Completed)) != 0 {
		t.Fatalf("aggregate = %s, want raw sum %s", result.Aggregate, h.expectedSum(plan.Completed))
	}
}

func TestFinalizeRequiresSeal(t *testing.T) {
	h := newHarness(t)
	phase, err := NewPhase(h.config(3))
	if err != nil {
		t.Fatal(err)
	}
	digest := RosterDigest(testRound, h.prime, []DeviceID{"dev-a", "dev-b", "dev-c"})
	h.submit(phase, "dev-a", digest, SharePersonal)
	plan := RecoveryPlan{Round: testRound, Prime: h.prime, Digest: digest}
	if _, err := phase.Finalize(context.Background(), plan, newObservable(h)); !errors.Is(err, ErrPhaseNotSealed) {
		t.Fatalf("error = %v, want ErrPhaseNotSealed", err)
	}
}

func TestFinalizeRejectsCompletedDeviceWithoutSubmission(t *testing.T) {
	h := newHarness(t)
	phase, _ := basePlan(t, h)
	// Forge a plan that lists every roster member as completed even though only
	// three devices submitted.
	forgedDigest := RosterDigest(testRound, h.prime, h.roster)
	forged, err := phase.BuildRecoveryPlan(forgedDigest, append([]DeviceID(nil), h.roster...))
	if err != nil {
		t.Fatalf("structurally valid forged plan should build: %v", err)
	}
	_, err = phase.Finalize(context.Background(), forged, newObservable(h))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("error = %v, want ErrDigestMismatch", err)
	}
}

type blockedRecoverer struct{}

func (blockedRecoverer) RecoverPersonal(ctx context.Context, _ []DeviceID) (PersonalRecovery, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockedRecoverer) RecoverPairwise(context.Context, []PairID) (PairwiseRecovery, error) {
	return nil, nil
}

func TestFinalizeHonorsContextCancellation(t *testing.T) {
	h := newHarness(t)
	phase, plan := basePlan(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := phase.Finalize(ctx, plan, blockedRecoverer{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestConfigValidation(t *testing.T) {
	h := newHarness(t)
	base := h.config(3)

	badPrime := base
	badPrime.Prime = big.NewInt(100)
	if _, err := NewPhase(badPrime); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("composite prime: %v", err)
	}

	badThreshold := base
	badThreshold.Threshold = 0
	if _, err := NewPhase(badThreshold); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero threshold: %v", err)
	}

	dupRoster := base
	dupRoster.Roster = append(dupRoster.Roster, "dev-a")
	if _, err := NewPhase(dupRoster); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("duplicate roster: %v", err)
	}

	missingKey := base
	delete(missingKey.TrustedKeys, "dev-a")
	if _, err := NewPhase(missingKey); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("missing key: %v", err)
	}
}
