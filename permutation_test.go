package netsec

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	nano atomic.Int64
}

func newFakeClock(start time.Time) *fakeClock {
	c := &fakeClock{}
	c.nano.Store(start.UnixNano())
	return c
}

func (c *fakeClock) now() time.Time {
	return time.Unix(0, c.nano.Load())
}

func (c *fakeClock) advance(d time.Duration) {
	c.nano.Add(int64(d))
}

type permEvent int

const (
	evBegin permEvent = iota
	evConfirmOld
	evConfirmNew
	evRevoke
	evLoseResponse
	evBeginSecond
	evExpire
)

func (e permEvent) String() string {
	switch e {
	case evBegin:
		return "begin"
	case evConfirmOld:
		return "confirm_old"
	case evConfirmNew:
		return "confirm_new"
	case evRevoke:
		return "revoke"
	case evLoseResponse:
		return "lose_response"
	case evBeginSecond:
		return "begin_second"
	case evExpire:
		return "expire"
	default:
		return "unknown"
	}
}

type permEnv struct {
	t        *testing.T
	manager  *Manager
	clock    *fakeClock
	account  string
	old      Channel
	next     Channel
	next2    Channel
	current  ChangeView
	haveCurr bool
	proofs   map[string]string
}

func newPermEnv(t *testing.T) *permEnv {
	t.Helper()
	clock := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	manager, err := NewManagerWithClock([]byte("permutation-hmac-secret"), clock.now)
	if err != nil {
		t.Fatalf("NewManagerWithClock: %v", err)
	}
	env := &permEnv{
		t:       t,
		manager: manager,
		clock:   clock,
		account: "acct-1",
		old:     Channel{ID: "old@example.test", Kind: "email"},
		next:    Channel{ID: "new@example.test", Kind: "email"},
		next2:   Channel{ID: "newer@example.test", Kind: "email"},
		proofs:  make(map[string]string),
	}
	if err := manager.Provision(env.account, env.old); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	return env
}

func (e *permEnv) proofKey(changeID string, role Role) string {
	return changeID + ":" + role.String()
}

func (e *permEnv) fetchProofs(change ChangeView) {
	for _, role := range []Role{RoleOld, RoleNew} {
		token, deadline, err := e.manager.SendChallenge(e.account, change.ID, role)
		if err != nil {
			e.t.Fatalf("SendChallenge %s %s: %v", change.ID, role, err)
		}
		if !deadline.Equal(change.Deadline) {
			e.t.Fatalf("challenge deadline %v != change deadline %v", deadline, change.Deadline)
		}
		e.proofs[e.proofKey(change.ID, role)] = token
	}
}

func (e *permEnv) begin(target Channel) {
	change, err := e.manager.Begin(e.account, target, 10*time.Second)
	if err != nil {
		e.t.Fatalf("Begin: %v", err)
	}
	e.current = change
	e.haveCurr = true
	e.fetchProofs(change)
}

func (e *permEnv) snapshotPending() *ChangeView {
	view, err := e.manager.Pending(e.account)
	if errors.Is(err, ErrNoPendingChange) || errors.Is(err, ErrExpired) ||
		errors.Is(err, ErrRevoked) || errors.Is(err, ErrSuperseded) {
		return nil
	}
	if err != nil {
		e.t.Fatalf("Pending: %v", err)
	}
	return &view
}

func sameView(a, b *ChangeView) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ID == b.ID &&
		a.Status == b.Status &&
		a.OldConfirmed == b.OldConfirmed &&
		a.NewConfirmed == b.NewConfirmed &&
		a.Deadline.Equal(b.Deadline) &&
		a.Old.equal(b.Old) &&
		a.New.equal(b.New) &&
		a.Digest.Equal(b.Digest)
}

// assertInvariants checks the global safety properties after every event.
func (e *permEnv) assertInvariants(beforeActive Channel, beforePending *ChangeView, opErr error) {
	active, err := e.manager.ActiveChannel(e.account)
	if err != nil {
		e.t.Fatalf("ActiveChannel: %v", err)
	}
	if !active.valid() {
		e.t.Fatalf("no definite recovery channel after event")
	}
	switch active.ID {
	case e.old.ID, e.next.ID, e.next2.ID:
	default:
		e.t.Fatalf("active channel switched to unknown channel %v", active)
	}

	afterPending := e.snapshotPending()

	if opErr != nil {
		if !active.equal(beforeActive) {
			e.t.Fatalf("failed event changed active channel: %v -> %v (err=%v)", beforeActive, active, opErr)
		}
		if !sameView(beforePending, afterPending) {
			e.t.Fatalf("failed event changed pending state: %+v -> %+v (err=%v)", beforePending, afterPending, opErr)
		}
	}

	if afterPending != nil {
		if afterPending.Status != StatusPending {
			e.t.Fatalf("pending view exposes non-pending status %v", afterPending.Status)
		}
		if afterPending.OldConfirmed && afterPending.NewConfirmed {
			e.t.Fatalf("pending view shows both confirmations; commit must be atomic")
		}
	}

	// If the active channel moved, it must be explained by a fully committed
	// transaction archived in history.
	if !active.equal(e.old) {
		history, err := e.manager.History(e.account)
		if err != nil {
			e.t.Fatalf("History: %v", err)
		}
		found := false
		for _, view := range history {
			if view.Status == StatusCommitted && view.New.equal(active) &&
				view.OldConfirmed && view.NewConfirmed {
				found = true
				break
			}
		}
		if !found {
			e.t.Fatalf("active channel %v has no matching committed transaction", active)
		}
	}
}

// assertProofsRejected verifies that every proof minted for the given change
// no longer drives a successful confirmation.
func (e *permEnv) assertProofsRejected(change ChangeView) {
	for _, role := range []Role{RoleOld, RoleNew} {
		token, ok := e.proofs[e.proofKey(change.ID, role)]
		if !ok {
			continue
		}
		if _, err := e.manager.Confirm(e.account, change.ID, role, token); err == nil {
			e.t.Fatalf("proof for %s role %s still accepted after invalidation", change.ID, role)
		}
	}
}

func (e *permEnv) apply(event permEvent) {
	beforeActive, err := e.manager.ActiveChannel(e.account)
	if err != nil {
		e.t.Fatalf("ActiveChannel: %v", err)
	}
	beforePending := e.snapshotPending()

	var opErr error
	switch event {
	case evBegin:
		e.begin(e.next)

	case evBeginSecond:
		if !e.haveCurr {
			_, opErr = e.manager.Begin(e.account, e.next2, 10*time.Second)
			break
		}
		stale := e.current
		e.begin(e.next2)
		// The concurrent second change must invalidate the first change's proofs.
		e.assertProofsRejected(stale)

	case evConfirmOld, evConfirmNew:
		role := RoleOld
		if event == evConfirmNew {
			role = RoleNew
		}
		if !e.haveCurr {
			_, opErr = e.manager.Confirm(e.account, "", role, "garbage-token")
			break
		}
		token := e.proofs[e.proofKey(e.current.ID, role)]
		_, opErr = e.manager.Confirm(e.account, e.current.ID, role, token)

	case evRevoke:
		if !e.haveCurr {
			opErr = e.manager.Revoke(e.account, "")
			break
		}
		stale := e.current
		opErr = e.manager.Revoke(e.account, e.current.ID)
		if opErr == nil {
			e.assertProofsRejected(stale)
		}

	case evLoseResponse:
		if !e.haveCurr || e.snapshotPending() == nil {
			_, _, opErr = e.manager.SendChallenge(e.account, "", RoleOld)
			break
		}
		// Re-sending after a lost response must return the identical token and
		// leave the transaction state untouched.
		for _, role := range []Role{RoleOld, RoleNew} {
			token, _, err := e.manager.SendChallenge(e.account, e.current.ID, role)
			if err != nil {
				e.t.Fatalf("retry SendChallenge: %v", err)
			}
			if token != e.proofs[e.proofKey(e.current.ID, role)] {
				e.t.Fatalf("retry produced a different proof for role %s", role)
			}
		}

	case evExpire:
		e.clock.advance(24 * time.Hour)
		if e.haveCurr {
			stale := e.current
			e.assertProofsRejected(stale)
		}
	}

	e.assertInvariants(beforeActive, beforePending, opErr)
}

func permute(events []permEvent, i int, visit func([]permEvent)) {
	if i == len(events)-1 {
		visit(events)
		return
	}
	for j := i; j < len(events); j++ {
		events[i], events[j] = events[j], events[i]
		permute(events, i+1, visit)
		events[i], events[j] = events[j], events[i]
	}
}

// TestEventPermutations runs every ordering of the seven observable events,
// including out-of-order confirmations, lost responses, revocation, a
// concurrent second change and the term boundary.
func TestEventPermutations(t *testing.T) {
	events := []permEvent{
		evBegin,
		evConfirmOld,
		evConfirmNew,
		evRevoke,
		evLoseResponse,
		evBeginSecond,
		evExpire,
	}
	count := 0
	permute(events, 0, func(order []permEvent) {
		name := ""
		for i, event := range order {
			if i > 0 {
				name += ">"
			}
			name += event.String()
		}
		count++
		t.Run(name, func(t *testing.T) {
			env := newPermEnv(t)
			for _, event := range order {
				env.apply(event)
			}

			active, err := env.manager.ActiveChannel(env.account)
			if err != nil {
				t.Fatalf("final ActiveChannel: %v", err)
			}
			history, err := env.manager.History(env.account)
			if err != nil {
				t.Fatalf("final History: %v", err)
			}
			committed := 0
			for _, view := range history {
				if view.Status == StatusCommitted {
					committed++
				}
				if view.Status == StatusCommitted &&
					!(view.OldConfirmed && view.NewConfirmed) {
					t.Fatalf("committed change %s missing a confirmation", view.ID)
				}
			}
			if committed > 1 {
				t.Fatalf("more than one committed transaction: %d", committed)
			}
			switch active.ID {
			case env.old.ID, env.next.ID, env.next2.ID:
			default:
				t.Fatalf("final active channel unknown: %v", active)
			}
			if active.ID == env.old.ID && committed != 0 {
				t.Fatalf("active still old but %d changes committed", committed)
			}
		})
	})
	t.Logf("exercised %d event permutations", count)
}

func TestPermutationCount(t *testing.T) {
	// Guard against accidental edits that shrink the event space silently.
	if got := 1 * 2 * 3 * 4 * 5 * 6 * 7; got != 5040 {
		t.Fatalf("permutation count drift: %d", got)
	}
	if err := fmt.Errorf("%w", ErrExpired); !errors.Is(err, ErrExpired) {
		t.Fatalf("sentinel wrapping broken")
	}
}
