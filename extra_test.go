package netsec

import (
	"errors"
	"testing"
	"time"
)

// denyStrategy never selects a path, leaving the host without an active path
// even after successful validation.
type denyStrategy struct{}

func (denyStrategy) Select([]Path) (Path, bool) { return Path{}, false }

// TestConfigValidationAndDefaults covers constructor defaults and error paths.
func TestConfigValidationAndDefaults(t *testing.T) {
	if _, err := NewValidator(Config{}); !errors.Is(err, errSendRequired) {
		t.Fatalf("NewValidator without Send = %v, want errSendRequired", err)
	}

	v, err := NewValidator(Config{
		Conn: 1,
		Send: func(Path, Nonce) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewValidator defaults: %v", err)
	}
	if v.interval != DefaultRetryInterval {
		t.Fatalf("default interval = %v, want %v", v.interval, DefaultRetryInterval)
	}
	if _, ok := v.clock.(wallClock); !ok {
		t.Fatalf("default clock type = %T, want wallClock", v.clock)
	}
	if _, ok := v.strategy.(latestStrategy); !ok {
		t.Fatalf("default strategy = %T, want latestStrategy", v.strategy)
	}

	// randomNonce must produce 8 bytes and be usable through the default source.
	n1, err := randomNonce()
	if err != nil {
		t.Fatalf("randomNonce: %v", err)
	}
	n2, _ := randomNonce()
	if n1 == n2 {
		t.Fatal("two random nonces collided")
	}

	// Wall clock wiring smoke test.
	wc := wallClock{}
	before := time.Now()
	tm := wc.AfterFunc(time.Minute, func() {})
	now := wc.Now()
	if now.Before(before) || now.After(time.Now()) {
		t.Fatalf("wallClock.Now() = %v not within expected window", now)
	}
	if !tm.Stop() {
		t.Fatal("fresh wall timer should stop")
	}
	tm.Reset(time.Minute)
	tm.Stop()
}

// TestAddCandidateValidation exercises all registration rejection branches.
func TestAddCandidateValidation(t *testing.T) {
	h := newHarness(t, 1, time.Second)
	v := h.v
	p := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")

	// Deadline in the past.
	assertErrIs(t, v.AddCandidate(p, h.base.Add(-time.Second)), ErrInvalidDeadline)

	// Wrong connection instance.
	stale := pathFor(t, 2, "10.0.0.1:4000", "192.0.2.1:443")
	assertErrIs(t, v.AddCandidate(stale, h.base.Add(time.Second)), ErrStaleConnection)

	// Valid registration.
	if err := v.AddCandidate(p, h.base.Add(time.Second)); err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}
	// Duplicate.
	assertErrIs(t, v.AddCandidate(p, h.base.Add(2*time.Second)), ErrDuplicateCandidate)

	// After close, registration and responses are refused.
	v.Close()
	assertErrIs(t, v.AddCandidate(p, h.base.Add(3*time.Second)), ErrClosed)
}

// TestStrategyDeniesActive verifies a validated path stays inactive and
// unsendable when the strategy returns no selection, and that a later strategy
// change can activate it.
func TestStrategyDeniesActive(t *testing.T) {
	h := newHarness(t, 1, time.Second)
	v := h.v
	v.strategy = denyStrategy{}

	p := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")
	if err := v.AddCandidate(p, h.base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}
	probe := h.rec.snapshot()[0]
	if err := v.HandleResponse(1, p.Local, p.Peer, probe.nonce); err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}
	if !v.IsValidated(p) {
		t.Fatal("path should be validated")
	}
	assertNoActive(t, v)
	assertErrIs(t, v.CheckSend(p), ErrPathNotActive)

	// Switching to a permissive strategy activates the already-validated path.
	v.strategy = latestStrategy{}
	v.mu.Lock()
	var acts actions
	v.reselectLocked(&acts)
	v.mu.Unlock()
	acts.run()
	assertActive(t, v, p)
}

// TestNonceSourceFailure makes challenge generation fail and checks the
// candidate is rejected (initial) or failed (retry).
func TestNonceSourceFailure(t *testing.T) {
	base := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(base)
	rec := &recorder{}
	ev := &eventLog{}
	boom := errors.New("entropy unavailable")

	// Failing on the very first challenge rejects registration.
	v, err := NewValidator(Config{
		Conn:        1,
		Clock:       clock,
		NonceSource: func() (Nonce, error) { return Nonce{}, boom },
		Send:        rec.send,
		OnEvent:     ev.record,
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	p := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")
	if err := v.AddCandidate(p, base.Add(2*time.Second)); !errors.Is(err, boom) {
		t.Fatalf("AddCandidate with failing nonce = %v, want %v", err, boom)
	}

	// First nonce succeeds, then every retry fails -> nonce-source failure.
	var calls int
	v2, err := NewValidator(Config{
		Conn:          1,
		Clock:         clock,
		RetryInterval: 100 * time.Millisecond,
		NonceSource: func() (Nonce, error) {
			calls++
			if calls == 1 {
				return Nonce{1}, nil
			}
			return Nonce{}, boom
		},
		Send:    rec.send,
		OnEvent: ev.record,
	})
	if err != nil {
		t.Fatalf("NewValidator v2: %v", err)
	}
	if err := v2.AddCandidate(p, base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate v2: %v", err)
	}
	clock.Advance(150 * time.Millisecond)
	var sawNonceFailure bool
	for _, e := range ev.all() {
		if e.Kind == EventFailed && e.Reason == ReasonNonceSource {
			sawNonceFailure = true
		}
	}
	if !sawNonceFailure {
		t.Fatalf("want nonce-source failure, got %+v", ev.all())
	}
}

// TestSnapshotAndUnknownResponse covers the observability snapshot and a
// response for an unregistered path on the current connection.
func TestSnapshotAndUnknownResponse(t *testing.T) {
	h := newHarness(t, 1, time.Second)
	v := h.v
	p := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")
	if err := v.AddCandidate(p, h.base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}
	snaps := v.Snapshots()
	if len(snaps) != 1 || snaps[0].Path != p || snaps[0].Validated || snaps[0].Active {
		t.Fatalf("unexpected snapshot: %+v", snaps)
	}

	assertErrIs(t,
		v.HandleResponse(1, mustAddr(t, "10.9.9.9:1"), mustAddr(t, "192.0.2.9:1"), Nonce{}),
		ErrUnknownPath)

	// Cancel of an unknown path.
	assertErrIs(t, v.Cancel(pathFor(t, 1, "10.8.8.8:1", "192.0.2.8:1")), ErrUnknownPath)

	// Host accessor.
	host := NewHost(v, func(Path, []byte) error { return nil })
	if host.Validator() != v {
		t.Fatal("Host.Validator mismatch")
	}
}

// TestMigrateActivePathAndIdempotency verifies migrating while a path is
// active emits a deactivation plus migration failures, that a same-ID migrate
// is a no-op, and that the fake clock exposes its next fire time.
func TestMigrateActivePathAndIdempotency(t *testing.T) {
	h := newHarness(t, 1, time.Second)
	v := h.v
	p := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")
	if err := v.AddCandidate(p, h.base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}
	probe := h.rec.snapshot()[0]
	if err := v.HandleResponse(1, p.Local, p.Peer, probe.nonce); err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}
	assertActive(t, v, p)

	// Same connection ID: no state change, no new events.
	eventsBefore := len(h.events.all())
	v.Migrate(1)
	if len(h.events.all()) != eventsBefore {
		t.Fatal("same-ID Migrate must be a no-op")
	}
	if v.Conn() != 1 {
		t.Fatal("same-ID Migrate changed connection")
	}

	// Real migration of an active path: failure + deactivation events.
	v.Migrate(2)
	assertNoActive(t, v)
	if v.Conn() != 2 {
		t.Fatalf("Conn = %d, want 2", v.Conn())
	}
	var sawFailure, sawDeact bool
	for _, e := range h.events.all() {
		if e.Kind == EventFailed && e.Reason == ReasonMigrated {
			sawFailure = true
		}
		if e.Kind == EventDeactivated && e.Path == p {
			sawDeact = true
		}
	}
	if !sawFailure || !sawDeact {
		t.Fatalf("want migration failure + deactivation, got %+v", h.events.all())
	}

	// Close after migration is idempotent and leaves no pending timers.
	v.Close()
	v.Close()
	if h.clock.Pending() != 0 {
		t.Fatalf("pending timers after close = %d", h.clock.Pending())
	}

	// Fake clock next-fire-time helper on a fresh instance.
	fc := NewFakeClock(time.Unix(0, 0))
	if _, ok := fc.earliestAt(); ok {
		t.Fatal("empty clock should have no next fire time")
	}
	fc.AfterFunc(200*time.Millisecond, func() {})
	at, ok := fc.earliestAt()
	if !ok || !at.Equal(time.Unix(0, 200*time.Millisecond.Nanoseconds())) {
		t.Fatalf("earliestAt = %v ok=%v", at, ok)
	}
}

// TestNonceCollisionRetried forces the nonce source to emit a nonce already
// outstanding on another candidate and confirms the validator keeps drawing
// until it gets a unique one.
func TestNonceCollisionRetried(t *testing.T) {
	base := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(base)
	rec := &recorder{}
	var calls int
	nonceSrc := func() (Nonce, error) {
		calls++
		if calls == 1 {
			return Nonce{0xaa}, nil // unique for the first candidate
		}
		if calls == 2 {
			return Nonce{0xaa}, nil // collision with the first; must retry
		}
		return Nonce{0xbb}, nil // unique for the second candidate
	}
	v, err := NewValidator(Config{
		Conn:        1,
		Clock:       clock,
		NonceSource: nonceSrc,
		Send:        rec.send,
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	first := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")
	second := pathFor(t, 1, "10.0.0.2:4000", "192.0.2.1:443")
	if err := v.AddCandidate(first, base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate first: %v", err)
	}
	if err := v.AddCandidate(second, base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate second: %v", err)
	}
	probes := rec.snapshot()
	if probes[0].nonce != (Nonce{0xaa}) {
		t.Fatalf("first nonce = %v, want aa", probes[0].nonce)
	}
	if probes[1].nonce != (Nonce{0xbb}) {
		t.Fatalf("second nonce = %v, want bb (collision must be retried)", probes[1].nonce)
	}

	// The distinct nonces still bind strictly to their own paths.
	assertErrIs(t, v.HandleResponse(1, first.Local, first.Peer, Nonce{0xbb}),
		ErrNonceMismatch)
	if err := v.HandleResponse(1, first.Local, first.Peer, Nonce{0xaa}); err != nil {
		t.Fatalf("validate first with its own nonce: %v", err)
	}
}

// TestCancelActiveFallsBackToNextValidated validates two paths, cancels the
// active one, and confirms the strategy immediately promotes the remaining
// validated candidate instead of leaving the host with no usable path.
func TestCancelActiveFallsBackToNextValidated(t *testing.T) {
	h := newHarness(t, 1, time.Second)
	v := h.v
	first := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")
	second := pathFor(t, 1, "10.0.0.2:4000", "192.0.2.1:443")
	for _, p := range []Path{first, second} {
		if err := v.AddCandidate(p, h.base.Add(2*time.Second)); err != nil {
			t.Fatalf("AddCandidate %v: %v", p, err)
		}
	}
	probes := h.rec.snapshot()
	if err := v.HandleResponse(1, first.Local, first.Peer, probes[0].nonce); err != nil {
		t.Fatalf("validate first: %v", err)
	}
	if err := v.HandleResponse(1, second.Local, second.Peer, probes[1].nonce); err != nil {
		t.Fatalf("validate second: %v", err)
	}
	assertActive(t, v, second) // latestStrategy

	if err := v.Cancel(second); err != nil {
		t.Fatalf("Cancel active: %v", err)
	}
	assertActive(t, v, first)
	if err := v.CheckSend(first); err != nil {
		t.Fatalf("remaining validated path should be sendable: %v", err)
	}

	// Snapshots now show exactly one active, validated candidate.
	snaps := v.Snapshots()
	if len(snaps) != 1 || !snaps[0].Validated || !snaps[0].Active {
		t.Fatalf("unexpected snapshots after cancel: %+v", snaps)
	}
}

// TestStrategyRevokesActivePath activates a path, then flips the strategy to
// one that selects nothing; reselecting must deactivate it and block sends.
func TestStrategyRevokesActivePath(t *testing.T) {
	h := newHarness(t, 1, time.Second)
	v := h.v
	p := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")
	if err := v.AddCandidate(p, h.base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}
	probe := h.rec.snapshot()[0]
	if err := v.HandleResponse(1, p.Local, p.Peer, probe.nonce); err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}
	assertActive(t, v, p)

	v.strategy = denyStrategy{}
	v.mu.Lock()
	var acts actions
	v.reselectLocked(&acts)
	v.mu.Unlock()
	acts.run()
	assertNoActive(t, v)
	assertErrIs(t, v.CheckSend(p), ErrPathNotActive)

	var sawDeact bool
	for _, e := range h.events.all() {
		if e.Kind == EventDeactivated && e.Path == p {
			sawDeact = true
		}
	}
	if !sawDeact {
		t.Fatalf("want deactivation after strategy revoked selection: %+v", h.events.all())
	}

	// Reselecting with the same deny strategy while inactive must be a no-op.
	eventsBefore := len(h.events.all())
	v.mu.Lock()
	acts = nil
	v.reselectLocked(&acts)
	v.mu.Unlock()
	acts.run()
	if len(h.events.all()) != eventsBefore {
		t.Fatal("reselect with no active path and no selection must be a no-op")
	}
}

// TestCancelAfterClose verifies operations on a closed validator return
// ErrClosed rather than mutating state.
func TestCancelAfterClose(t *testing.T) {
	h := newHarness(t, 1, time.Second)
	v := h.v
	p := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")
	if err := v.AddCandidate(p, h.base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}
	v.Close()
	assertErrIs(t, v.Cancel(p), ErrClosed)
}

// TestSnapshotsOrdered covers the multi-candidate snapshot ordering and its
// active flag, plus a nonce source that fails only on a collision redraw.
func TestSnapshotsOrdered(t *testing.T) {
	h := newHarness(t, 1, time.Second)
	v := h.v
	first := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")
	second := pathFor(t, 1, "10.0.0.2:4000", "192.0.2.1:443")
	if err := v.AddCandidate(first, h.base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate first: %v", err)
	}
	if err := v.AddCandidate(second, h.base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate second: %v", err)
	}
	probes := h.rec.snapshot()
	if err := v.HandleResponse(1, second.Local, second.Peer, probes[1].nonce); err != nil {
		t.Fatalf("validate second: %v", err)
	}

	snaps := v.Snapshots()
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(snaps))
	}
	if snaps[0].Path != first || snaps[1].Path != second {
		t.Fatalf("snapshot order = %v then %v, want first then second",
			snaps[0].Path, snaps[1].Path)
	}
	if snaps[0].Validated || snaps[0].Active {
		t.Fatalf("first snapshot should be pending/inactive: %+v", snaps[0])
	}
	if !snaps[1].Validated || !snaps[1].Active {
		t.Fatalf("second snapshot should be validated/active: %+v", snaps[1])
	}
}

// TestCollisionRedrawFailure covers the error branch when a nonce source fails
// specifically while redrawing after a collision.
func TestCollisionRedrawFailure(t *testing.T) {
	base := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(base)
	rec := &recorder{}
	boom := errors.New("entropy exhausted")
	var calls int
	v, err := NewValidator(Config{
		Conn:  1,
		Clock: clock,
		NonceSource: func() (Nonce, error) {
			calls++
			switch calls {
			case 1:
				return Nonce{1}, nil // first candidate
			case 2:
				return Nonce{1}, nil // collision
			default:
				return Nonce{}, boom // redraw fails
			}
		},
		Send: rec.send,
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	first := pathFor(t, 1, "10.0.0.1:4000", "192.0.2.1:443")
	second := pathFor(t, 1, "10.0.0.2:4000", "192.0.2.1:443")
	if err := v.AddCandidate(first, base.Add(2*time.Second)); err != nil {
		t.Fatalf("AddCandidate first: %v", err)
	}
	if err := v.AddCandidate(second, base.Add(2*time.Second)); !errors.Is(err, boom) {
		t.Fatalf("AddCandidate second = %v, want %v", err, boom)
	}
}
