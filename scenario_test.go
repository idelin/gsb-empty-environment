package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func newScenarioManager(t *testing.T) (*Manager, *fakeClock) {
	t.Helper()
	clock := newFakeClock(time.Date(2026, 2, 2, 9, 0, 0, 0, time.UTC))
	manager, err := NewManagerWithClock([]byte("scenario-hmac-secret"), clock.now)
	if err != nil {
		t.Fatalf("NewManagerWithClock: %v", err)
	}
	return manager, clock
}

func mustProvision(t *testing.T, manager *Manager, accountID string, ch Channel) {
	t.Helper()
	if err := manager.Provision(accountID, ch); err != nil {
		t.Fatalf("Provision: %v", err)
	}
}

func mustBegin(t *testing.T, manager *Manager, accountID string, next Channel, ttl time.Duration) ChangeView {
	t.Helper()
	view, err := manager.Begin(accountID, next, ttl)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return view
}

func mustProof(t *testing.T, manager *Manager, accountID, changeID string, role Role) string {
	t.Helper()
	token, _, err := manager.SendChallenge(accountID, changeID, role)
	if err != nil {
		t.Fatalf("SendChallenge %s: %v", role, err)
	}
	return token
}

func mustPending(t *testing.T, manager *Manager, accountID string) ChangeView {
	t.Helper()
	view, err := manager.Pending(accountID)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	return view
}

func confirmPair(t *testing.T, manager *Manager, accountID string, view ChangeView, first, second Role) {
	t.Helper()
	token1 := mustProof(t, manager, accountID, view.ID, first)
	if _, err := manager.Confirm(accountID, view.ID, first, token1); err != nil {
		t.Fatalf("Confirm %s: %v", first, err)
	}

	active, err := manager.ActiveChannel(accountID)
	if err != nil {
		t.Fatalf("ActiveChannel mid-flight: %v", err)
	}
	if !active.equal(view.Old) {
		t.Fatalf("active channel changed after single confirmation: got %v want %v", active, view.Old)
	}

	token2 := mustProof(t, manager, accountID, view.ID, second)
	committed, err := manager.Confirm(accountID, view.ID, second, token2)
	if err != nil {
		t.Fatalf("Confirm %s: %v", second, err)
	}
	if committed.Status != StatusCommitted {
		t.Fatalf("expected committed status, got %v", committed.Status)
	}
}

// TestHappyPathAtomicCommit covers the normal flow: neither confirmation alone
// moves recovery power; only the pair commits the swap.
func TestHappyPathAtomicCommit(t *testing.T) {
	manager, _ := newScenarioManager(t)
	accountID := "acct-happy"
	old := Channel{ID: "old@example.test", Kind: "email"}
	next := Channel{ID: "new@example.test", Kind: "email"}
	mustProvision(t, manager, accountID, old)

	view := mustBegin(t, manager, accountID, next, time.Hour)
	if view.Status != StatusPending || view.OldConfirmed || view.NewConfirmed {
		t.Fatalf("unexpected initial view: %+v", view)
	}

	replayProofs := map[Role]string{
		RoleOld: mustProof(t, manager, accountID, view.ID, RoleOld),
		RoleNew: mustProof(t, manager, accountID, view.ID, RoleNew),
	}

	confirmPair(t, manager, accountID, view, RoleOld, RoleNew)

	active, err := manager.ActiveChannel(accountID)
	if err != nil {
		t.Fatalf("ActiveChannel: %v", err)
	}
	if !active.equal(next) {
		t.Fatalf("active = %v, want %v", active, next)
	}
	if _, err := manager.Pending(accountID); !errors.Is(err, ErrNoPendingChange) {
		t.Fatalf("Pending after commit = %v, want ErrNoPendingChange", err)
	}

	for _, role := range []Role{RoleOld, RoleNew} {
		if _, err := manager.Confirm(accountID, view.ID, role, replayProofs[role]); err == nil {
			t.Fatalf("replay of %s proof after commit accepted", role)
		}
	}
}

// TestOutOfOrderConfirmations verifies new-channel-first ordering.
func TestOutOfOrderConfirmations(t *testing.T) {
	manager, _ := newScenarioManager(t)
	accountID := "acct-order"
	old := Channel{ID: "old@example.test", Kind: "email"}
	next := Channel{ID: "new@example.test", Kind: "email"}
	mustProvision(t, manager, accountID, old)

	view := mustBegin(t, manager, accountID, next, time.Hour)
	confirmPair(t, manager, accountID, view, RoleNew, RoleOld)

	active, err := manager.ActiveChannel(accountID)
	if err != nil || !active.equal(next) {
		t.Fatalf("active = %v (err=%v), want %v", active, err, next)
	}
}

// TestDeadlineBoundary pins the exact term semantics: valid strictly before
// the deadline, invalid at or after it.
func TestDeadlineBoundary(t *testing.T) {
	manager, clock := newScenarioManager(t)
	accountID := "acct-deadline"
	old := Channel{ID: "old@example.test", Kind: "email"}
	next := Channel{ID: "new@example.test", Kind: "email"}
	mustProvision(t, manager, accountID, old)

	view := mustBegin(t, manager, accountID, next, time.Hour)

	clock.advance(time.Hour - 2*time.Nanosecond)
	oldProof := mustProof(t, manager, accountID, view.ID, RoleOld)
	if _, err := manager.Confirm(accountID, view.ID, RoleOld, oldProof); err != nil {
		t.Fatalf("confirm at deadline-2ns: %v", err)
	}

	clock.advance(time.Nanosecond) // now deadline-1ns
	newProof := mustProof(t, manager, accountID, view.ID, RoleNew)
	if _, err := manager.Confirm(accountID, view.ID, RoleNew, newProof); err != nil {
		t.Fatalf("confirm at deadline-1ns: %v", err)
	}
	active, _ := manager.ActiveChannel(accountID)
	if !active.equal(next) {
		t.Fatalf("commit before deadline failed: active=%v", active)
	}

	// A fresh transaction observed exactly at its deadline must be dead.
	expiring := mustBegin(t, manager, accountID, Channel{ID: "third@example.test", Kind: "email"}, time.Hour)
	expiringProofs := map[Role]string{
		RoleOld: mustProof(t, manager, accountID, expiring.ID, RoleOld),
		RoleNew: mustProof(t, manager, accountID, expiring.ID, RoleNew),
	}
	clock.advance(time.Hour) // exactly the deadline

	if _, err := manager.Pending(accountID); !errors.Is(err, ErrExpired) {
		t.Fatalf("Pending at deadline = %v, want ErrExpired", err)
	}
	for _, role := range []Role{RoleOld, RoleNew} {
		if _, err := manager.Confirm(accountID, expiring.ID, role, expiringProofs[role]); !errors.Is(err, ErrExpired) {
			t.Fatalf("Confirm at deadline role %s = %v, want ErrExpired", role, err)
		}
		if _, _, err := manager.SendChallenge(accountID, expiring.ID, role); !errors.Is(err, ErrExpired) {
			t.Fatalf("SendChallenge at deadline role %s = %v, want ErrExpired", role, err)
		}
	}

	// Expiry never removes recovery power; revocation still clears the state.
	active, _ = manager.ActiveChannel(accountID)
	if !active.equal(next) {
		t.Fatalf("expiry changed active channel: %v", active)
	}
	if err := manager.Revoke(accountID, expiring.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("Revoke at deadline = %v, want ErrExpired", err)
	}
	if _, err := manager.Pending(accountID); !errors.Is(err, ErrNoPendingChange) {
		t.Fatalf("Pending after expiry cleanup = %v", err)
	}
	active, _ = manager.ActiveChannel(accountID)
	if !active.equal(next) {
		t.Fatalf("revocation of expired change altered active channel: %v", active)
	}
}

// TestResponseLoss ensures challenge re-delivery is idempotent and the
// recovered token still completes the transaction.
func TestResponseLoss(t *testing.T) {
	manager, _ := newScenarioManager(t)
	accountID := "acct-loss"
	old := Channel{ID: "old@example.test", Kind: "email"}
	next := Channel{ID: "new@example.test", Kind: "email"}
	mustProvision(t, manager, accountID, old)

	view := mustBegin(t, manager, accountID, next, time.Hour)
	first := mustProof(t, manager, accountID, view.ID, RoleOld)

	// Simulate three lost responses, then a successful retry.
	for i := 0; i < 3; i++ {
		token, deadline, err := manager.SendChallenge(accountID, view.ID, RoleOld)
		if err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
		if token != first || !deadline.Equal(view.Deadline) {
			t.Fatalf("retry %d changed the proof or term", i)
		}
		pending := mustPending(t, manager, accountID)
		if pending.OldConfirmed || pending.NewConfirmed {
			t.Fatalf("re-delivery mutated confirmation state")
		}
	}

	confirmPair(t, manager, accountID, view, RoleOld, RoleNew)
	active, _ := manager.ActiveChannel(accountID)
	if !active.equal(next) {
		t.Fatalf("active = %v, want %v", active, next)
	}
}

// TestRevocationInvalidatesProofs checks that explicit cancel kills both
// outstanding proofs while leaving the active channel in place.
func TestRevocationInvalidatesProofs(t *testing.T) {
	manager, _ := newScenarioManager(t)
	accountID := "acct-revoke"
	old := Channel{ID: "old@example.test", Kind: "email"}
	next := Channel{ID: "new@example.test", Kind: "email"}
	mustProvision(t, manager, accountID, old)

	view := mustBegin(t, manager, accountID, next, time.Hour)
	oldProof := mustProof(t, manager, accountID, view.ID, RoleOld)
	newProof := mustProof(t, manager, accountID, view.ID, RoleNew)
	if _, err := manager.Confirm(accountID, view.ID, RoleOld, oldProof); err != nil {
		t.Fatalf("old confirm: %v", err)
	}

	if err := manager.Revoke(accountID, view.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	for role, token := range map[Role]string{RoleOld: oldProof, RoleNew: newProof} {
		if _, err := manager.Confirm(accountID, view.ID, role, token); err == nil {
			t.Fatalf("proof for %s accepted after revocation", role)
		}
	}
	active, _ := manager.ActiveChannel(accountID)
	if !active.equal(old) {
		t.Fatalf("revocation changed active channel: %v", active)
	}

	history, err := manager.History(accountID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	last := history[len(history)-1]
	if last.Status != StatusRevoked || !last.OldConfirmed || last.NewConfirmed {
		t.Fatalf("archived view wrong: %+v", last)
	}

	// A fresh change on the same account works and can still commit.
	retry := mustBegin(t, manager, accountID, next, time.Hour)
	confirmPair(t, manager, accountID, retry, RoleOld, RoleNew)
	active, _ = manager.ActiveChannel(accountID)
	if !active.equal(next) {
		t.Fatalf("active after retry = %v, want %v", active, next)
	}
}

// TestConcurrentSecondChangeSupersedes verifies the second Begin archives the
// first and invalidates all of its proofs, including when both target the
// same channel pair.
func TestConcurrentSecondChangeSupersedes(t *testing.T) {
	manager, _ := newScenarioManager(t)
	accountID := "acct-supersede"
	old := Channel{ID: "old@example.test", Kind: "email"}
	next := Channel{ID: "new@example.test", Kind: "email"}
	mustProvision(t, manager, accountID, old)

	first := mustBegin(t, manager, accountID, next, time.Hour)
	firstProofs := map[Role]string{
		RoleOld: mustProof(t, manager, accountID, first.ID, RoleOld),
		RoleNew: mustProof(t, manager, accountID, first.ID, RoleNew),
	}
	second := mustBegin(t, manager, accountID, next, time.Hour)
	if first.Digest.Equal(second.Digest) {
		t.Fatalf("two changes over the same channels share a digest")
	}

	current := mustPending(t, manager, accountID)
	if current.ID != second.ID {
		t.Fatalf("second Begin did not replace pending transaction")
	}

	for role, token := range firstProofs {
		if _, err := manager.Confirm(accountID, first.ID, role, token); !errors.Is(err, ErrSuperseded) {
			t.Fatalf("superseded proof role %s = %v, want ErrSuperseded", role, err)
		}
	}

	confirmPair(t, manager, accountID, second, RoleOld, RoleNew)
	active, _ := manager.ActiveChannel(accountID)
	if !active.equal(next) {
		t.Fatalf("active = %v, want %v", active, next)
	}

	history, _ := manager.History(accountID)
	sawSuperseded, sawCommitted := false, false
	for _, view := range history {
		sawSuperseded = sawSuperseded || view.Status == StatusSuperseded
		sawCommitted = sawCommitted || view.Status == StatusCommitted
	}
	if !sawSuperseded || !sawCommitted {
		t.Fatalf("history missing lifecycle states: superseded=%v committed=%v", sawSuperseded, sawCommitted)
	}
}

func flipLastByte(token string) string {
	raw, err := tokenEncoding.DecodeString(token)
	if err != nil {
		return token
	}
	raw[len(raw)-1] ^= 0x01
	return tokenEncoding.EncodeToString(raw)
}

// TestProofBinding covers every cross-boundary replay the HMAC token rejects.
func TestProofBinding(t *testing.T) {
	manager, _ := newScenarioManager(t)
	accountID := "acct-binding"
	old := Channel{ID: "old@example.test", Kind: "email"}
	next := Channel{ID: "new@example.test", Kind: "email"}
	mustProvision(t, manager, accountID, old)

	view := mustBegin(t, manager, accountID, next, time.Hour)
	oldProof := mustProof(t, manager, accountID, view.ID, RoleOld)
	newProof := mustProof(t, manager, accountID, view.ID, RoleNew)

	// Role confusion: an old-channel proof presented as the new role.
	if _, err := manager.Confirm(accountID, view.ID, RoleNew, oldProof); !errors.Is(err, ErrRoleMismatch) {
		t.Fatalf("cross-role proof = %v, want ErrRoleMismatch", err)
	}
	if _, err := manager.Confirm(accountID, view.ID, RoleOld, newProof); !errors.Is(err, ErrRoleMismatch) {
		t.Fatalf("cross-role proof = %v, want ErrRoleMismatch", err)
	}

	// Tampered token fails signature verification.
	if _, err := manager.Confirm(accountID, view.ID, RoleOld, flipLastByte(oldProof)); !errors.Is(err, ErrProofVerification) {
		t.Fatalf("tampered proof = %v, want ErrProofVerification", err)
	}

	// Garbage tokens are rejected as malformed.
	for _, garbage := range []string{"", "not-base64!!", "AAAA"} {
		if _, err := manager.Confirm(accountID, view.ID, RoleOld, garbage); !errors.Is(err, ErrInvalidProof) {
			t.Fatalf("garbage proof %q = %v, want ErrInvalidProof", garbage, err)
		}
	}

	// A token signed with another manager secret must not verify.
	other, _ := NewManagerWithClock([]byte("a-different-secret"), manager.clock)
	mustProvision(t, other, accountID, old)
	otherView := mustBegin(t, other, accountID, next, time.Hour)
	foreign := mustProof(t, other, accountID, otherView.ID, RoleOld)
	if _, err := manager.Confirm(accountID, view.ID, RoleOld, foreign); !errors.Is(err, ErrProofVerification) {
		t.Fatalf("foreign-secret proof = %v, want ErrProofVerification", err)
	}

	// Unknown change id cannot be used to confirm.
	if _, err := manager.Confirm(accountID, "unknown-id", RoleOld, oldProof); !errors.Is(err, ErrUnknownChange) {
		t.Fatalf("unknown change id = %v, want ErrUnknownChange", err)
	}

	// Double submission of the same single-use proof is rejected.
	if _, err := manager.Confirm(accountID, view.ID, RoleOld, oldProof); err != nil {
		t.Fatalf("first old confirm: %v", err)
	}
	if _, err := manager.Confirm(accountID, view.ID, RoleOld, oldProof); !errors.Is(err, ErrRoleAlreadyDone) {
		t.Fatalf("duplicate proof = %v, want ErrRoleAlreadyDone", err)
	}

	// The digest is deterministic for identical inputs and stable across calls.
	digestA := computeDigest(accountID, view.ID, old, next)
	digestB := computeDigest(accountID, view.ID, old, next)
	if !digestA.Equal(digestB) || !digestA.Equal(view.Digest) {
		t.Fatalf("digest computation unstable: %s vs %s vs %s", digestA, digestB, view.Digest)
	}
	if computeDigest("other-account", view.ID, old, next).Equal(digestA) {
		t.Fatalf("digest must include the account id")
	}

	// Finish legitimately and check no unverified channel ever gained power.
	active, _ := manager.ActiveChannel(accountID)
	if !active.equal(old) {
		t.Fatalf("active channel changed before both confirmations: %v", active)
	}
	if _, err := manager.Confirm(accountID, view.ID, RoleNew, newProof); err != nil {
		t.Fatalf("final new confirm: %v", err)
	}
	active, _ = manager.ActiveChannel(accountID)
	if !active.equal(next) {
		t.Fatalf("active = %v, want %v", active, next)
	}
}

// TestBeginValidation guards the preconditions that must fail without side
// effects.
func TestBeginValidation(t *testing.T) {
	manager, _ := newScenarioManager(t)
	accountID := "acct-validation"
	old := Channel{ID: "old@example.test", Kind: "email"}
	mustProvision(t, manager, accountID, old)

	if _, err := manager.Begin(accountID, old, time.Hour); !errors.Is(err, ErrDuplicateChannel) {
		t.Fatalf("same-channel Begin = %v, want ErrDuplicateChannel", err)
	}
	if _, err := manager.Begin(accountID, Channel{ID: "", Kind: "email"}, time.Hour); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("invalid channel Begin = %v, want ErrInvalidChannel", err)
	}
	if _, err := manager.Begin(accountID, Channel{ID: "x@y", Kind: "email"}, 0); !errors.Is(err, ErrExpired) {
		t.Fatalf("zero ttl Begin = %v, want ErrExpired", err)
	}
	if _, err := manager.Begin("unprovisioned", Channel{ID: "x@y", Kind: "email"}, time.Hour); !errors.Is(err, ErrNoActiveChannel) {
		t.Fatalf("unprovisioned Begin = %v, want ErrNoActiveChannel", err)
	}

	active, _ := manager.ActiveChannel(accountID)
	if !active.equal(old) {
		t.Fatalf("failed Begin calls mutated state: active=%v", active)
	}
	if _, err := manager.Pending(accountID); !errors.Is(err, ErrNoPendingChange) {
		t.Fatalf("failed Begin created a pending change: %v", err)
	}
}

// TestConcurrentAccess hammers one account from many goroutines. At every
// moment exactly one channel holds recovery power and all committed swaps
// have both confirmations.
func TestConcurrentAccess(t *testing.T) {
	manager, _ := newScenarioManager(t)
	accountID := "acct-concurrent"
	old := Channel{ID: "old@example.test", Kind: "email"}
	mustProvision(t, manager, accountID, old)

	targets := []Channel{
		{ID: "a@example.test", Kind: "email"},
		{ID: "b@example.test", Kind: "email"},
		{ID: "c@example.test", Kind: "email"},
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			target := targets[worker%len(targets)]
			for iteration := 0; iteration < 25; iteration++ {
				view, err := manager.Begin(accountID, target, time.Hour)
				if err != nil {
					continue
				}
				oldToken, _, oldErr := manager.SendChallenge(accountID, view.ID, RoleOld)
				newToken, _, newErr := manager.SendChallenge(accountID, view.ID, RoleNew)
				if oldErr != nil || newErr != nil {
					continue
				}
				// Randomize ordering between workers by alternating roles.
				if worker%2 == 0 {
					_, _ = manager.Confirm(accountID, view.ID, RoleOld, oldToken)
					_, _ = manager.Confirm(accountID, view.ID, RoleNew, newToken)
				} else {
					_, _ = manager.Confirm(accountID, view.ID, RoleNew, newToken)
					_, _ = manager.Confirm(accountID, view.ID, RoleOld, oldToken)
				}

				active, activeErr := manager.ActiveChannel(accountID)
				if activeErr != nil || !active.valid() {
					t.Errorf("indefinite active channel during concurrency: %v", activeErr)
					return
				}
				if iteration%5 == 0 {
					_ = manager.Revoke(accountID, view.ID)
				}
			}
		}(worker)
	}
	wg.Wait()

	active, err := manager.ActiveChannel(accountID)
	if err != nil || !active.valid() {
		t.Fatalf("final active channel invalid: %v err=%v", active, err)
	}
	history, err := manager.History(accountID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	committed := 0
	for _, view := range history {
		if view.Status == StatusCommitted {
			committed++
			if !view.OldConfirmed || !view.NewConfirmed {
				t.Fatalf("committed change %s missing a confirmation", view.ID)
			}
			if view.New.equal(old) {
				t.Fatalf("committed change never moved off the original channel")
			}
		}
	}
	if committed == 0 {
		t.Fatalf("expected at least one committed swap under concurrent load")
	}
	if !active.equal(old) && !isKnownTarget(active, targets) {
		t.Fatalf("final active channel is not a known recovery endpoint: %v", active)
	}
}

func isKnownTarget(channel Channel, targets []Channel) bool {
	for _, target := range targets {
		if channel.equal(target) {
			return true
		}
	}
	return false
}
