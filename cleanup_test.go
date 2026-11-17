package netsec

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// Freeze is the first cleanup step: afterwards the host must refuse to
// fork for that generation, while other generations are unaffected.
func TestFreezeBlocksNewForks(t *testing.T) {
	tr := NewTracker()
	tok := Token{Tenant: "acme", Gen: 1}
	other := Token{Tenant: "acme", Gen: 2}

	if !tr.Admit(tok) {
		t.Fatal("Admit false before freeze")
	}
	tr.Freeze(tok)

	if tr.Admit(tok) {
		t.Error("Admit true after freeze")
	}
	if _, err := tr.Spawn(Instance{}, 400, tok); !errors.Is(err, ErrFrozen) {
		t.Errorf("Spawn after freeze: err=%v, want ErrFrozen", err)
	}
	if !tr.Admit(other) {
		t.Error("Admit false for unrelated generation")
	}
	if _, err := tr.Spawn(Instance{}, 401, other); err != nil {
		t.Errorf("Spawn for other generation: %v", err)
	}
}

// A process that ignores SIGTERM is escalated to SIGKILL, and cleanup
// returns only after the exit is confirmed by an Exit event.
func TestEscalatesAndWaitsForConfirmation(t *testing.T) {
	tr := NewTracker()
	tok := Token{Tenant: "acme", Gen: 1}
	stubborn := mustSpawn(t, tr, Instance{}, 500, tok)

	k := &fakeKiller{}
	k.onTerm = func(Instance) {} // ignores SIGTERM
	k.onKill = func(inst Instance) { tr.Exit(inst) }

	start := time.Now()
	runCleanup(t, tr, tok, k, 30*time.Millisecond)
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("cleanup returned after %v without waiting out the grace period", elapsed)
	}

	terms, kills := k.signaled()
	if !slices.Contains(terms, stubborn) {
		t.Errorf("no SIGTERM for %v", stubborn)
	}
	if !slices.Contains(kills, stubborn) {
		t.Errorf("no SIGKILL escalation for %v", stubborn)
	}
	if tr.Live(stubborn) {
		t.Error("stubborn process still live")
	}
}

// Terminate blocks until every member's exit is confirmed; signaling
// alone is not enough.
func TestTerminateBlocksUntilExitsConfirmed(t *testing.T) {
	tr := NewTracker()
	tok := Token{Tenant: "acme", Gen: 1}
	slow := mustSpawn(t, tr, Instance{}, 600, tok)

	k := &fakeKiller{}
	k.onKill = func(inst Instance) {
		// Confirm the exit asynchronously, after the signal returns.
		time.AfterFunc(20*time.Millisecond, func() { tr.Exit(inst) })
	}

	done := make(chan error, 1)
	go func() { done <- tr.Terminate(context.Background(), tok, k, 10*time.Millisecond) }()

	select {
	case err := <-done:
		t.Fatalf("Terminate returned early (%v) before exit confirmation", err)
	case <-time.After(15 * time.Millisecond):
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Terminate: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Terminate never observed the confirmed exit")
	}
	if tr.Live(slow) {
		t.Error("process still live")
	}
}

// A fork that races the freeze is refused; anything already recorded
// is caught by the cleanup loop's re-scan.
func TestForkRacingCleanupIsRefused(t *testing.T) {
	tr := NewTracker()
	tok := Token{Tenant: "acme", Gen: 1}
	mustSpawn(t, tr, Instance{}, 700, tok)

	k := &fakeKiller{}
	k.onTerm = func(inst Instance) {
		// The doomed process tries to fork during its shutdown.
		if _, err := tr.Spawn(inst, 701, tok); !errors.Is(err, ErrFrozen) {
			t.Errorf("racing fork: err=%v, want ErrFrozen", err)
		}
		tr.Exit(inst)
	}
	runCleanup(t, tr, tok, k, 20*time.Millisecond)

	if got := tr.Snapshot(tok); len(got) != 0 {
		t.Errorf("reachable set not empty after cleanup: %v", got)
	}
}
