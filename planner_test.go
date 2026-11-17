package netsec

import (
	"errors"
	"reflect"
	"testing"
)

func mustPlanner(t *testing.T, cfg Config, spans ...Span) *Planner {
	t.Helper()
	planner, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	for _, span := range spans {
		if err := planner.AddFree(span); err != nil {
			t.Fatalf("AddFree(%v): %v", span, err)
		}
	}
	return planner
}

func mustAllocate(t *testing.T, planner *Planner, now Tick, span Span) Allocation {
	t.Helper()
	plan, err := planner.Allocate(now, span)
	if err != nil {
		t.Fatalf("Allocate(%d, %v): %v", now, span, err)
	}
	if plan.Status != StatusGranted {
		t.Fatalf("Allocate(%d, %v) = %+v, want granted", now, span, plan)
	}
	return plan.Allocation
}

func mustRelease(t *testing.T, planner *Planner, now Tick, allocation Allocation) {
	t.Helper()
	if err := planner.Release(now, allocation); err != nil {
		t.Fatalf("Release(%d, %+v): %v", now, allocation, err)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "no labels", cfg: Config{Quarantine: 1}},
		{name: "no quarantine", cfg: Config{LabelCount: 1}},
		{name: "bad policy", cfg: Config{LabelCount: 1, Quarantine: 1, Exhaustion: 9}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New(%+v) error = %v, want ErrInvalidConfig", test.cfg, err)
			}
		})
	}
}

func TestWaitReportsFirstSafeRetryAndUsesExpiryInstant(t *testing.T) {
	span := Span{Start: 0, End: 4}
	planner := mustPlanner(t, Config{
		LabelCount: 2,
		Quarantine: 3,
		Exhaustion: WaitWhenExhausted,
	}, span)

	first := mustAllocate(t, planner, 0, span)
	if first.Label != 0 {
		t.Fatalf("first label = %d, want 0", first.Label)
	}
	mustRelease(t, planner, 0, first)

	second := mustAllocate(t, planner, 1, span)
	if second.Label != 1 {
		t.Fatalf("second label = %d, want 1", second.Label)
	}
	mustRelease(t, planner, 1, second)

	plan, err := planner.Allocate(2, span)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != StatusWait || plan.Reason != ReasonQuarantined {
		t.Fatalf("plan at 2 = %+v, want quarantine wait", plan)
	}
	if plan.RetryAt != 3 {
		t.Fatalf("RetryAt = %d, want 3", plan.RetryAt)
	}

	plan, err = planner.Allocate(3, span)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != StatusGranted || plan.Allocation.Label != 0 {
		t.Fatalf("plan at expiry = %+v, want label 0 grant", plan)
	}
	if plan.Allocation.ID != 3 {
		t.Fatalf("granted allocation ID = %d, want 3; wait must not consume an ID", plan.Allocation.ID)
	}
}

func TestRejectPolicyNeverSilentlyReuses(t *testing.T) {
	span := Span{Start: 10, End: 18}
	planner := mustPlanner(t, Config{
		LabelCount: 1,
		Quarantine: 5,
		Exhaustion: RejectWhenExhausted,
	}, span)

	first := mustAllocate(t, planner, 0, span)
	mustRelease(t, planner, 0, first)

	plan, err := planner.Allocate(4, span)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != StatusRejected || plan.Reason != ReasonQuarantined {
		t.Fatalf("plan at 4 = %+v, want explicit rejection", plan)
	}
	if plan.RetryAt != 5 {
		t.Fatalf("RetryAt = %d, want 5", plan.RetryAt)
	}
	if plan.Allocation != (Allocation{}) {
		t.Fatalf("rejected plan allocated %+v", plan.Allocation)
	}

	plan, err = planner.Allocate(5, span)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != StatusGranted || plan.Allocation.Label != 0 {
		t.Fatalf("plan at expiry = %+v, want label 0 grant", plan)
	}
}

func TestPartialIntervalReuseTracksOverlap(t *testing.T) {
	all := Span{Start: 0, End: 8}
	planner := mustPlanner(t, Config{
		LabelCount: 2,
		Quarantine: 10,
		Exhaustion: RejectWhenExhausted,
	}, all)

	left := mustAllocate(t, planner, 0, Span{Start: 0, End: 4})
	mustRelease(t, planner, 0, left)

	right := mustAllocate(t, planner, 1, Span{Start: 4, End: 8})
	if right.Label != 0 {
		t.Fatalf("disjoint reuse label = %d, want 0", right.Label)
	}
	mustRelease(t, planner, 1, right)

	whole := mustAllocate(t, planner, 2, all)
	if whole.Label != 1 {
		t.Fatalf("overlapping whole-span label = %d, want 1", whole.Label)
	}
	mustRelease(t, planner, 2, whole)

	for _, now := range []Tick{3, 10} {
		plan, err := planner.Allocate(now, all)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Status != StatusRejected || plan.RetryAt != 11 {
			t.Fatalf("plan at %d = %+v, want rejection with retry 11", now, plan)
		}
	}

	plan, err := planner.Allocate(11, all)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != StatusGranted || plan.Allocation.Label != 0 {
		t.Fatalf("plan at 11 = %+v, want label 0 grant", plan)
	}
}

func TestSplitAndMergePreserveExactHistory(t *testing.T) {
	all := Span{Start: 0, End: 4}
	planner := mustPlanner(t, Config{
		LabelCount: 2,
		Quarantine: 10,
		Exhaustion: RejectWhenExhausted,
	}, all)

	left := mustAllocate(t, planner, 0, Span{Start: 0, End: 2})
	mustRelease(t, planner, 0, left)
	right := mustAllocate(t, planner, 0, Span{Start: 2, End: 4})
	if right.Label != 0 {
		t.Fatalf("right label = %d, want 0", right.Label)
	}
	mustRelease(t, planner, 1, right)

	if err := planner.Split(all, 2); err != nil {
		t.Fatal(err)
	}
	segments := planner.Segments()
	if len(segments) != 2 {
		t.Fatalf("segments after split = %+v", segments)
	}
	wantLeft := []Quarantine{{
		Span:      Span{Start: 0, End: 2},
		Label:     0,
		ExpiresAt: 10,
	}}
	wantRight := []Quarantine{{
		Span:      Span{Start: 2, End: 4},
		Label:     0,
		ExpiresAt: 11,
	}}
	if !reflect.DeepEqual(segments[0].History, wantLeft) {
		t.Fatalf("left history = %+v, want %+v", segments[0].History, wantLeft)
	}
	if !reflect.DeepEqual(segments[1].History, wantRight) {
		t.Fatalf("right history = %+v, want %+v", segments[1].History, wantRight)
	}

	if err := planner.Merge(Span{Start: 0, End: 2}, Span{Start: 2, End: 4}); err != nil {
		t.Fatal(err)
	}
	segments = planner.Segments()
	if len(segments) != 1 {
		t.Fatalf("segments after merge = %+v", segments)
	}
	wantMerged := []Quarantine{wantLeft[0], wantRight[0]}
	if !reflect.DeepEqual(segments[0].History, wantMerged) {
		t.Fatalf("merged history = %+v, want %+v", segments[0].History, wantMerged)
	}
}

func TestSplitClipsHistoryToBothChildren(t *testing.T) {
	all := Span{Start: 0, End: 4}
	planner := mustPlanner(t, Config{
		LabelCount: 1,
		Quarantine: 5,
		Exhaustion: RejectWhenExhausted,
	}, all)

	allocation := mustAllocate(t, planner, 0, all)
	mustRelease(t, planner, 0, allocation)
	if err := planner.Split(all, 2); err != nil {
		t.Fatal(err)
	}
	segments := planner.Segments()
	if len(segments) != 2 {
		t.Fatalf("segments = %+v", segments)
	}
	for i, want := range []Span{{Start: 0, End: 2}, {Start: 2, End: 4}} {
		if len(segments[i].History) != 1 {
			t.Fatalf("segment %d history = %+v", i, segments[i].History)
		}
		if segments[i].History[0].Span != want {
			t.Fatalf("segment %d history span = %v, want %v", i, segments[i].History[0].Span, want)
		}
	}

	if err := planner.Merge(Span{Start: 0, End: 2}, Span{Start: 2, End: 4}); err != nil {
		t.Fatal(err)
	}
	segments = planner.Segments()
	wantHistory := []Quarantine{{
		Span:      all,
		Label:     0,
		ExpiresAt: 5,
	}}
	if len(segments) != 1 || !reflect.DeepEqual(segments[0].History, wantHistory) {
		t.Fatalf("merged segments = %+v, want history %+v", segments, wantHistory)
	}

	plan, err := planner.Allocate(5, all)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != StatusGranted || plan.Allocation.Label != 0 {
		t.Fatalf("plan at expiry = %+v, want label 0 grant", plan)
	}
}

func TestAllocationAcrossAdjacentSplitSegmentsUsesCombinedHistory(t *testing.T) {
	all := Span{Start: 0, End: 8}
	planner := mustPlanner(t, Config{
		LabelCount: 1,
		Quarantine: 10,
		Exhaustion: RejectWhenExhausted,
	}, all)

	allocation := mustAllocate(t, planner, 0, all)
	mustRelease(t, planner, 0, allocation)
	if err := planner.Split(all, 4); err != nil {
		t.Fatal(err)
	}

	plan, err := planner.Allocate(1, Span{Start: 2, End: 6})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != StatusRejected || plan.RetryAt != 10 {
		t.Fatalf("plan at 1 = %+v, want rejection with retry 10", plan)
	}

	plan, err = planner.Allocate(10, Span{Start: 2, End: 6})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != StatusGranted || plan.Allocation.Label != 0 {
		t.Fatalf("plan at 10 = %+v, want label 0 grant", plan)
	}
}

func TestFirstFitSkipsQuarantinedAddressRange(t *testing.T) {
	planner := mustPlanner(t, Config{
		LabelCount: 1,
		Quarantine: 10,
		Exhaustion: WaitWhenExhausted,
	}, Span{Start: 0, End: 2}, Span{Start: 3, End: 5})

	first := mustAllocate(t, planner, 0, Span{Start: 0, End: 2})
	mustRelease(t, planner, 0, first)

	plan, err := planner.AllocateFirstFit(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != StatusGranted {
		t.Fatalf("first-fit plan = %+v, want grant", plan)
	}
	if plan.Allocation.Span != (Span{Start: 3, End: 5}) {
		t.Fatalf("first-fit span = %v, want [3,5)", plan.Allocation.Span)
	}
}

func TestClockRegressionAndStaleRelease(t *testing.T) {
	span := Span{Start: 0, End: 2}
	planner := mustPlanner(t, Config{
		LabelCount: 2,
		Quarantine: 1,
		Exhaustion: RejectWhenExhausted,
	}, span)

	first := mustAllocate(t, planner, 5, span)
	mustRelease(t, planner, 6, first)
	if _, err := planner.Allocate(5, span); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("clock regression error = %v", err)
	}

	second := mustAllocate(t, planner, 6, span)
	if second.ID == first.ID {
		t.Fatal("reallocated allocation reused ID")
	}
	if err := planner.Release(6, first); !errors.Is(err, ErrAllocationNotFound) {
		t.Fatalf("stale release error = %v, want ErrAllocationNotFound", err)
	}
	segments := planner.Segments()
	if len(segments) != 1 || !segments[0].Allocated || segments[0].Allocation.ID != second.ID {
		t.Fatalf("stale release changed state: %+v", segments)
	}
}

func TestExhaustiveExpiryBoundary(t *testing.T) {
	span := Span{Start: 0, End: 1}
	policies := []ExhaustionPolicy{RejectWhenExhausted, WaitWhenExhausted}

	for _, policy := range policies {
		for labelCount := uint64(1); labelCount <= 3; labelCount++ {
			for quarantine := Tick(1); quarantine <= 5; quarantine++ {
				for releaseAt := Tick(0); releaseAt <= 4; releaseAt++ {
					expiresAt := releaseAt + quarantine
					for now := releaseAt; now <= expiresAt+2; now++ {
						planner := mustPlanner(t, Config{
							LabelCount: labelCount,
							Quarantine: quarantine,
							Exhaustion: policy,
						}, span)
						allocation := mustAllocate(t, planner, 0, span)
						mustRelease(t, planner, releaseAt, allocation)

						states, err := planner.LabelStates(now, span)
						if err != nil {
							t.Fatal(err)
						}
						wantAvailable := now >= expiresAt
						if states[0].Available != wantAvailable {
							t.Fatalf(
								"policy=%d labels=%d q=%d release=%d now=%d: label 0 available=%v, want %v",
								policy, labelCount, quarantine, releaseAt, now,
								states[0].Available, wantAvailable,
							)
						}

						plan, err := planner.Allocate(now, span)
						if err != nil {
							t.Fatal(err)
						}
						if wantAvailable {
							if plan.Status != StatusGranted || plan.Allocation.Label != 0 {
								t.Fatalf(
									"policy=%d labels=%d q=%d release=%d now=%d: plan=%+v, want label 0",
									policy, labelCount, quarantine, releaseAt, now, plan,
								)
							}
							continue
						}
						if labelCount > 1 {
							if plan.Status != StatusGranted || plan.Allocation.Label != 1 {
								t.Fatalf(
									"policy=%d labels=%d q=%d release=%d now=%d: plan=%+v, want label 1",
									policy, labelCount, quarantine, releaseAt, now, plan,
								)
							}
							continue
						}
						wantStatus := StatusRejected
						if policy == WaitWhenExhausted {
							wantStatus = StatusWait
						}
						if plan.Status != wantStatus || plan.RetryAt != expiresAt {
							t.Fatalf(
								"policy=%d labels=%d q=%d release=%d now=%d: plan=%+v, want %d at %d",
								policy, labelCount, quarantine, releaseAt, now, plan,
								wantStatus, expiresAt,
							)
						}
					}
				}
			}
		}
	}
}

func TestExhaustivePartialIntervalQuarantine(t *testing.T) {
	all := Span{Start: 0, End: 4}
	for releasedStart := uint64(0); releasedStart < 4; releasedStart++ {
		for releasedEnd := releasedStart + 1; releasedEnd <= 4; releasedEnd++ {
			for requestStart := uint64(0); requestStart < 4; requestStart++ {
				for requestEnd := requestStart + 1; requestEnd <= 4; requestEnd++ {
					planner := mustPlanner(t, Config{
						LabelCount: 2,
						Quarantine: 10,
						Exhaustion: RejectWhenExhausted,
					}, all)
					released := Span{Start: releasedStart, End: releasedEnd}
					requested := Span{Start: requestStart, End: requestEnd}
					allocation := mustAllocate(t, planner, 0, released)
					mustRelease(t, planner, 0, allocation)

					overlaps := requested.Start < released.End && released.Start < requested.End
					states, err := planner.LabelStates(1, requested)
					if err != nil {
						t.Fatal(err)
					}
					if states[0].Available == overlaps {
						t.Fatalf(
							"released=%v requested=%v: label 0 available=%v, overlap=%v",
							released, requested, states[0].Available, overlaps,
						)
					}

					plan, err := planner.Allocate(1, requested)
					if err != nil {
						t.Fatal(err)
					}
					wantLabel := Label(0)
					if overlaps {
						wantLabel = 1
					}
					if plan.Status != StatusGranted || plan.Allocation.Label != wantLabel {
						t.Fatalf(
							"released=%v requested=%v: plan=%+v, want label %d",
							released, requested, plan, wantLabel,
						)
					}
				}
			}
		}
	}
}
