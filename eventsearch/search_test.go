package eventsearch_test

import (
	"strings"
	"testing"

	"example.org/netsec/q0070/eventsearch"
)

func TestSafeModelHasNoPostRevokeCall(t *testing.T) {
	for _, opts := range []eventsearch.Options{
		{},
		{AllowResurrection: true},
		{AllowResurrection: true, OccupiedGlobalSlot: true},
		{OccupiedGlobalSlot: true},
	} {
		result := eventsearch.FindShortest(eventsearch.Safe, opts)
		if result.Found {
			t.Fatalf("safe model produced counterexample for %+v:\n%s", opts, result.Trace())
		}
		if result.ResourceCalls != 0 {
			t.Fatalf("safe search reported %d resource calls for %+v", result.ResourceCalls, opts)
		}
	}
}

func TestPendingShortestTraceIsDepthSix(t *testing.T) {
	result := eventsearch.FindShortest(eventsearch.Pending, eventsearch.Options{
		AllowResurrection:  false,
		OccupiedGlobalSlot: true,
	})
	if !result.Found {
		t.Fatal("pending fixture was not detected")
	}
	if result.Depth != 6 || len(result.Steps) != 6 {
		t.Fatalf("shortest depth = %d, want 6:\n%s", result.Depth, result.Trace())
	}
	if result.ResourceCalls != 1 || result.Final.ResourceCalls != 1 {
		t.Fatalf("finding must contain a real resource call: %+v", result)
	}

	wantActions := []eventsearch.Action{
		eventsearch.ActionAllocate,
		eventsearch.ActionIssueTicket,
		eventsearch.ActionStrongDrop,
		eventsearch.ActionRevoke,
		eventsearch.ActionFinalize,
		eventsearch.ActionCall,
	}
	for i, step := range result.Steps {
		if step.Action != wantActions[i] {
			t.Fatalf("step %d action = %s, want %s", i, step.Action, wantActions[i])
		}
		if step.Thread < 0 || step.Thread >= eventsearch.MaxThreads {
			t.Fatalf("step %d uses thread %d", i, step.Thread)
		}
	}
	if eventsearch.MaxThreads != 3 {
		t.Fatalf("search must be bounded to three threads")
	}

	if !result.Final.Revoked || !result.Final.WeakGetAttempted || result.Final.WeakGetSucceeded {
		t.Fatalf("bad final state: %+v", result.Final)
	}
	if result.Final.GlobalSlotG1 != result.Final.ObjectID ||
		result.Final.GlobalSlotG2 != result.Final.ObjectID {
		t.Fatalf("pending finalizer did not overwrite both requested slots: %+v", result.Final)
	}
	if len(result.Final.Requests) != 2 {
		t.Fatalf("expected two resurrection requests: %+v", result.Final.Requests)
	}
	for _, request := range result.Final.Requests {
		if !request.Accepted || request.Reason != "VULNERABLE_ACCEPTED" {
			t.Fatalf("pending fixture should ignore policy: %+v", request)
		}
	}
}

func TestPendingBugIgnoresHostPolicyAndSlotState(t *testing.T) {
	for _, opts := range []eventsearch.Options{
		{},
		{AllowResurrection: true, OccupiedGlobalSlot: true},
		{AllowResurrection: false, OccupiedGlobalSlot: true},
	} {
		result := eventsearch.FindShortest(eventsearch.Pending, opts)
		if !result.Found || result.Depth != 6 || result.ResourceCalls < 1 {
			t.Fatalf("pending search failed for %+v:\n%s", opts, result.Trace())
		}
		if !strings.Contains(result.Trace(), "SUCCEEDED AFTER REVOKE") {
			t.Fatalf("trace lacks explicit finding:\n%s", result.Trace())
		}
	}
}

func TestTraceDistinguishesFinalizerRunFromResourceCall(t *testing.T) {
	safe := eventsearch.FindShortest(eventsearch.Safe, eventsearch.Options{AllowResurrection: true})
	pending := eventsearch.FindShortest(eventsearch.Pending, eventsearch.Options{})

	if safe.Found {
		t.Fatal("safe model incorrectly flagged")
	}
	if !pending.Found || pending.ResourceCalls != 1 {
		t.Fatal("pending model did not produce a real call")
	}
	if strings.Contains(safe.Trace(), "SUCCEEDED AFTER REVOKE") {
		t.Fatal("safe trace contains a false positive")
	}
	if !strings.Contains(pending.Trace(), "WeakGet denied") {
		t.Fatal("finalizer callback WeakGet attempt was not exercised")
	}
}
