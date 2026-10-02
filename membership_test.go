package gracefulleave

import (
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestReadyExcludesLeaving(t *testing.T) {
	ms := NewMembership(time.Hour)
	ms.Add(NewMember("a", "addr-a"))
	ms.Add(NewMember("b", "addr-b"))
	ms.Add(NewMember("c", "addr-c"))

	if got := len(ms.Ready()); got != 3 {
		t.Fatalf("ready pool = %d, want 3", got)
	}
	if !ms.MarkLeaving("b") {
		t.Fatal("MarkLeaving(b) = false, want true")
	}

	ready := ms.Ready()
	if len(ready) != 2 {
		t.Fatalf("ready pool = %d, want 2", len(ready))
	}
	for _, m := range ready {
		if m.ID == "b" {
			t.Fatal("leaving member b still in ready pool")
		}
	}
	m, ok := ms.Get("b")
	if !ok {
		t.Fatal("entry b missing during quiescent period")
	}
	if m.State() != StateLeaving {
		t.Fatalf("b state = %v, want Leaving", m.State())
	}
	if m.LeavingSince().IsZero() {
		t.Fatal("LeavingSince not recorded")
	}
}

func TestPhysicalRemovalAfterQuiescent(t *testing.T) {
	// Deterministic: inject a manual timer instead of time.AfterFunc.
	fire := make(chan func(), 1)
	ms := NewMembership(time.Minute)
	ms.afterFunc = func(_ time.Duration, f func()) *time.Timer {
		fire <- f
		return nil
	}
	removed := make(chan string, 1)
	ms.SetOnRemove(func(id string) { removed <- id })

	ms.Add(NewMember("x", "addr-x"))
	if !ms.MarkLeaving("x") {
		t.Fatal("MarkLeaving(x) = false, want true")
	}
	if ms.Len() != 1 {
		t.Fatal("entry removed before quiescent timer fired")
	}

	(<-fire)() // quiescent timer expires

	select {
	case id := <-removed:
		if id != "x" {
			t.Fatalf("removed id = %q, want x", id)
		}
	default:
		t.Fatal("onRemove hook not called")
	}
	if _, ok := ms.Get("x"); ok {
		t.Fatal("entry x still present after quiescent expiry")
	}
	if ms.Len() != 0 {
		t.Fatalf("table len = %d, want 0", ms.Len())
	}
}

func TestMarkLeavingIdempotentAndUnknown(t *testing.T) {
	ms := NewMembership(time.Hour)
	ms.Add(NewMember("a", "addr-a"))

	if ms.MarkLeaving("ghost") {
		t.Fatal("MarkLeaving on unknown member = true, want false")
	}
	if !ms.MarkLeaving("a") {
		t.Fatal("first MarkLeaving(a) = false, want true")
	}
	if ms.MarkLeaving("a") {
		t.Fatal("second MarkLeaving(a) = true, want false (idempotent)")
	}
}
