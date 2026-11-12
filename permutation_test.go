package netsec

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// opKind enumerates the delivery/control events that can arrive at an identity
// host over an unreliable network.
type opKind int

const (
	opBeginA opKind = iota // open change A -> B
	opConfirmAOld
	opConfirmANew
	opBeginB // concurrent second change A -> C
	opCancelA
	opRetryAOld    // duplicate delivery (response lost)
	opExpireWindow // deadline passes
)

func (k opKind) String() string {
	names := [...]string{
		"beginA", "confirmAOld", "confirmANew",
		"beginB", "cancelA", "retryAOld", "expire",
	}
	return names[k]
}

// permEnv holds one deterministic run under a specific event ordering.
type permEnv struct {
	t       *testing.T
	manager *Manager
	store   *MemoryStore
	clock   *FakeClock
	start   time.Time

	tokensA map[opKind]string // digest-A old/new tokens
	tokensB map[opKind]string // digest-B old/new tokens
	seen    map[string]bool
}

func newPermEnv(t *testing.T) *permEnv {
	t.Helper()
	manager, store, clock := newTestManager(t)
	provisionUser(t, store, "perm", chA)
	start := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	clock.Set(start)
	return &permEnv{
		t:       t,
		manager: manager,
		store:   store,
		clock:   clock,
		start:   start,
		tokensA: map[opKind]string{},
		tokensB: map[opKind]string{},
		seen:    map[string]bool{},
	}
}

// run executes one permutation, asserting the recovery-channel invariants after
// every single step (so intermediate states are all covered).
func (e *permEnv) run(order []opKind) {
	for step, op := range order {
		e.apply(op)
		e.assertInvariants(step, op, order)
	}
	e.assertTerminalClassification(order)
	e.assertReplayFixedPoint(order)
}

// assertReplayFixedPoint drives the system with repeated replays of every
// issued proof. After the first replay round the state must be a fixed point:
// duplicate/lost-response deliveries never mutate state a second time and all
// errors belong to the documented stale-proof set.
func (e *permEnv) assertReplayFixedPoint(order []opKind) {
	e.replayAllProofs()
	snapshot, err := e.manager.State("perm")
	if err != nil {
		e.t.Fatalf("snapshot: %v", err)
	}
	e.replayAllProofs()
	again, err := e.manager.State("perm")
	if err != nil {
		e.t.Fatalf("snapshot after second replay: %v", err)
	}
	if again.Version != snapshot.Version || !again.Active.Equal(snapshot.Active) {
		e.failOrder(order, -1,
			"replay is not a fixed point: first=%+v second=%+v", snapshot, again)
	}
	if (again.Pending == nil) != (snapshot.Pending == nil) {
		e.failOrder(order, -1, "replay toggled pending presence")
	}
}

// assertTerminalClassification verifies the end state is one of a small,
// fully explained set and that expired pending proofs are correctly rejected.
func (e *permEnv) assertTerminalClassification(order []opKind) {
	state, err := e.manager.State("perm")
	if err != nil {
		e.t.Fatalf("terminal state: %v", err)
	}
	switch state.Active {
	case chA, chB, chC:
	default:
		e.failOrder(order, -1, "unexpected terminal active channel %v", state.Active)
	}

	for _, archived := range state.Past {
		switch archived.Outcome {
		case OutcomeSwitched, OutcomeCanceled, OutcomeSuperseded:
		default:
			e.failOrder(order, -1, "archive entry has no terminal outcome: %+v", archived)
		}
	}

	now := e.clock.Now().UnixNano()
	if state.Pending != nil && now >= state.Pending.ExpiresNano {
		token := e.tokenForDigest(state.Pending.Digest)
		if token != "" {
			if _, err := e.manager.Confirm("perm", token); !errors.Is(err, ErrProofExpired) {
				e.failOrder(order, -1,
					"expired pending proof = %v; want ErrProofExpired", err)
			}
		}
	}
}

func (e *permEnv) tokenForDigest(digest string) string {
	buckets := []map[opKind]string{e.tokensA, e.tokensB}
	for _, bucket := range buckets {
		for _, token := range bucket {
			if token == "" {
				continue
			}
			claims, err := e.manager.sign.parse(token)
			if err != nil {
				e.t.Fatalf("parse token: %v", err)
			}
			if claims.Digest == digest {
				return token
			}
		}
	}
	return ""
}

func (e *permEnv) apply(op opKind) {
	switch op {
	case opBeginA:
		if e.seen["beginA"] {
			return
		}
		pending, err := e.manager.BeginChange("perm", chA, chB)
		if err != nil {
			e.t.Fatalf("beginA: %v", err)
		}
		e.tokensA[opConfirmAOld] = pending.OldToken
		e.tokensA[opConfirmANew] = pending.NewToken
		e.seen["beginA"] = true

	case opBeginB:
		state, err := e.manager.State("perm")
		if err != nil {
			e.t.Fatalf("state before beginB: %v", err)
		}
		if !state.Active.Equal(chA) {
			return // B targets A as the old channel; invalid once A switched
		}
		pending, err := e.manager.BeginChange("perm", chA, chC)
		if err != nil {
			e.t.Fatalf("beginB: %v", err)
		}
		e.tokensB[opConfirmAOld] = pending.OldToken
		e.tokensB[opConfirmANew] = pending.NewToken

	case opConfirmAOld, opConfirmANew, opRetryAOld:
		token, ok := e.tokensA[opConfirmAOld]
		if op == opConfirmANew {
			token, ok = e.tokensA[opConfirmANew]
		}
		if !ok {
			return // token not issued yet: a lost early delivery
		}
		_, _ = e.manager.Confirm("perm", token)

	case opCancelA:
		_ = e.manager.CancelChange("perm")

	case opExpireWindow:
		e.clock.Set(e.start.Add(testTTL + time.Nanosecond))
	}
}

// allowedErrors are the expected rejections for stale/late/out-of-window
// deliveries; anything else is a correctness failure.
var allowedErrors = []error{
	ErrProofRevoked,
	ErrProofExpired,
	ErrAlreadyComplete,
	ErrNoPending,
}

func (e *permEnv) assertInvariants(step int, op opKind, order []opKind) {
	state, err := e.manager.State("perm")
	if err != nil {
		e.t.Fatalf("state after %s: %v", op, err)
	}

	// Invariant 1: a definite, valid recovery channel always exists.
	if !state.Active.Valid() {
		e.failOrder(order, step, "active recovery channel missing or invalid: %+v", state)
	}

	// Invariant 2: an unverified new channel is never the active channel.
	if state.Active.Equal(chB) || state.Active.Equal(chC) {
		e.requireBothConfirmed(state, order, step, op)
	}

	// Invariant 3: at most one pending transaction exists at any time.
	if state.Pending != nil {
		if !state.Active.Equal(state.Pending.Old) {
			e.failOrder(order, step, "pending old channel %v != active %v",
				state.Pending.Old, state.Active)
		}
		if state.Active.Equal(state.Pending.New) {
			e.failOrder(order, step, "unverified new channel is already active")
		}
	}

	// Invariant 4: version strictly tracks committed mutations.
	if state.Version == 0 {
		e.failOrder(order, step, "version never advanced")
	}

	// Invariant 5 (replay safety) is verified at the terminal fixed point; see
	// assertReplayFixedPoint. Replaying mid-run would steal the in-flight
	// transaction from the ordering under test.
}

func (e *permEnv) requireBothConfirmed(state *State, order []opKind, step int, op opKind) {
	if state.Pending != nil {
		e.failOrder(order, step, "new channel active while a transaction is still pending")
	}
	last := state.Past[len(state.Past)-1]
	if last.Outcome != OutcomeSwitched {
		e.failOrder(order, step, "new channel active without a switched archive entry: %+v", last)
	}
}

func (e *permEnv) replayAllProofs() {
	play := func(token string) {
		if token == "" {
			return
		}
		_, err := e.manager.Confirm("perm", token)
		if err != nil && !oneOfAllowed(err) {
			e.t.Fatalf("undocumented error %v on proof replay", err)
		}
	}
	play(e.tokensA[opConfirmAOld])
	play(e.tokensA[opConfirmANew])
	play(e.tokensB[opConfirmAOld])
	play(e.tokensB[opConfirmANew])
}

func oneOfAllowed(err error) bool {
	for _, candidate := range allowedErrors {
		if errors.Is(err, candidate) {
			return true
		}
	}
	return false
}

func (e *permEnv) failOrder(order []opKind, step int, format string, args ...any) {
	names := make([]string, len(order))
	for i, op := range order {
		names[i] = op.String()
	}
	e.t.Fatalf("invariant broken at step %d (%s), order %v: %s",
		step, order[step], names, fmt.Sprintf(format, args...))
}

// TestEventPermutations exhaustively runs every 7! ordering of the unreliable
// network events, including out-of-order deliveries, lost responses (ops that
// run before their tokens exist), cancellation, a concurrent second change and
// the deadline boundary.
func TestEventPermutations(t *testing.T) {
	ops := []opKind{
		opBeginA,
		opConfirmAOld,
		opConfirmANew,
		opBeginB,
		opCancelA,
		opRetryAOld,
		opExpireWindow,
	}
	count := 0
	for _, order := range permutations(ops) {
		env := newPermEnv(t)
		env.run(order)
		count++
	}
	t.Logf("exercised %d event orderings", count)
	if count != 5040 {
		t.Fatalf("permutation count = %d; want 5040", count)
	}
}

func permutations(items []opKind) [][]opKind {
	if len(items) <= 1 {
		return [][]opKind{append([]opKind(nil), items...)}
	}
	var out [][]opKind
	for i, item := range items {
		rest := make([]opKind, 0, len(items)-1)
		rest = append(rest, items[:i]...)
		rest = append(rest, items[i+1:]...)
		for _, tail := range permutations(rest) {
			out = append(out, append([]opKind{item}, tail...))
		}
	}
	return out
}
