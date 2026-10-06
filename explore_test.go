package main

import "testing"

func load(t *testing.T, path string) *Spec {
	t.Helper()
	spec, err := LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec(%s): %v", path, err)
	}
	return spec
}

func TestLeakFindsShortestViolation(t *testing.T) {
	spec := load(t, "examples/leak.json")
	res := Check(spec)
	if res.Status != StatusViolation {
		t.Fatalf("status = %q, want %q", res.Status, StatusViolation)
	}
	// Shortest leak: B acquires, then A's acquire and release both fail.
	if len(res.Trace) != 3 {
		t.Fatalf("trace length = %d, want 3 (shortest): %+v", len(res.Trace), res.Trace)
	}
	if len(res.Unreleased) != 1 || res.Unreleased[0] != "lock=held" {
		t.Fatalf("unreleased = %v, want [lock=held]", res.Unreleased)
	}
	if err := VerifyCounterexample(spec, res); err != nil {
		t.Fatalf("replay verification failed: %v", err)
	}
}

func TestSafeWhenAllReleased(t *testing.T) {
	spec := load(t, "examples/safe.json")
	res := Check(spec)
	if res.Status != StatusSafe {
		t.Fatalf("status = %q, want %q (trace %+v)", res.Status, StatusSafe, res.Trace)
	}
}

func TestUnknownOnStateBudget(t *testing.T) {
	spec := load(t, "examples/safe.json")
	spec.Budget.MaxStates = 2
	res := Check(spec)
	if res.Status != StatusUnknown {
		t.Fatalf("status = %q, want %q", res.Status, StatusUnknown)
	}
	if res.Reason == "" {
		t.Fatal("unknown result should carry a reason")
	}
}

func TestUnknownOnDepthBudget(t *testing.T) {
	spec := load(t, "examples/leak.json")
	spec.Budget.MaxDepth = 1
	res := Check(spec)
	if res.Status != StatusUnknown {
		t.Fatalf("status = %q, want %q", res.Status, StatusUnknown)
	}
}

func TestReplayRejectsTamperedTrace(t *testing.T) {
	spec := load(t, "examples/leak.json")
	res := Check(spec)
	if res.Status != StatusViolation {
		t.Fatalf("status = %q, want %q", res.Status, StatusViolation)
	}

	// Flip an outcome: the recorded transition no longer matches.
	bad := append([]Step(nil), res.Trace...)
	if bad[0].Outcome == "ok" {
		bad[0].Outcome = "fail"
	} else {
		bad[0].Outcome = "ok"
	}
	if _, err := Replay(spec, bad); err == nil {
		t.Fatal("replay accepted a tampered outcome")
	}

	// Truncate: scripts no longer run to completion.
	if _, err := Replay(spec, res.Trace[:len(res.Trace)-1]); err == nil {
		t.Fatal("replay accepted a truncated trace")
	}

	// Reorder steps across scripts' own order must be caught when it
	// violates a script's internal order; swap first two steps of a
	// two-action script trace.
	orderBad := []Step{
		{Script: "A", Action: "release", Outcome: "fail"},
		{Script: "A", Action: "acquire", Outcome: "ok"},
		{Script: "B", Action: "acquire", Outcome: "fail"},
	}
	if _, err := Replay(spec, orderBad); err == nil {
		t.Fatal("replay accepted an order-violating trace")
	}
}

func TestReplayConfirmsUnreleased(t *testing.T) {
	spec := load(t, "examples/leak.json")
	res := Check(spec)
	unrel, err := Replay(spec, res.Trace)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(unrel) != 1 || unrel[0] != "lock=held" {
		t.Fatalf("unreleased = %v, want [lock=held]", unrel)
	}
}
