package main

import "testing"

func TestEnumTwoNodeDeadlockFree(t *testing.T) {
	enum, err := NewEnumerator(twoNodeConfig(1))
	if err != nil {
		t.Fatal(err)
	}
	res := enum.Run()
	if res.InvariantError != nil {
		t.Fatal("invariant violation:", res.InvariantError)
	}
	if res.TerminalDead != 0 {
		t.Fatalf("unexpected deadlock terminals: %d", res.TerminalDead)
	}
	if res.TerminalOK == 0 {
		t.Fatal("no successful terminal state")
	}
	t.Logf("explored %d states", res.States)
}

func TestEnumTwoNodeDeadlock(t *testing.T) {
	enum, err := NewEnumerator(twoNodeConfig(2))
	if err != nil {
		t.Fatal(err)
	}
	res := enum.Run()
	if res.InvariantError != nil {
		t.Fatal("invariant violation:", res.InvariantError)
	}
	if res.TerminalDead == 0 {
		t.Fatal("expected at least one deadlock terminal")
	}
	if len(res.DeadWitness) == 0 {
		t.Fatal("no deadlock witness trace")
	}
	if len(res.DeadCycle) != 2 {
		t.Fatalf("wait-for cycle length = %d, want 2", len(res.DeadCycle))
	}
	// Witness trace must start at the initial state and end undelivered.
	last := res.DeadWitness[len(res.DeadWitness)-1]
	if enum.DeliveredList(last) != nil {
		t.Fatal("deadlock witness delivered packets unexpectedly")
	}
	t.Logf("explored %d states, %d deadlock terminals", res.States, res.TerminalDead)
}

func TestEnumRingDelivered(t *testing.T) {
	enum, err := NewEnumerator(ringConfig(3, 1))
	if err != nil {
		t.Fatal(err)
	}
	res := enum.Run()
	if res.InvariantError != nil {
		t.Fatal("invariant violation:", res.InvariantError)
	}
	if res.TerminalDead != 0 {
		t.Fatalf("ring(3,1) should be deadlock-free, got %d", res.TerminalDead)
	}
	t.Logf("explored %d states", res.States)
}

// The deterministic simulator's outcome must be consistent with the
// exhaustive exploration: sim deadlocks iff enum finds a deadlock
// terminal (for these small configs).
func TestSimAgreesWithEnum(t *testing.T) {
	for _, cfg := range []Config{twoNodeConfig(1), twoNodeConfig(2), ringConfig(3, 1), ringConfig(3, 2)} {
		enum, err := NewEnumerator(cfg)
		if err != nil {
			t.Fatal(err)
		}
		eres := enum.Run()
		if eres.InvariantError != nil {
			t.Fatal(eres.InvariantError)
		}
		sim, err := NewSim(cfg)
		if err != nil {
			t.Fatal(err)
		}
		sres := sim.Run(1000)
		enumDead := eres.TerminalDead > 0
		if enumDead != sres.Deadlock {
			t.Errorf("sim deadlock=%v but enum deadlock=%v (states=%d)",
				sres.Deadlock, enumDead, eres.States)
		}
	}
}
