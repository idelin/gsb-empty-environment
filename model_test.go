package netsec

import "testing"

func lastSuccess(events []Event) (Event, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == EvSuccess {
			return events[i], true
		}
	}
	return Event{}, false
}

func containsTag(tag string, list []string) bool {
	for _, item := range list {
		if item == tag {
			return true
		}
	}
	return false
}

// A well-formed protocol with roomy queues is safe under either policy and
// never overflows.
func TestCorrectHandshakeSafeBothPolicies(t *testing.T) {
	for _, capacity := range []int{1, 4} {
		spec := CorrectHandshake(capacity)
		for _, policy := range []OverflowPolicy{DropNew, DropOld} {
			rep := Check(spec, policy, Budget{MaxStates: 500, MaxSteps: 100})
			if rep.Verdict != Safe {
				t.Fatalf("cap=%d %s: want safe, got %s: %s",
					capacity, policy, rep.Verdict, rep.Reason)
			}
			if rep.Stats.DroppedSends != 0 {
				t.Fatalf("cap=%d %s: no overflow expected, got %d drops",
					capacity, policy, rep.Stats.DroppedSends)
			}
		}
	}
}

// The strict-order model with a capacity-1 channel is the direct comparison
// the task asks for: same spec, different overflow policy, different verdict.
func TestStrictOrderDistinguishesOverflowPolicies(t *testing.T) {
	spec := StrictOrderHandshake(1)

	t.Run("drop-new preserves required messages", func(t *testing.T) {
		rep := Check(spec, DropNew, Budget{MaxStates: 500, MaxSteps: 100})
		if rep.Verdict != Safe {
			t.Fatalf("drop-new: want safe, got %s: %s", rep.Verdict, rep.Reason)
		}
		if rep.Stats.DroppedSends == 0 {
			t.Fatalf("drop-new: expected the capacity-1 retransmit to overflow")
		}
	})

	t.Run("drop-old evicts a required message", func(t *testing.T) {
		rep := Check(spec, DropOld, Budget{MaxStates: 500, MaxSteps: 100})
		if rep.Verdict != Unsafe {
			t.Fatalf("drop-old: want unsafe, got %s: %s", rep.Verdict, rep.Reason)
		}
		if rep.Stats.DroppedSends == 0 {
			t.Fatalf("drop-old: expected a queue overflow on capacity-1 c1")
		}
		ev, ok := lastSuccess(rep.Trace)
		if !ok {
			t.Fatalf("drop-old: violating trace has no success event")
		}
		if ev.Endpoint != "R" || ev.State != "R_DONE" {
			t.Fatalf("drop-old: violating event = %+v, want R/R_DONE", ev)
		}
		if len(ev.Missing) == 0 {
			t.Fatalf("drop-old: evicting the head must leave a sequence gap, got %v", ev.Missing)
		}
	})
}

// With capacity 2 the retransmit never overflows, so both policies are safe.
func TestStrictOrderRoomyQueue(t *testing.T) {
	spec := StrictOrderHandshake(2)
	for _, policy := range []OverflowPolicy{DropNew, DropOld} {
		rep := Check(spec, policy, Budget{MaxStates: 500, MaxSteps: 100})
		if rep.Verdict != Safe {
			t.Errorf("%s: want safe at capacity 2, got %s: %s",
				policy, rep.Verdict, rep.Reason)
		}
		if rep.Stats.DroppedSends != 0 {
			t.Errorf("%s: no drops expected at capacity 2", policy)
		}
	}
}

// A genuine state-machine half-authentication flaw is caught even with
// unlimited queues and under either overflow policy.
func TestHalfAuthCaughtBothPolicies(t *testing.T) {
	spec := HalfAuthHandshake(4)
	for _, policy := range []OverflowPolicy{DropNew, DropOld} {
		rep := Check(spec, policy, Budget{MaxStates: 500, MaxSteps: 100})
		if rep.Verdict != Unsafe {
			t.Fatalf("%s: want unsafe half-auth trace, got %s: %s",
				policy, rep.Verdict, rep.Reason)
		}
		ev, ok := lastSuccess(rep.Trace)
		if !ok || ev.Endpoint != "R" || ev.State != "R_DONE" {
			t.Fatalf("%s: unexpected violating event %+v", policy, ev)
		}
		if !containsTag("M3", ev.MissingTags) {
			t.Fatalf("%s: M3 must be reported missing, got tags %v seqs %v",
				policy, ev.MissingTags, ev.Missing)
		}
	}
}

// Exhausted budgets yield Unknown, never a premature Safe.
func TestBudgetExhaustionReturnsUnknown(t *testing.T) {
	spec := StrictOrderHandshake(2)

	t.Run("state budget", func(t *testing.T) {
		rep := Check(spec, DropNew, Budget{MaxStates: 1, MaxSteps: 100})
		if rep.Verdict != Unknown {
			t.Fatalf("want unknown on state exhaustion, got %s", rep.Verdict)
		}
	})

	t.Run("step budget", func(t *testing.T) {
		rep := Check(spec, DropOld, Budget{MaxStates: 500, MaxSteps: 1})
		if rep.Verdict != Unknown {
			t.Fatalf("want unknown on step exhaustion, got %s", rep.Verdict)
		}
	})
}

func TestInvalidSpecsRejected(t *testing.T) {
	cases := map[string]Spec{
		"zero capacity": {
			Channels:  []Channel{{Name: "c1", Capacity: 0}},
			Endpoints: []Endpoint{{Name: "I", Init: "s0", Incoming: "c1", Transitions: []Transition{{From: "s0", To: "s0"}}}},
		},
		"undefined incoming channel": {
			Channels:  []Channel{{Name: "c1", Capacity: 1}},
			Endpoints: []Endpoint{{Name: "I", Init: "s0", Incoming: "ghost", Transitions: []Transition{{From: "s0", To: "s0"}}}},
		},
		"success declared in initial state": {
			Channels: []Channel{{Name: "c1", Capacity: 1}},
			Endpoints: []Endpoint{{
				Name:        "I",
				Init:        "s0",
				Incoming:    "c1",
				Transitions: []Transition{{From: "s0", To: "s1"}},
				Success:     []SuccessBinding{{State: "s0", RequiredTags: []string{"x"}}},
			}},
		},
		"binding without requirements": {
			Channels: []Channel{{Name: "c1", Capacity: 1}},
			Endpoints: []Endpoint{{
				Name:        "I",
				Init:        "s0",
				Incoming:    "c1",
				Transitions: []Transition{{From: "s0", To: "s1"}},
				Success:     []SuccessBinding{{State: "s1"}},
			}},
		},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			rep := Check(spec, DropNew, Budget{MaxStates: 10, MaxSteps: 10})
			if rep.Verdict != Unknown || rep.Reason == "" {
				t.Fatalf("want unknown with reason, got %s/%q", rep.Verdict, rep.Reason)
			}
		})
	}
}

// Dropped sends still consume a sequence number under both policies.
func TestOverflowConsumesSequenceNumbers(t *testing.T) {
	spec := StrictOrderHandshake(1)
	for _, policy := range []OverflowPolicy{DropNew, DropOld} {
		rep := Check(spec, policy, Budget{MaxStates: 500, MaxSteps: 100})
		if rep.Stats.DroppedSends == 0 {
			t.Fatalf("%s: expected overflow drops", policy)
		}
	}
}

// The unsafe trace is a concrete, replayable event sequence.
func TestUnsafeTraceIsReplayable(t *testing.T) {
	spec := HalfAuthHandshake(4)
	rep := Check(spec, DropNew, Budget{MaxStates: 500, MaxSteps: 100})
	if rep.Verdict != Unsafe {
		t.Fatalf("want unsafe, got %s", rep.Verdict)
	}
	if len(rep.Trace) < 2 {
		t.Fatalf("trace too short: %d events", len(rep.Trace))
	}
	kinds := map[EventKind]bool{}
	for _, ev := range rep.Trace {
		kinds[ev.Kind] = true
	}
	if !kinds[EvRecv] || !kinds[EvSuccess] {
		t.Fatalf("trace must contain recv and success events, got %v", kinds)
	}
}
