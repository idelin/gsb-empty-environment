package netsec

import (
	"errors"
	"testing"
)

func TestOrderedMessages(t *testing.T) {
	msgs := OrderedMessages()
	if len(msgs) != 21 {
		t.Fatalf("expected 21 messages, got %d", len(msgs))
	}
	want := []Message{
		Begin(0), Begin(1), Begin(2), Begin(3),
		Proof(0, false), Proof(1, false), Proof(2, false), Proof(3, false),
		Proof(0, true), Proof(1, true), Proof(2, true), Proof(3, true),
		Abort(0), Abort(1), Abort(2), Abort(3),
		Write(0), Write(1), Write(2), Write(3),
		Reset(),
	}
	for i, m := range want {
		if msgs[i] != m {
			t.Fatalf("position %d: want %s, got %s", i, m, msgs[i])
		}
	}
}

func TestReferenceRules(t *testing.T) {
	init := InitialState()

	next, wrote := ReferenceStep(init, Begin(2), false)
	if next != (State{Phase: PhaseChallenge, Session: 2}) || wrote {
		t.Fatalf("Begin: got %s wrote=%t", next, wrote)
	}

	ch := State{Phase: PhaseChallenge, Session: 2}
	next, _ = ReferenceStep(ch, Proof(2, true), true)
	if next != (State{Phase: PhaseAuthenticated, Session: 2, AuthFlag: true}) {
		t.Fatalf("valid Proof: got %s", next)
	}
	next, _ = ReferenceStep(ch, Proof(2, false), false)
	if next != init {
		t.Fatalf("invalid Proof: got %s", next)
	}
	next, _ = ReferenceStep(ch, Proof(1, true), true)
	if next != ch {
		t.Fatalf("cross-session Proof: got %s", next)
	}

	auth := State{Phase: PhaseAuthenticated, Session: 2, AuthFlag: true}
	next, _ = ReferenceStep(auth, Proof(2, true), true)
	if next != auth {
		t.Fatalf("Proof while authenticated: got %s", next)
	}
	next, wrote = ReferenceStep(auth, Write(2), false)
	if next != auth || !wrote {
		t.Fatalf("matching Write: got %s wrote=%t", next, wrote)
	}
	_, wrote = ReferenceStep(auth, Write(1), false)
	if wrote {
		t.Fatal("mismatched Write must be denied")
	}
	_, wrote = ReferenceStep(ch, Write(2), false)
	if wrote {
		t.Fatal("Write in challenge must be denied")
	}

	next, _ = ReferenceStep(auth, Abort(2), false)
	if next != init {
		t.Fatalf("Abort current session: got %s", next)
	}
	next, _ = ReferenceStep(auth, Abort(1), false)
	if next != auth {
		t.Fatalf("Abort other session: got %s", next)
	}
	next, _ = ReferenceStep(auth, Reset(), false)
	if next != init {
		t.Fatalf("Reset: got %s", next)
	}
}

func TestCorrectDeviceHasNoVulnerability(t *testing.T) {
	res, err := Explore(CorrectDevice{}, nil)
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if res.Vulnerability != nil {
		t.Fatalf("unexpected vulnerability: %s", res.Vulnerability)
	}
	if !res.Complete {
		t.Fatal("search should complete within depth 12")
	}
}

func checkVulnerability(t *testing.T, res *Result, want []Message) {
	t.Helper()
	if res.Vulnerability == nil {
		t.Fatal("expected a vulnerability, got none")
	}
	got := res.Vulnerability.Messages()
	if len(got) != len(want) {
		t.Fatalf("sequence length: want %d, got %d (%s)", len(want), len(got), res.Vulnerability)
	}
	for i, m := range want {
		if got[i] != m {
			t.Fatalf("message %d: want %s, got %s", i, m, got[i])
		}
	}
	trace := res.Vulnerability.Trace
	last := trace[len(trace)-1]
	if last.Msg.Kind != KindWrite || !last.Wrote || last.RefWrote {
		t.Fatalf("final step must be a device-allowed, reference-denied Write: %+v", last)
	}
	for i, rec := range trace {
		if len(rec.BeforeSnapshot) == 0 || len(rec.AfterSnapshot) == 0 {
			t.Fatalf("step %d missing snapshots", i)
		}
		if i+1 < len(trace) && string(rec.AfterSnapshot) != string(trace[i+1].BeforeSnapshot) {
			t.Fatalf("snapshot chain broken between steps %d and %d", i, i+1)
		}
	}
}

func TestLeakyAuthFlagDevice(t *testing.T) {
	res, err := Explore(LeakyAuthFlagDevice{}, nil)
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	checkVulnerability(t, res, []Message{
		Begin(0), Proof(0, true), Begin(0), Write(0),
	})
}

func TestCrossSessionProofDevice(t *testing.T) {
	res, err := Explore(CrossSessionProofDevice{}, nil)
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	checkVulnerability(t, res, []Message{
		Begin(0), Proof(1, true), Write(0),
	})
}

func TestNondeterministicDeviceIsNotReported(t *testing.T) {
	calls := 0
	dev := DeviceFunc(func(st State, msg Message, ok bool) (State, bool) {
		calls++
		// Flip the Write verdict between the double-step probes.
		return st, msg.Kind == KindWrite && calls%2 == 0
	})
	res, err := Explore(dev, nil)
	if err == nil || !errors.Is(err, ErrNondeterministic) {
		t.Fatalf("expected ErrNondeterministic, got %v", err)
	}
	if res.Vulnerability != nil {
		t.Fatal("nondeterministic behavior must never be reported as a security finding")
	}
}

func TestBudgetExhaustionPreservesFrontier(t *testing.T) {
	res, err := Explore(LeakyAuthFlagDevice{}, &Options{Budget: 1})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if !res.BudgetExhausted {
		t.Fatal("expected budget exhaustion")
	}
	if len(res.Frontier) == 0 {
		t.Fatal("frontier must be preserved on budget exhaustion")
	}
	if res.Vulnerability != nil {
		t.Fatal("no vulnerability expected within budget 1")
	}

	resumed, err := Explore(LeakyAuthFlagDevice{}, &Options{Frontier: res.Frontier})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	checkVulnerability(t, resumed, []Message{
		Begin(0), Proof(0, true), Begin(0), Write(0),
	})
}

func TestInvalidDeviceStateIsRejected(t *testing.T) {
	dev := DeviceFunc(func(st State, msg Message, ok bool) (State, bool) {
		return State{Phase: Phase(9), Session: 7}, false
	})
	_, err := Explore(dev, nil)
	if err == nil || !errors.Is(err, ErrStateNotSerializable) {
		t.Fatalf("expected ErrStateNotSerializable, got %v", err)
	}
}
