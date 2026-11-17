package netsec

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeKiller is the event-model signal sink. The test scripts how the
// "OS" reacts to each signal via onTerm/onKill, typically by feeding
// Exit events back into the tracker.
type fakeKiller struct {
	mu     sync.Mutex
	terms  []Instance
	kills  []Instance
	onTerm func(Instance)
	onKill func(Instance)
}

func (f *fakeKiller) Terminate(inst Instance) error {
	f.mu.Lock()
	f.terms = append(f.terms, inst)
	cb := f.onTerm
	f.mu.Unlock()
	if cb != nil {
		cb(inst)
	}
	return nil
}

func (f *fakeKiller) Kill(inst Instance) error {
	f.mu.Lock()
	f.kills = append(f.kills, inst)
	cb := f.onKill
	f.mu.Unlock()
	if cb != nil {
		cb(inst)
	}
	return nil
}

func (f *fakeKiller) signaled() (terms, kills []Instance) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.terms), slices.Clone(f.kills)
}

func mustSpawn(t *testing.T, tr *Tracker, parent Instance, pid int, tok Token) Instance {
	t.Helper()
	inst, err := tr.Spawn(parent, pid, tok)
	if err != nil {
		t.Fatalf("Spawn(pid=%d, tok=%v): %v", pid, tok, err)
	}
	return inst
}

// runCleanup drives Terminate in the background and fails the test if
// it does not finish (with confirmed exits) within the deadline.
func runCleanup(t *testing.T, tr *Tracker, tok Token, k Killer, grace time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- tr.Terminate(context.Background(), tok, k, grace) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Terminate(%v): %v", tok, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Terminate(%v) did not complete after confirmed exits", tok)
	}
}

// Parent exits first; the child is reparented by the OS and changes
// its process group. Cleanup must still find and kill it, because
// ownership follows the immutable token, not the live process tree.
func TestParentExitsFirstChildReparented(t *testing.T) {
	tr := NewTracker()
	tok := Token{Tenant: "acme", Gen: 1}

	parent := mustSpawn(t, tr, Instance{}, 100, tok)
	child := mustSpawn(t, tr, parent, 101, tok)
	grandchild := mustSpawn(t, tr, child, 102, tok)

	tr.Exit(parent)                                // parent dies; OS reparents child to init
	if err := tr.SetPgid(child, 777); err != nil { // child joins another group
		t.Fatal(err)
	}
	if err := tr.Setsid(grandchild); err != nil { // grandchild starts a session
		t.Fatal(err)
	}

	k := &fakeKiller{}
	k.onKill = func(inst Instance) { tr.Exit(inst) } // SIGKILL always reaps
	runCleanup(t, tr, tok, k, 20*time.Millisecond)

	terms, kills := k.signaled()
	for _, want := range []Instance{child, grandchild} {
		if !slices.Contains(terms, want) {
			t.Errorf("missing SIGTERM for %v; terms=%v", want, terms)
		}
		if !slices.Contains(kills, want) {
			t.Errorf("missing SIGKILL for %v; kills=%v", want, kills)
		}
	}
	if slices.Contains(terms, parent) || slices.Contains(kills, parent) {
		t.Errorf("dead parent %v was signaled", parent)
	}
	if tr.Live(child) || tr.Live(grandchild) {
		t.Error("descendants still live after cleanup")
	}
}

// Group and session changes never move a process out of its tenant's
// reachable set.
func TestGroupSessionChangesDoNotEscapeOwnership(t *testing.T) {
	tr := NewTracker()
	tok := Token{Tenant: "acme", Gen: 1}

	root := mustSpawn(t, tr, Instance{}, 200, tok)
	a := mustSpawn(t, tr, root, 201, tok)
	b := mustSpawn(t, tr, a, 202, tok)

	if err := tr.Setsid(a); err != nil {
		t.Fatal(err)
	}
	if err := tr.SetPgid(b, 999); err != nil {
		t.Fatal(err)
	}

	got := tr.Snapshot(tok)
	for _, want := range []Instance{root, a, b} {
		if !slices.Contains(got, want) {
			t.Errorf("snapshot missing %v after regrouping; got %v", want, got)
		}
	}
	if owner, _ := tr.OwnerOf(b); owner != tok {
		t.Errorf("OwnerOf(%v) = %v, want %v", b, owner, tok)
	}
}

// A reused PID belongs to the new tenant generation. Cleaning up the
// old generation must not signal the new incarnation, and a stale exit
// event for the old incarnation must not reap it.
func TestPIDReuseDoesNotKillNewTenant(t *testing.T) {
	tr := NewTracker()
	oldTok := Token{Tenant: "acme", Gen: 1}
	newTok := Token{Tenant: "acme", Gen: 2}

	old := mustSpawn(t, tr, Instance{}, 300, oldTok)
	tr.Exit(old) // PID 300 is freed

	reused := mustSpawn(t, tr, Instance{}, 300, newTok) // OS reuses the PID
	if reused == old {
		t.Fatalf("incarnation not distinguished: %v", reused)
	}

	tr.Exit(old) // stale exit event for the old incarnation
	if !tr.Live(reused) {
		t.Fatal("stale exit reaped the new tenant's process")
	}

	k := &fakeKiller{}
	runCleanup(t, tr, oldTok, k, 20*time.Millisecond)

	terms, kills := k.signaled()
	if len(terms) != 0 || len(kills) != 0 {
		t.Errorf("old-generation cleanup signaled %v/%v with nothing to kill", terms, kills)
	}
	if !tr.Live(reused) {
		t.Fatal("new tenant's process was killed by old-generation cleanup")
	}
	if owner, ok := tr.OwnerOf(reused); !ok || owner != newTok {
		t.Errorf("OwnerOf(%v) = %v, %v; want %v", reused, owner, ok, newTok)
	}
}
