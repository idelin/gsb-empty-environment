package netsec

import (
	"reflect"
	"testing"
)

func TestReviewFlagsQuiescentWhileActive(t *testing.T) {
	steps := []ProposedStep{
		{Thread: 0, Event: Enter()},
		{Thread: 1, Event: ReplaceRoot(1)},
		{Thread: 0, Event: LoadRoot()},
		{Thread: 0, Event: Quiescent()},
		{Thread: 1, Event: RetireObject(1)},
		{Thread: 1, Event: TryAdvance(), ClaimedAdvance: boolPtr(true)},
		{Thread: 1, Event: TryAdvance(), ClaimedAdvance: boolPtr(true)},
		{Thread: 0, Event: Dereference()},
	}
	result, err := ReviewProposed(steps)
	if err != nil {
		t.Fatalf("ReviewProposed: %v", err)
	}
	if result.FirstUnsafe == nil {
		t.Fatal("expected Quiescent-while-active to be the first unsafe decision")
	}
	got := result.FirstUnsafe
	if got.Step != 3 || got.Kind != DecisionEvent {
		t.Fatalf("first unsafe = %+v, want step 3 event", got)
	}
	if got.Before.Threads[0].Active != true {
		t.Fatal("snapshot before unsafe decision should show active thread")
	}

	first, err := FirstUnsafeDecision(steps)
	if err != nil {
		t.Fatalf("FirstUnsafeDecision: %v", err)
	}
	if first == nil || first.Step != 3 {
		t.Fatalf("FirstUnsafeDecision = %+v", first)
	}
}

func TestReviewFlagsUnsafeAdvance(t *testing.T) {
	steps := []ProposedStep{
		{Thread: 0, Event: Enter()},
		{Thread: 1, Event: ReplaceRoot(1)},
		{Thread: 0, Event: LoadRoot()},
		{Thread: 1, Event: RetireObject(1)},
		{Thread: 1, Event: TryAdvance(), ClaimedAdvance: boolPtr(true)},
		{Thread: 1, Event: TryAdvance(), ClaimedAdvance: boolPtr(true)},
	}
	result, err := ReviewProposed(steps)
	if err != nil {
		t.Fatalf("ReviewProposed: %v", err)
	}
	if result.FirstUnsafe == nil {
		t.Fatal("expected unsafe advance")
	}
	got := result.FirstUnsafe
	if got.Step != 5 || got.Kind != DecisionAdvance || !got.Claimed || got.Expected {
		t.Fatalf("first unsafe = %+v, want unsafe advance at step 5", got)
	}
}

func TestReviewFlagsUnsafeReclaim(t *testing.T) {
	ref := ObjectRef{ID: 8, Gen: 0}
	steps := []ProposedStep{
		{Thread: 0, Event: ReplaceRoot(8)},
		{Thread: 0, Event: RetireObject(8)},
		{
			Thread:         0,
			Event:          TryAdvance(),
			ClaimedAdvance: boolPtr(true),
			ClaimedReclaim: []ObjectRef{ref},
		},
	}
	result, err := ReviewProposed(steps)
	if err != nil {
		t.Fatalf("ReviewProposed: %v", err)
	}
	if result.FirstUnsafe == nil {
		t.Fatal("expected unsafe reclaim")
	}
	got := result.FirstUnsafe
	if got.Step != 2 || got.Kind != DecisionReclaim || got.Ref != ref {
		t.Fatalf("first unsafe = %+v, want reclaim at step 2", got)
	}
}

func TestReviewAcceptsSafeReclaimAndNotesConservativeAdvance(t *testing.T) {
	ref := ObjectRef{ID: 6, Gen: 0}
	steps := []ProposedStep{
		{Thread: 0, Event: ReplaceRoot(6)},
		{Thread: 0, Event: RetireObject(6)},
		// The implementation conservatively declines the permitted advance.
		{Thread: 0, Event: TryAdvance(), ClaimedAdvance: boolPtr(false)},
		{Thread: 0, Event: TryAdvance(), ClaimedAdvance: boolPtr(true)},
		{Thread: 0, Event: TryAdvance(), ClaimedAdvance: boolPtr(true), ClaimedReclaim: []ObjectRef{ref}},
	}
	result, err := ReviewProposed(steps)
	if err != nil {
		t.Fatalf("ReviewProposed: %v", err)
	}
	if !result.Safe() {
		t.Fatalf("unexpected unsafe decision: %+v", result.FirstUnsafe)
	}
	if len(result.Mismatches) != 1 || result.Mismatches[0].Step != 2 {
		t.Fatalf("mismatches = %+v, want conservative advance note at step 2", result.Mismatches)
	}
	if !reflect.DeepEqual(result.Reclaimed, []ObjectRef{ref}) {
		t.Fatalf("reviewed reclaims = %v, want %v", result.Reclaimed, ref)
	}
	if obj, _ := result.FinalState.Object(ref); obj.Status != ObjectReclaimed {
		t.Fatalf("final status = %s, want reclaimed", obj.Status)
	}
}

func boolPtr(v bool) *bool {
	return &v
}
