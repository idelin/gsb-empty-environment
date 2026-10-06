package checker

import "testing"

func mismatchSystem() *System {
	a := &Machine{
		Name:    "A",
		Initial: "a0",
		Transitions: []Transition{
			{From: "a0", To: "a1", Act: Action{Kind: Tau}},
			{From: "a1", To: "a2", Act: Action{Kind: Send, Message: "data"}},
			{From: "a2", To: "a3", Act: Action{Kind: Recv, Message: "ack"}},
		},
	}
	b := &Machine{
		Name:    "B",
		Initial: "b0",
		Transitions: []Transition{
			{From: "b0", To: "b1", Act: Action{Kind: Recv, Message: "req"}},
			{From: "b1", To: "b2", Act: Action{Kind: Send, Message: "ack"}},
		},
	}
	return &System{A: a, B: b}
}

func pingPongSystem() *System {
	a := &Machine{
		Name:    "A",
		Initial: "a0",
		Transitions: []Transition{
			{From: "a0", To: "a1", Act: Action{Kind: Send, Message: "ping"}},
			{From: "a1", To: "a0", Act: Action{Kind: Recv, Message: "pong"}},
		},
	}
	b := &Machine{
		Name:    "B",
		Initial: "b0",
		Transitions: []Transition{
			{From: "b0", To: "b1", Act: Action{Kind: Recv, Message: "ping"}},
			{From: "b1", To: "b0", Act: Action{Kind: Send, Message: "pong"}},
		},
	}
	return &System{A: a, B: b}
}

func TestFindsShortestDeadlockTrace(t *testing.T) {
	sys := mismatchSystem()
	res := sys.Explore()
	if !res.Deadlock {
		t.Fatal("expected a deadlock")
	}
	// Shortest trace: A tau, then A send(data); B is stuck on recv(req).
	if len(res.Trace) != 2 {
		t.Fatalf("expected shortest trace of length 2, got %d", len(res.Trace))
	}
	if res.Trace[0].Transition.Act.Kind != Tau || res.Trace[1].Transition.Act.Kind != Send {
		t.Fatalf("unexpected trace: %v", res.Trace)
	}
	dead := res.DeadState
	if dead.A != "a2" || dead.B != "b0" {
		t.Fatalf("unexpected machine states in deadlock: %s", dead)
	}
	if len(dead.QAB) != 1 || dead.QAB[0] != "data" {
		t.Fatalf("expected A->B queue [data], got %v", dead.QAB)
	}
	if len(dead.QBA) != 0 {
		t.Fatalf("expected empty B->A queue, got %v", dead.QBA)
	}
}

func TestNoDeadlockInPingPong(t *testing.T) {
	res := pingPongSystem().Explore()
	if res.Deadlock {
		t.Fatalf("ping-pong must be deadlock-free, got trace %v", res.Trace)
	}
	if res.Explored == 0 {
		t.Fatal("expected a non-empty state space")
	}
}

func TestTerminatedIsNotDeadlock(t *testing.T) {
	// A sends, B receives, both reach terminal states: clean termination.
	a := &Machine{
		Name:    "A",
		Initial: "a0",
		Transitions: []Transition{
			{From: "a0", To: "done", Act: Action{Kind: Send, Message: "m"}},
		},
	}
	b := &Machine{
		Name:    "B",
		Initial: "b0",
		Transitions: []Transition{
			{From: "b0", To: "done", Act: Action{Kind: Recv, Message: "m"}},
		},
	}
	res := (&System{A: a, B: b}).Explore()
	if res.Deadlock {
		t.Fatalf("clean termination must not be reported as deadlock: %v", res.Trace)
	}
}

func TestReplayAcceptsFoundTrace(t *testing.T) {
	sys := mismatchSystem()
	res := sys.Explore()
	if !res.Deadlock {
		t.Fatal("expected a deadlock")
	}
	if err := sys.VerifyDeadlockTrace(res.Trace); err != nil {
		t.Fatalf("replay rejected a valid trace: %v", err)
	}
}

func TestReplayRejectsIllegalRecv(t *testing.T) {
	sys := mismatchSystem()
	// B tries to recv(req) from an empty queue at step 0.
	bad := []Step{{
		Who:        1,
		Transition: Transition{From: "b0", To: "b1", Act: Action{Kind: Recv, Message: "req"}},
		After:      State{A: "a0", B: "b1"},
	}}
	if err := sys.VerifyDeadlockTrace(bad); err == nil {
		t.Fatal("expected replay to reject recv from empty queue")
	}
}

func TestReplayRejectsWrongHeadMessage(t *testing.T) {
	sys := mismatchSystem()
	res := sys.Explore()
	if !res.Deadlock {
		t.Fatal("expected a deadlock")
	}
	// After A sends "data", fabricate B consuming it as if it were "req".
	tampered := append([]Step(nil), res.Trace...)
	tampered = append(tampered, Step{
		Who:        1,
		Transition: Transition{From: "b0", To: "b1", Act: Action{Kind: Recv, Message: "req"}},
		After:      State{A: "a2", B: "b1"},
	})
	if err := sys.VerifyDeadlockTrace(tampered); err == nil {
		t.Fatal("expected replay to reject recv of mismatched head message")
	}
}

func TestReplayRejectsForgedPostState(t *testing.T) {
	sys := mismatchSystem()
	res := sys.Explore()
	if !res.Deadlock {
		t.Fatal("expected a deadlock")
	}
	tampered := append([]Step(nil), res.Trace...)
	// Corrupt the recorded queue contents of the final state.
	tampered[len(tampered)-1].After.QAB = nil
	if err := sys.VerifyDeadlockTrace(tampered); err == nil {
		t.Fatal("expected replay to reject forged post-state queue contents")
	}
}

func TestReplayRejectsSendToFullQueue(t *testing.T) {
	// A can send twice in a row; with capacity 1 the second send is illegal
	// until B consumes. Replay must catch a trace that fires it anyway.
	a := &Machine{
		Name:    "A",
		Initial: "a0",
		Transitions: []Transition{
			{From: "a0", To: "a1", Act: Action{Kind: Send, Message: "m1"}},
			{From: "a1", To: "a2", Act: Action{Kind: Send, Message: "m2"}},
		},
	}
	b := &Machine{
		Name:    "B",
		Initial: "b0",
		Transitions: []Transition{
			{From: "b0", To: "b1", Act: Action{Kind: Recv, Message: "m1"}},
		},
	}
	sys := &System{A: a, B: b}
	bad := []Step{
		{Who: 0, Transition: a.Transitions[0], After: State{A: "a1", B: "b0", QAB: []string{"m1"}}},
		{Who: 0, Transition: a.Transitions[1], After: State{A: "a2", B: "b0", QAB: []string{"m1", "m2"}}},
	}
	if _, err := sys.Replay(bad); err == nil {
		t.Fatal("expected replay to reject send into a full queue")
	}
}
