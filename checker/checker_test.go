package checker

import "testing"

func crossing() [2]Machine {
	return [2]Machine{
		{Name: "A", Trans: map[int][]Transition{
			0: {{Kind: Send, Msg: "a1", To: 1}},
			1: {{Kind: Recv, Msg: "b", To: 2}},
		}, Finals: map[int]bool{2: true}},
		{Name: "B", Trans: map[int][]Transition{
			0: {{Kind: Send, Msg: "b1", To: 1}},
			1: {{Kind: Recv, Msg: "a", To: 2}},
		}, Finals: map[int]bool{2: true}},
	}
}

// pingPong is a correct request/response protocol: no deadlock reachable.
func pingPong() [2]Machine {
	return [2]Machine{
		{Name: "A", Trans: map[int][]Transition{
			0: {{Kind: Send, Msg: "req", To: 1}},
			1: {{Kind: Recv, Msg: "ack", To: 2}},
		}, Finals: map[int]bool{2: true}},
		{Name: "B", Trans: map[int][]Transition{
			0: {{Kind: Recv, Msg: "req", To: 1}},
			1: {{Kind: Send, Msg: "ack", To: 2}},
		}, Finals: map[int]bool{2: true}},
	}
}

func TestFindsShortestDeadlock(t *testing.T) {
	m := crossing()
	steps, dl, found := FindDeadlock(m)
	if !found {
		t.Fatal("expected a reachable deadlock")
	}
	if len(steps) != 2 {
		t.Fatalf("expected shortest trace of 2 steps, got %d: %v", len(steps), steps)
	}
	if !Deadlock(dl, m) {
		t.Fatalf("reported state %s is not a deadlock", dl)
	}
	if _, err := CheckDeadlockTrace(m, steps); err != nil {
		t.Fatalf("replay of found trace failed: %v", err)
	}
}

func TestNoDeadlockInCorrectProtocol(t *testing.T) {
	if steps, s, found := FindDeadlock(pingPong()); found {
		t.Fatalf("unexpected deadlock: %v -> %s", steps, s)
	}
}

func TestInternalActionsAndCapacity(t *testing.T) {
	// A loops on an internal action forever; B sends once then is done but
	// never final -> with capacity 1 the queue fills and B blocks, but A's
	// internal loop means no deadlock exists (livelock is not deadlock).
	m := [2]Machine{
		{Name: "A", Trans: map[int][]Transition{
			0: {{Kind: Internal, To: 0}},
		}, Finals: map[int]bool{}},
		{Name: "B", Trans: map[int][]Transition{
			0: {{Kind: Send, Msg: "x", To: 1}},
		}, Finals: map[int]bool{1: true}},
	}
	if _, _, found := FindDeadlock(m); found {
		t.Fatal("internal action must count as enabled; no deadlock expected")
	}
}

func TestReplayRejectsIllegalSteps(t *testing.T) {
	m := crossing()
	cases := []struct {
		name  string
		steps []Step
	}{
		{"recv from empty queue", []Step{{Machine: 0, Trans: Transition{Kind: Recv, Msg: "b", To: 2}}}},
		{"transition not from current state", []Step{
			{Machine: 0, Trans: Transition{Kind: Send, Msg: "a1", To: 1}},
			{Machine: 0, Trans: Transition{Kind: Send, Msg: "a1", To: 1}},
		}},
		{"nonexistent edge", []Step{{Machine: 1, Trans: Transition{Kind: Send, Msg: "zzz", To: 9}}}},
		{"invalid machine id", []Step{{Machine: 7, Trans: Transition{Kind: Internal, To: 0}}}},
	}
	for _, c := range cases {
		if _, err := VerifyTrace(m, c.steps); err == nil {
			t.Errorf("%s: expected replay error, got nil", c.name)
		}
	}
}

func TestReplayRejectsSendToFullQueue(t *testing.T) {
	m := [2]Machine{
		{Name: "A", Trans: map[int][]Transition{
			0: {{Kind: Send, Msg: "x", To: 1}},
			1: {{Kind: Send, Msg: "y", To: 2}},
		}, Finals: map[int]bool{2: true}},
		{Name: "B", Trans: map[int][]Transition{}, Finals: map[int]bool{0: true}},
	}
	steps := []Step{
		{Machine: 0, Trans: Transition{Kind: Send, Msg: "x", To: 1}},
		{Machine: 0, Trans: Transition{Kind: Send, Msg: "y", To: 2}}, // queue full
	}
	if _, err := VerifyTrace(m, steps); err == nil {
		t.Fatal("expected capacity violation")
	}
}

func TestCheckDeadlockTraceRejectsNonDeadlockEnd(t *testing.T) {
	m := crossing()
	// A single send leaves B's send enabled: legal trace, but not a deadlock.
	steps := []Step{{Machine: 0, Trans: Transition{Kind: Send, Msg: "a1", To: 1}}}
	if _, err := CheckDeadlockTrace(m, steps); err == nil {
		t.Fatal("expected non-deadlock end state to be rejected")
	}
}
