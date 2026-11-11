package netsec

import (
	"testing"
	"time"
)

// fixedStrategy chooses a predetermined path, used to prove the policy (not
// mere validation) decides the active path.
type fixedStrategy struct {
	prefer Path
}

func (s fixedStrategy) Select(validated []Path) (Path, bool) {
	for _, p := range validated {
		if p == s.prefer {
			return p, true
		}
	}
	return Path{}, false
}

// firstStrategy always selects the earliest registered validated candidate.
type firstStrategy struct{}

func (firstStrategy) Select(validated []Path) (Path, bool) {
	if len(validated) == 0 {
		return Path{}, false
	}
	return validated[0], true
}

// TestOnlyStrategySelectedPathActive registers two simultaneous candidates and
// shows that validating both does not make both sendable: only the path the
// strategy selects is active, and switching the policy moves activity without
// any new network traffic.
func TestOnlyStrategySelectedPathActive(t *testing.T) {
	h := newHarness(t, 4, time.Second)
	v := h.v

	alpha := pathFor(t, 4, "10.0.0.1:4000", "192.0.2.1:443")
	beta := pathFor(t, 4, "10.0.0.2:4000", "192.0.2.1:443")

	if err := v.AddCandidate(alpha, h.base.Add(5*time.Second)); err != nil {
		t.Fatalf("AddCandidate alpha: %v", err)
	}
	if err := v.AddCandidate(beta, h.base.Add(5*time.Second)); err != nil {
		t.Fatalf("AddCandidate beta: %v", err)
	}

	// Two distinct probes, each on its own candidate address.
	probes := h.rec.snapshot()
	if len(probes) != 2 {
		t.Fatalf("probes = %d, want 2", len(probes))
	}
	for _, pr := range probes {
		if pr.path != alpha && pr.path != beta {
			t.Fatalf("unexpected probe path %+v", pr.path)
		}
	}

	// Default latestStrategy: the most recently registered (beta) wins.
	betaProbe := probes[1]
	if err := v.HandleResponse(4, beta.Local, beta.Peer, betaProbe.nonce); err != nil {
		t.Fatalf("validate beta: %v", err)
	}
	alphaProbe := probes[0]
	if err := v.HandleResponse(4, alpha.Local, alpha.Peer, alphaProbe.nonce); err != nil {
		t.Fatalf("validate alpha: %v", err)
	}

	if !v.IsValidated(alpha) || !v.IsValidated(beta) {
		t.Fatal("both paths should be validated")
	}
	assertActive(t, v, beta)
	// alpha is validated but not selected -> still not sendable.
	assertErrIs(t, v.CheckSend(alpha), ErrPathNotActive)
	if err := v.CheckSend(beta); err != nil {
		t.Fatalf("CheckSend beta: %v", err)
	}

	// Swap the policy to a fixed preference for alpha and re-run selection.
	v.strategy = fixedStrategy{prefer: alpha}
	v.mu.Lock()
	var acts actions
	v.reselectLocked(&acts)
	v.mu.Unlock()
	acts.run()
	assertActive(t, v, alpha)
	assertErrIs(t, v.CheckSend(beta), ErrPathNotActive)
	if err := v.CheckSend(alpha); err != nil {
		t.Fatalf("CheckSend alpha: %v", err)
	}

	// A first-registered strategy yields the same alpha deterministically.
	v.strategy = firstStrategy{}
	v.mu.Lock()
	acts = nil
	v.reselectLocked(&acts)
	v.mu.Unlock()
	acts.run()
	assertActive(t, v, alpha)

	// Exactly one activation per distinct active change; no spurious event when
	// reselecting the already-active path.
	activations := 0
	for _, ev := range h.events.all() {
		if ev.Kind == EventActivated {
			activations++
		}
	}
	if activations != 2 {
		t.Fatalf("activation events = %d, want 2 (beta then alpha)", activations)
	}
}

// TestUnvalidatedCandidateNeverActive confirms a validated path cannot be
// bypassed by a newer, still-pending candidate, and that the pending candidate
// becomes active only after its own round trip under the default strategy.
func TestUnvalidatedCandidateNeverActive(t *testing.T) {
	h := newHarness(t, 9, time.Second)
	v := h.v

	first := pathFor(t, 9, "10.0.0.1:4000", "192.0.2.1:443")
	second := pathFor(t, 9, "10.0.0.2:4000", "192.0.2.1:443")

	if err := v.AddCandidate(first, h.base.Add(5*time.Second)); err != nil {
		t.Fatalf("AddCandidate first: %v", err)
	}
	firstProbe := h.rec.snapshot()[0]
	if err := v.HandleResponse(9, first.Local, first.Peer, firstProbe.nonce); err != nil {
		t.Fatalf("validate first: %v", err)
	}
	assertActive(t, v, first)

	// A new candidate is pending: the active path stays the validated first.
	if err := v.AddCandidate(second, h.base.Add(5*time.Second)); err != nil {
		t.Fatalf("AddCandidate second: %v", err)
	}
	assertActive(t, v, first)
	assertErrIs(t, v.CheckSend(second), ErrPathNotValidated)

	// Once the pending path validates, latestStrategy promotes it.
	secondProbe := h.rec.snapshot()[1]
	if err := v.HandleResponse(9, second.Local, second.Peer, secondProbe.nonce); err != nil {
		t.Fatalf("validate second: %v", err)
	}
	assertActive(t, v, second)
	assertErrIs(t, v.CheckSend(first), ErrPathNotActive)
}

// TestOutOfOrderResponses covers responses arriving in a different order than
// the challenges were sent. Responses are matched purely by the triple
// (connection, path, nonce), so order does not matter and a nonce issued for
// one candidate can never validate another.
func TestOutOfOrderResponses(t *testing.T) {
	h := newHarness(t, 8, time.Second)
	v := h.v

	a := pathFor(t, 8, "10.0.0.1:4000", "192.0.2.1:443")
	b := pathFor(t, 8, "10.0.0.2:4000", "192.0.2.1:443")
	c := pathFor(t, 8, "10.0.0.3:4000", "192.0.2.1:443")

	for _, p := range []Path{a, b, c} {
		if err := v.AddCandidate(p, h.base.Add(5*time.Second)); err != nil {
			t.Fatalf("AddCandidate: %v", err)
		}
	}
	probes := h.rec.snapshot()
	if len(probes) != 3 {
		t.Fatalf("probes = %d, want 3", len(probes))
	}
	nonceOf := map[Path]Nonce{}
	for _, pr := range probes {
		nonceOf[pr.path] = pr.nonce
	}

	// Respond in reverse registration order: c, then b, then a.
	order := []Path{c, b, a}
	for _, p := range order {
		if err := v.HandleResponse(8, p.Local, p.Peer, nonceOf[p]); err != nil {
			t.Fatalf("out-of-order response for %v: %v", p, err)
		}
	}
	for _, p := range []Path{a, b, c} {
		if !v.IsValidated(p) {
			t.Fatalf("path %v not validated despite out-of-order response", p)
		}
	}

	// Cross-path nonce use must fail regardless of arrival order.
	assertErrIs(t, v.HandleResponse(8, a.Local, a.Peer, nonceOf[b]),
		ErrNonceMismatch)
	assertErrIs(t, v.HandleResponse(8, c.Local, c.Peer, nonceOf[a]),
		ErrNonceMismatch)
	// A random, never-issued nonce is rejected too.
	assertErrIs(t, v.HandleResponse(8, a.Local, a.Peer, Nonce{0xde, 0xad}),
		ErrNonceMismatch)

	// Default strategy selects the last registered (c) as the single active.
	assertActive(t, v, c)
	assertErrIs(t, v.CheckSend(a), ErrPathNotActive)
	assertErrIs(t, v.CheckSend(b), ErrPathNotActive)
	if err := v.CheckSend(c); err != nil {
		t.Fatalf("CheckSend c: %v", err)
	}
}

// TestRetryThenLateResponse shows an out-of-order/late response can still win
// if it carries a currently outstanding nonce and arrives before the fixed
// deadline, while a response after the deadline finds no candidate.
func TestRetryThenLateResponse(t *testing.T) {
	interval := 200 * time.Millisecond
	h := newHarness(t, 10, interval)
	v := h.v

	p := pathFor(t, 10, "10.0.0.1:4000", "192.0.2.1:443")
	deadline := h.base.Add(1 * time.Second)
	if err := v.AddCandidate(p, deadline); err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}

	// Let two retries pass; each issues a fresh outstanding nonce.
	h.clock.Advance(interval * 2)
	probes := h.rec.snapshot()
	if len(probes) != 3 {
		t.Fatalf("probes = %d, want initial + 2 retries", len(probes))
	}

	// The newest nonce arriving late-but-before-deadline still validates.
	latest := probes[len(probes)-1]
	if err := v.HandleResponse(10, p.Local, p.Peer, latest.nonce); err != nil {
		t.Fatalf("late valid response: %v", err)
	}
	assertActive(t, v, p)

	// A separate candidate that misses its deadline rejects late responses.
	q := pathFor(t, 10, "10.0.0.2:4000", "192.0.2.1:443")
	if err := v.AddCandidate(q, h.base.Add(500*time.Millisecond)); err != nil {
		t.Fatalf("AddCandidate q: %v", err)
	}
	qProbe, ok := h.rec.last()
	if !ok || qProbe.path != q {
		t.Fatalf("expected an initial probe for q, got %+v", h.rec.snapshot())
	}
	h.clock.Advance(600 * time.Millisecond)
	assertErrIs(t, v.HandleResponse(10, q.Local, q.Peer, qProbe.nonce),
		ErrUnknownPath)
}
