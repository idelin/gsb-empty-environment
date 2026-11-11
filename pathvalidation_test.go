package netsec

import (
	"errors"
	"testing"
	"time"
)

// TestAddressRoundTrip verifies the happy path: a probe leaves on the
// candidate's own addresses and a response echoing its nonce on that exact
// local/peer pair validates and activates the path.
func TestAddressRoundTrip(t *testing.T) {
	h := newHarness(t, 7, 100*time.Millisecond)
	v := h.v

	p := pathFor(t, 7, "10.0.0.1:4000", "192.0.2.1:443")
	deadline := h.base.Add(1 * time.Second)
	if err := v.AddCandidate(p, deadline); err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}

	// Exactly one probe, sent from the candidate's local socket to its peer.
	probes := h.rec.snapshot()
	if len(probes) != 1 {
		t.Fatalf("probes = %d, want 1", len(probes))
	}
	if probes[0].path != p {
		t.Fatalf("probe path = %+v, want %+v", probes[0].path, p)
	}
	if probes[0].path.Local != p.Local || probes[0].path.Peer != p.Peer {
		t.Fatalf("probe addressed to %v -> %v, want %v -> %v",
			probes[0].path.Local, probes[0].path.Peer, p.Local, p.Peer)
	}
	nonce := probes[0].nonce

	// Before the round trip completes, sending app data is restricted.
	assertErrIs(t, v.CheckSend(p), ErrPathNotValidated)

	// The response comes back from the peer to the local socket that sent it.
	if err := v.HandleResponse(7, p.Local, p.Peer, nonce); err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}
	if !v.IsValidated(p) {
		t.Fatal("path should be validated")
	}
	assertActive(t, v, p)
	if err := v.CheckSend(p); err != nil {
		t.Fatalf("CheckSend after validation: %v", err)
	}

	// A second, retransmitted response carrying a now-cleared nonce is rejected.
	assertErrIs(t, v.HandleResponse(7, p.Local, p.Peer, nonce), ErrNonceMismatch)

	kinds := h.events.kinds()
	wantKinds := []EventKind{EventValidated, EventActivated}
	if len(kinds) != len(wantKinds) {
		t.Fatalf("events = %v, want %v", kinds, wantKinds)
	}
	for i := range wantKinds {
		if kinds[i] != wantKinds[i] {
			t.Fatalf("events = %v, want %v", kinds, wantKinds)
		}
	}

	// The retry timer must be stopped once validation succeeds.
	h.clock.Advance(5 * time.Second)
	if h.rec.count() != 1 {
		t.Fatalf("probes after validation = %d, want no retries", h.rec.count())
	}
}

// TestResponseFromOtherPathRejected ensures a response arriving on a different
// local/peer pair can never validate the candidate address, even with a valid
// nonce structure.
func TestResponseFromOtherPathRejected(t *testing.T) {
	h := newHarness(t, 1, time.Second)
	v := h.v

	candidate := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")
	if err := v.AddCandidate(candidate, h.base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}
	nonce := h.rec.snapshot()[0].nonce

	// Datagram physically arrives on a second local socket / different pair.
	otherLocal := mustAddr(t, "10.0.0.2:4000")
	otherPeer := mustAddr(t, "192.0.2.1:443")
	assertErrIs(t, v.HandleResponse(1, otherLocal, otherPeer, nonce), ErrUnknownPath)

	// Same nonce but a spoofed peer source address.
	spoofedPeer := mustAddr(t, "198.51.100.9:443")
	assertErrIs(t, v.HandleResponse(1, candidate.Local, spoofedPeer, nonce), ErrUnknownPath)

	// Registered candidate remains unvalidated and restricted.
	assertErrIs(t, v.CheckSend(candidate), ErrPathNotValidated)
	assertNoActive(t, v)
}

// TestOldConnectionIDReuse proves validation material from a previous
// connection instance cannot authenticate a new instance after migration.
func TestOldConnectionIDReuse(t *testing.T) {
	h := newHarness(t, 11, time.Second)
	v := h.v

	oldPath := pathFor(t, 11, "10.0.0.1:4000", "192.0.2.1:443")
	if err := v.AddCandidate(oldPath, h.base.Add(5*time.Second)); err != nil {
		t.Fatalf("AddCandidate old: %v", err)
	}
	oldNonce := h.rec.snapshot()[0].nonce

	// Network handoff mints a new connection instance.
	v.Migrate(12)
	if got := v.Conn(); got != 12 {
		t.Fatalf("Conn = %d, want 12", got)
	}
	if v.IsValidated(oldPath) {
		t.Fatal("old candidate must be dropped on migration")
	}
	assertNoActive(t, v)

	// Replaying the old-instance response (even with the correct old nonce and
	// addresses) is rejected because the connection ID is stale.
	assertErrIs(t, v.HandleResponse(11, oldPath.Local, oldPath.Peer, oldNonce),
		ErrStaleConnection)

	// Re-registering an old path under the new instance is also rejected.
	assertErrIs(t, v.AddCandidate(oldPath, h.base.Add(6*time.Second)),
		ErrStaleConnection)

	// A freshly registered candidate under conn 12 starts from scratch.
	newPath := pathFor(t, 12, "10.0.0.5:4000", "192.0.2.1:443")
	if err := v.AddCandidate(newPath, h.base.Add(6*time.Second)); err != nil {
		t.Fatalf("AddCandidate new: %v", err)
	}
	probe, ok := h.rec.last()
	if !ok || probe.nonce == oldNonce {
		t.Fatal("new instance must issue a fresh challenge nonce")
	}
	if err := v.HandleResponse(12, newPath.Local, newPath.Peer, probe.nonce); err != nil {
		t.Fatalf("HandleResponse new: %v", err)
	}
	assertActive(t, v, newPath)

	// Migration emitted a failure for the dropped old candidate; the new path
	// then emitted validated+activated.
	var sawMigratedFailure bool
	for _, ev := range h.events.all() {
		if ev.Kind == EventFailed && ev.Reason == ReasonMigrated {
			sawMigratedFailure = true
		}
	}
	if !sawMigratedFailure {
		t.Fatalf("missing migration events: %+v", h.events.all())
	}
}

// TestRetryDoesNotExtendDeadline verifies challenge retries use the fixed
// overall deadline: probes keep going until the deadline and then stop with a
// deadline failure, regardless of retry count.
func TestRetryDoesNotExtendDeadline(t *testing.T) {
	interval := 200 * time.Millisecond
	h := newHarness(t, 3, interval)
	v := h.v

	p := pathFor(t, 3, "10.0.0.1:4000", "192.0.2.1:443")
	deadline := h.base.Add(1 * time.Second)
	if err := v.AddCandidate(p, deadline); err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}

	// Drive almost the whole window: retries at 200ms, 400ms, 600ms, 800ms.
	h.clock.Advance(999 * time.Millisecond)
	snaps := v.Snapshots()
	if len(snaps) != 1 || snaps[0].Deadline != deadline {
		t.Fatalf("deadline changed: %+v", snaps)
	}
	probesBefore := h.rec.count()
	if probesBefore < 2 {
		t.Fatalf("expected retries, got %d probes", probesBefore)
	}

	// Crossing the deadline fires the clamped timer and fails the candidate.
	h.clock.Advance(2 * time.Millisecond)
	if _, ok := v.Active(); ok {
		t.Fatal("deadline must not produce an active path")
	}
	if err := v.CheckSend(p); !errors.Is(err, ErrUnknownPath) {
		t.Fatalf("CheckSend after deadline = %v, want ErrUnknownPath", err)
	}
	probesAtDeadline := h.rec.count()

	// No further probes after the deadline; the timer is gone.
	h.clock.Advance(5 * time.Second)
	if h.rec.count() != probesAtDeadline {
		t.Fatalf("probes grew after deadline: %d -> %d",
			probesAtDeadline, h.rec.count())
	}

	var failed *Event
	for i := range h.events.all() {
		ev := h.events.all()[i]
		if ev.Kind == EventFailed {
			failed = &ev
		}
	}
	if failed == nil || failed.Reason != ReasonDeadline {
		t.Fatalf("want deadline failure event, got %+v", h.events.all())
	}

	// Sanity: all probes still used the candidate's own addresses.
	for _, pr := range h.rec.snapshot() {
		if pr.path != p {
			t.Fatalf("retry probe left on wrong path %+v", pr.path)
		}
	}
}

// TestCancel verifies explicit cancellation stops retries and frees the active
// slot, allowing another validated candidate to take over.
func TestCancel(t *testing.T) {
	h := newHarness(t, 5, 100*time.Millisecond)
	v := h.v

	p1 := pathFor(t, 5, "10.0.0.1:4000", "192.0.2.1:443")
	if err := v.AddCandidate(p1, h.base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate p1: %v", err)
	}
	pr1 := h.rec.snapshot()[0]
	if err := v.HandleResponse(5, p1.Local, p1.Peer, pr1.nonce); err != nil {
		t.Fatalf("validate p1: %v", err)
	}
	assertActive(t, v, p1)

	p2 := pathFor(t, 5, "10.0.0.2:4000", "192.0.2.1:443")
	if err := v.AddCandidate(p2, h.base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate p2: %v", err)
	}

	// Canceling an unvalidated candidate is fine and leaves the active path.
	if err := v.Cancel(p2); err != nil {
		t.Fatalf("Cancel p2: %v", err)
	}
	assertActive(t, v, p1)

	// Canceling the active candidate deactivates it (no validated replacement).
	if err := v.Cancel(p1); err != nil {
		t.Fatalf("Cancel p1: %v", err)
	}
	assertNoActive(t, v)
	assertErrIs(t, v.CheckSend(p1), ErrUnknownPath)

	// Re-canceling is an error, and long after cancellation nothing retries.
	assertErrIs(t, v.Cancel(p1), ErrUnknownPath)
	count := h.rec.count()
	h.clock.Advance(5 * time.Second)
	if h.rec.count() != count {
		t.Fatal("canceled candidate sent further probes")
	}

	var sawCanceled bool
	for _, ev := range h.events.all() {
		if ev.Kind == EventFailed && ev.Reason == ReasonCanceled {
			sawCanceled = true
		}
	}
	if !sawCanceled {
		t.Fatalf("missing canceled failure event: %+v", h.events.all())
	}
}

// TestCloseCancelsEverything verifies Close stops all validation and makes
// subsequent operations fail with ErrClosed.
func TestCloseCancelsEverything(t *testing.T) {
	h := newHarness(t, 6, 100*time.Millisecond)
	v := h.v
	p := pathFor(t, 6, "10.0.0.1:4000", "192.0.2.1:443")
	if err := v.AddCandidate(p, h.base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}
	v.Close()

	assertErrIs(t, v.AddCandidate(p, h.base.Add(3*time.Second)), ErrClosed)
	assertErrIs(t, v.HandleResponse(6, p.Local, p.Peer, Nonce{}), ErrClosed)
	assertErrIs(t, v.CheckSend(p), ErrClosed)

	count := h.rec.count()
	h.clock.Advance(5 * time.Second)
	if h.rec.count() != count {
		t.Fatal("closed validator sent further probes")
	}
}
