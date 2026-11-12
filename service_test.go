package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

type testHarness struct {
	svc   *Service
	store *MemoryStore
	clk   *fakeClock
	ttl   time.Duration
}

func newHarness(t *testing.T, ttl time.Duration) *testHarness {
	t.Helper()
	store := NewMemoryStore()
	clk := newFakeClock()
	svc, err := New(Config{Store: store, TTL: ttl, Clock: clk})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &testHarness{svc: svc, store: store, clk: clk, ttl: ttl}
}

func (h *testHarness) put(t *testing.T, id string, payload []byte) {
	t.Helper()
	if err := h.svc.Put(context.Background(), id, payload); err != nil {
		t.Fatalf("Put(%q): %v", id, err)
	}
}

func prove(lease *Lease) []byte {
	return ComputeProof(lease.ProofKey, lease.Nonce, lease.Payload)
}

func assertErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is %v", err, target)
	}
}

func assertTerminalNoServe(t *testing.T, h *testHarness, id string) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.svc.Claim(ctx, id, "anybody"); errors.Is(err, ErrLeaseHeld) || err == nil {
		t.Fatalf("Claim on terminal record unexpectedly served: %v", err)
	}
	if _, err := h.svc.Retransmit(ctx, id, "anybody", []byte("x")); err == nil {
		t.Fatalf("Retransmit on terminal record unexpectedly served")
	}
	if err := h.svc.Acknowledge(ctx, id, "anybody", []byte("x"), []byte("y")); err == nil {
		t.Fatalf("Acknowledge on terminal record unexpectedly succeeded")
	}
	if err := h.svc.Put(ctx, id, []byte("new")); !errors.Is(err, ErrExists) {
		t.Fatalf("re-Put terminal id: %v, want ErrExists", err)
	}
}

// A full happy path: claim, confirm, destroy, delivered terminal state.
func TestClaimAcknowledgeDestroy(t *testing.T) {
	h := newHarness(t, time.Minute)
	secret := []byte("top-secret-payload")
	h.put(t, "s1", secret)

	lease, err := h.svc.Claim(context.Background(), "s1", "session-A")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !bytes.Equal(lease.Payload, secret) {
		t.Fatalf("lease payload = %q, want %q", lease.Payload, secret)
	}
	if !lease.ExpiresAt.Equal(h.clk.Now().Add(h.ttl)) {
		t.Fatalf("expiry = %v, want %v", lease.ExpiresAt, h.clk.Now().Add(h.ttl))
	}
	if lease.Attempt != 1 || len(lease.Nonce) != 32 || len(lease.ProofKey) != 32 {
		t.Fatalf("lease fields wrong: %+v", lease)
	}

	if err := h.svc.Acknowledge(context.Background(), "s1", "session-A", lease.Nonce, prove(lease)); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if h.store.Has("s1") {
		t.Fatal("backend still holds the payload after delivery")
	}
	outcome, err := h.svc.Outcome("s1")
	if err != nil {
		t.Fatalf("Outcome: %v", err)
	}
	if outcome.State != StateDelivered {
		t.Fatalf("state = %s, want delivered", outcome.State)
	}

	// Delivered records are terminal: no reissue, no duplicate ack.
	assertTerminalNoServe(t, h, "s1")
	if _, err := h.svc.Claim(context.Background(), "s1", "session-A"); !errors.Is(err, ErrAlreadyDelivered) {
		t.Fatalf("Claim after delivery: %v, want ErrAlreadyDelivered", err)
	}

	events := h.svc.Events()
	last := events[len(events)-1]
	if last.Type != EventDelivered || last.SecretID != "s1" {
		t.Fatalf("last event = %+v", last)
	}
}

// A dropped receipt must not count as delivery: the same lease can retransmit
// and later confirm; wrong sessions/nonces are rejected all the while.
func TestDroppedReceiptRetransmitOnlySameLease(t *testing.T) {
	h := newHarness(t, time.Minute)
	secret := []byte("payload-for-receipt-loss")
	h.put(t, "s2", secret)
	ctx := context.Background()

	lease, err := h.svc.Claim(ctx, "s2", "session-A")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	deadline := lease.ExpiresAt

	// Another client cannot take over while the lease is live.
	if _, err := h.svc.Claim(ctx, "s2", "session-B"); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("concurrent Claim: %v, want ErrLeaseHeld", err)
	}
	// The holder cannot start a new lease either; only Retransmit is allowed.
	if _, err := h.svc.Claim(ctx, "s2", "session-A"); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("holder re-Claim: %v, want ErrLeaseHeld", err)
	}
	// Wrong session, wrong nonce, and both are refused.
	if _, err := h.svc.Retransmit(ctx, "s2", "session-B", lease.Nonce); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("Retransmit wrong session: %v", err)
	}
	if _, err := h.svc.Retransmit(ctx, "s2", "session-A", bytes.Repeat([]byte{1}, 32)); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("Retransmit wrong nonce: %v", err)
	}

	// Simulate receipt loss: invalid/missing proofs never move the state.
	badProof := bytes.Clone(prove(lease))
	badProof[0] ^= 0xFF
	if err := h.svc.Acknowledge(ctx, "s2", "session-A", lease.Nonce, badProof); !errors.Is(err, ErrInvalidProof) {
		t.Fatalf("bad proof: %v, want ErrInvalidProof", err)
	}
	outcome, _ := h.svc.Outcome("s2")
	if outcome.State != StateLeased {
		t.Fatalf("state after bad proof = %s, want leased", outcome.State)
	}

	// Valid retransmit under the same lease: same nonce/key, deadline unchanged.
	retry, err := h.svc.Retransmit(ctx, "s2", "session-A", lease.Nonce)
	if err != nil {
		t.Fatalf("Retransmit: %v", err)
	}
	if !bytes.Equal(retry.Payload, secret) || !bytes.Equal(retry.Nonce, lease.Nonce) {
		t.Fatal("retransmitted lease changed nonce or payload")
	}
	if !retry.ExpiresAt.Equal(deadline) {
		t.Fatalf("retransmit extended deadline: %v vs %v", retry.ExpiresAt, deadline)
	}
	if retry.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", retry.Attempt)
	}

	// Original lease material still proves the retransmitted delivery.
	if err := h.svc.Acknowledge(ctx, "s2", "session-A", retry.Nonce, prove(retry)); err != nil {
		t.Fatalf("Acknowledge after retransmit: %v", err)
	}
	outcome, _ = h.svc.Outcome("s2")
	if outcome.State != StateDelivered || outcome.Attempts != 2 {
		t.Fatalf("outcome = %+v, want delivered with 2 attempts", outcome)
	}
	if h.store.Has("s2") {
		t.Fatal("payload not destroyed after confirmation")
	}

	// Rejected proofs are audited without any secret material.
	var sawRejection bool
	for _, ev := range h.svc.Events() {
		if ev.Type == EventAckRejected {
			sawRejection = true
			if bytes.Contains([]byte(ev.Detail), secret) {
				t.Fatal("audit detail leaks payload")
			}
		}
	}
	if !sawRejection {
		t.Fatal("expected an ack_rejected audit event")
	}
}

// Many concurrent Claim calls must produce exactly one lease winner; everyone
// else gets ErrLeaseHeld and never observes the payload.
func TestConcurrentClaimSingleWinner(t *testing.T) {
	h := newHarness(t, time.Minute)
	secret := []byte("concurrent-secret")
	h.put(t, "s3", secret)
	ctx := context.Background()

	const n = 64
	var wg sync.WaitGroup
	var winners int32
	var held int32
	var payloadSeen int32
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			lease, err := h.svc.Claim(ctx, "s3", fmt.Sprintf("session-%d", i))
			switch {
			case err == nil:
				atomic.AddInt32(&winners, 1)
				if bytes.Equal(lease.Payload, secret) {
					atomic.AddInt32(&payloadSeen, 1)
				}
			case errors.Is(err, ErrLeaseHeld):
				atomic.AddInt32(&held, 1)
			default:
				t.Errorf("Claim %d: unexpected error %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if winners != 1 {
		t.Fatalf("winners = %d, want 1", winners)
	}
	if held != n-1 {
		t.Fatalf("held = %d, want %d", held, n-1)
	}
	if payloadSeen != 1 {
		t.Fatalf("payload observers = %d, want 1", payloadSeen)
	}
	outcome, _ := h.svc.Outcome("s3")
	if outcome.State != StateLeased {
		t.Fatalf("state = %s, want leased", outcome.State)
	}
}

// After lease timeout the payload is destroyed, the result is recorded as
// unknown, and the secret can never be reissued - even to the original holder.
func TestLeaseExpiryDestroysAndGoesUnknown(t *testing.T) {
	h := newHarness(t, time.Minute)
	secret := []byte("expiring-secret")
	h.put(t, "s4", secret)
	ctx := context.Background()

	lease, err := h.svc.Claim(ctx, "s4", "session-A")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	h.clk.advance(h.ttl - time.Second)
	// Still inside the window: same-lease retransmit works and does not extend.
	if _, err := h.svc.Retransmit(ctx, "s4", "session-A", lease.Nonce); err != nil {
		t.Fatalf("Retransmit before expiry: %v", err)
	}
	h.clk.advance(2 * time.Second)

	// Lazy expiry on touch: retransmit, claim and ack all observe the terminal.
	if _, err := h.svc.Retransmit(ctx, "s4", "session-A", lease.Nonce); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("Retransmit after expiry: %v", err)
	}
	outcome, err := h.svc.Outcome("s4")
	if err != nil {
		t.Fatalf("Outcome: %v", err)
	}
	if outcome.State != StateUnknown || outcome.Reason != ReasonLeaseExpired {
		t.Fatalf("outcome = %+v, want unknown/lease_expired", outcome)
	}
	if h.store.Has("s4") {
		t.Fatal("payload survived lease expiry")
	}
	assertTerminalNoServe(t, h, "s4")

	events := h.svc.Events()
	last := events[len(events)-1]
	if last.Type != EventExpired || last.Reason != ReasonLeaseExpired {
		t.Fatalf("last event = %+v, want lease_expired", last)
	}
}

// Sweep explicitly expires multiple overdue leases in one pass and reports the
// count; a second sweep is a no-op.
func TestSweepExpiresOverdueLeases(t *testing.T) {
	h := newHarness(t, time.Minute)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		h.put(t, id, []byte("payload-"+id))
		if _, err := h.svc.Claim(ctx, id, "session-"+id); err != nil {
			t.Fatalf("Claim %q: %v", id, err)
		}
	}
	h.clk.advance(h.ttl)
	if got := h.svc.Sweep(ctx); got != 3 {
		t.Fatalf("Sweep count = %d, want 3", got)
	}
	if got := h.svc.Sweep(ctx); got != 0 {
		t.Fatalf("second Sweep count = %d, want 0", got)
	}
	for _, id := range []string{"a", "b", "c"} {
		outcome, _ := h.svc.Outcome(id)
		if outcome.State != StateUnknown || outcome.Reason != ReasonLeaseExpired {
			t.Fatalf("outcome[%s] = %+v", id, outcome)
		}
		if h.store.Has(id) {
			t.Fatalf("payload %q survived sweep", id)
		}
	}
}

// Backend destruction failure after a valid client proof: the service must not
// report delivery, goes terminal-unknown, and never serves or reissues data.
// The leak boundary is the backend blob only; service state keeps no bytes.
func TestDestroyFailureAtAcknowledge(t *testing.T) {
	h := newHarness(t, time.Minute)
	secret := []byte("destroy-fail-secret")
	h.put(t, "s5", secret)
	ctx := context.Background()

	lease, err := h.svc.Claim(ctx, "s5", "session-A")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	boom := errors.New("backend disk on fire")
	var failTarget string
	h.store.FailDelete(func(id string) error {
		if id == failTarget {
			return boom
		}
		return nil
	})
	failTarget = "s5"

	err = h.svc.Acknowledge(ctx, "s5", "session-A", lease.Nonce, prove(lease))
	if !errors.Is(err, ErrDestroyFailed) || !errors.Is(err, boom) {
		t.Fatalf("Acknowledge error = %v, want ErrDestroyFailed wrapping boom", err)
	}
	outcome, _ := h.svc.Outcome("s5")
	if outcome.State != StateUnknown || outcome.Reason != ReasonDestroyFailed {
		t.Fatalf("outcome = %+v, want unknown/destroy_failed", outcome)
	}
	// Backend still physically holds the blob: this is the documented leak scope.
	if !h.store.Has("s5") {
		t.Fatal("expected backend blob to remain after failed delete")
	}
	if h.store.DeleteCalls() != 1 {
		t.Fatalf("delete attempts = %d, want 1 (no blind retry storm)", h.store.DeleteCalls())
	}
	// The service layer itself retains no payload, nonce or proof key.
	h.svc.mu.Lock()
	rec := h.svc.secrets["s5"]
	if len(rec.nonce) != 0 || len(rec.proofKey) != 0 {
		h.svc.mu.Unlock()
		t.Fatal("service record still holds nonce/proof key after failure")
	}
	h.svc.mu.Unlock()
	assertTerminalNoServe(t, h, "s5")

	// A later retry of the same confirmation must not succeed, and must not
	// trigger another delete attempt.
	if err := h.svc.Acknowledge(ctx, "s5", "session-A", lease.Nonce, prove(lease)); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("retry ack: %v, want ErrOutcomeUnknown", err)
	}
	if h.store.DeleteCalls() != 1 {
		t.Fatalf("delete attempts after retry = %d, still want 1", h.store.DeleteCalls())
	}

	events := h.svc.Events()
	last := events[len(events)-1]
	if last.Type != EventDestroyFailed || last.Reason != ReasonDestroyFailed {
		t.Fatalf("last event = %+v", last)
	}
	if bytes.Contains([]byte(last.Detail), secret) {
		t.Fatal("audit detail leaks payload")
	}
}

// Backend destruction failure during timeout expiry: unknown terminal state,
// reason lease_expired, failure detail retained, no reissue possible.
func TestDestroyFailureAtExpiry(t *testing.T) {
	h := newHarness(t, time.Minute)
	secret := []byte("expiry-destroy-fail")
	h.put(t, "s6", secret)
	ctx := context.Background()

	lease, err := h.svc.Claim(ctx, "s6", "session-A")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	boom := errors.New("quota exhausted")
	h.store.FailDelete(func(string) error { return boom })
	h.clk.advance(h.ttl + time.Second)

	if _, err := h.svc.Retransmit(ctx, "s6", "session-A", lease.Nonce); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("expired retransmit: %v", err)
	}
	outcome, _ := h.svc.Outcome("s6")
	if outcome.State != StateUnknown || outcome.Reason != ReasonLeaseExpired {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !h.store.Has("s6") {
		t.Fatal("expected backend blob to remain when expiry delete failed")
	}
	assertTerminalNoServe(t, h, "s6")
}

// Audit events and Outcome must never carry payload, nonce or proof-key bytes.
func TestNoSecretMaterialInObservability(t *testing.T) {
	h := newHarness(t, time.Minute)
	secret := []byte("observability-secret")
	h.put(t, "s7", secret)
	ctx := context.Background()

	lease, err := h.svc.Claim(ctx, "s7", "session-A")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	wrong := bytes.Clone(prove(lease))
	wrong[0] ^= 0xFF
	_ = h.svc.Acknowledge(ctx, "s7", "session-A", lease.Nonce, wrong)
	if err := h.svc.Acknowledge(ctx, "s7", "session-A", lease.Nonce, prove(lease)); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	for _, ev := range h.svc.Events() {
		blob := []byte(ev.SecretID + ev.SessionID + ev.Detail + string(ev.State) + string(ev.Type) + string(ev.Reason))
		if bytes.Contains(blob, secret) {
			t.Fatalf("audit event leaks payload: %+v", ev)
		}
		if bytes.Contains(blob, lease.Nonce) || bytes.Contains(blob, lease.ProofKey) {
			t.Fatalf("audit event leaks lease material: %+v", ev)
		}
	}
	outcome, _ := h.svc.Outcome("s7")
	if bytes.Contains([]byte(outcome.SessionID), secret) {
		t.Fatal("outcome leaks payload")
	}
}

// A proof is bound to payload and nonce: tampered content or a foreign nonce
// cannot be confirmed.
func TestProofBindingToPayloadAndNonce(t *testing.T) {
	h := newHarness(t, time.Minute)
	h.put(t, "s8", []byte("bound-payload"))
	ctx := context.Background()
	lease, err := h.svc.Claim(ctx, "s8", "session-A")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	tampered := bytes.Clone(lease.Payload)
	tampered[0]++
	proofForTampered := ComputeProof(lease.ProofKey, lease.Nonce, tampered)
	if err := h.svc.Acknowledge(ctx, "s8", "session-A", lease.Nonce, proofForTampered); !errors.Is(err, ErrInvalidProof) {
		t.Fatalf("proof over tampered payload: %v, want ErrInvalidProof", err)
	}
	foreignNonce := bytes.Repeat([]byte{9}, 32)
	proofForeign := ComputeProof(lease.ProofKey, foreignNonce, lease.Payload)
	if err := h.svc.Acknowledge(ctx, "s8", "session-A", foreignNonce, proofForeign); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("proof with foreign nonce: %v, want ErrLeaseMismatch", err)
	}
	outcome, _ := h.svc.Outcome("s8")
	if outcome.State != StateLeased {
		t.Fatalf("state = %s, want leased after rejected proofs", outcome.State)
	}
	if err := h.svc.Acknowledge(ctx, "s8", "session-A", lease.Nonce, prove(lease)); err != nil {
		t.Fatalf("valid Acknowledge: %v", err)
	}
}

// Every claim mints fresh nonce and proof key; leases and DTOs return copies
// that cannot mutate service-internal state.
func TestLeaseMaterialFreshAndIsolated(t *testing.T) {
	h := newHarness(t, time.Minute)
	ctx := context.Background()
	h.put(t, "x1", []byte("one"))
	h.put(t, "x2", []byte("two"))

	l1, err := h.svc.Claim(ctx, "x1", "s")
	if err != nil {
		t.Fatalf("Claim x1: %v", err)
	}
	h.clk.advance(h.ttl + time.Second)
	l2, err := h.svc.Claim(ctx, "x2", "s")
	if err != nil {
		t.Fatalf("Claim x2: %v", err)
	}
	if bytes.Equal(l1.Nonce, l2.Nonce) || bytes.Equal(l1.ProofKey, l2.ProofKey) {
		t.Fatal("nonce/proof key reused across claims")
	}

	// Mutating caller-owned slices must not affect service behavior.
	l2.Payload[0] = '?'
	l2.Nonce[0]++
	l2.ProofKey[0]++
	retry, err := h.svc.Retransmit(ctx, "x2", "s", l2.Nonce)
	if err == nil {
		t.Fatalf("Retransmit with mutated nonce unexpectedly succeeded: %+v", retry)
	}
}

// Unknown ids and pre-claim lease operations return the documented errors.
func TestNotFoundAndNoLease(t *testing.T) {
	h := newHarness(t, time.Minute)
	ctx := context.Background()
	if _, err := h.svc.Claim(ctx, "missing", "s"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Claim missing: %v", err)
	}
	h.put(t, "s9", []byte("v"))
	if _, err := h.svc.Retransmit(ctx, "s9", "s", []byte("n")); !errors.Is(err, ErrNoLease) {
		t.Fatalf("Retransmit before claim: %v", err)
	}
	if err := h.svc.Acknowledge(ctx, "s9", "s", []byte("n"), []byte("p")); !errors.Is(err, ErrNoLease) {
		t.Fatalf("Acknowledge before claim: %v", err)
	}
	if _, err := h.svc.Outcome("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Outcome missing: %v", err)
	}
}

// New rejects unusable configurations.
func TestNewValidation(t *testing.T) {
	if _, err := New(Config{Store: NewMemoryStore(), TTL: 0}); err == nil {
		t.Fatal("expected error for zero TTL")
	}
	if _, err := New(Config{TTL: time.Minute}); err == nil {
		t.Fatal("expected error for nil store")
	}
}
