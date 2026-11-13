package netsec

import (
	"context"
	"fmt"
	"testing"
)

// Each mutating Service operation is a single linearization point. The model
// below enumerates every ordering of those points for the dangerous set:
// refresh with a pre-reset token, completion of a pre-reset login (including a
// refresh of that login's family), commit of reset A, and commit of reset B
// whose proof was verified before A committed. BFS returns the shortest
// schedule that lets old proof or an old token issue a new session after a
// reset linearization point.

type modelSetup struct {
	clock     *fakeClock
	store     *MemoryStore
	signer    *TokenSigner
	accountID AccountID
	oldPair   TokenPair
	oldLogin  LoginID
	resetA    *ResetTransaction
	resetB    *ResetTransaction
	prepA     *PreparedPassword
	prepB     *PreparedPassword
}

func newModelSetup(t *testing.T) *modelSetup {
	t.Helper()
	clock := newFakeClock()
	store := NewMemoryStore()
	signer := testSigner(t)
	service := newTestService(t, store, clock, nil, signer)
	createTestAccount(t, service, "alice", emailPolicy())
	pair := login(t, service, "alice")
	pendingLogin, err := service.BeginLogin(context.Background(), BeginLoginRequest{
		AccountID: "alice",
		Password:  testOldPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &modelSetup{
		clock:     clock,
		store:     store,
		signer:    signer,
		accountID: "alice",
		oldPair:   pair,
		oldLogin:  pendingLogin.ID,
		resetA:    beginReset(t, service, "alice", "model-reset-a", "email"),
		resetB:    beginReset(t, service, "alice", "model-reset-b", "email+totp"),
		prepA:     prepare(t, service, "model-password-aaa"),
		prepB:     prepare(t, service, "model-password-bbb"),
	}
}

const (
	modelRefreshOld = iota
	modelRefreshRotated
	modelCompleteLogin
	modelRefreshCompletedLogin
	modelCommitA
	modelCommitB
	modelOpCount
)

var modelOpNames = [modelOpCount]string{
	"refresh-old",
	"refresh-rotated",
	"complete-old-login",
	"refresh-completed-login",
	"commit-reset-A",
	"commit-reset-B",
}

type modelWorld struct {
	setup     *modelSetup
	service   *Service
	rotated   *TokenPair
	loginPair *TokenPair
	done      [modelOpCount]bool
	resetDone bool
	resetBy   string
	violation string
}

func (w *modelWorld) fork(t *testing.T) *modelWorld {
	t.Helper()
	// The fork must start from the current branch, not the setup snapshot.
	cloned := w.service.Store().clone()
	next := *w
	next.service = newTestService(t, cloned, w.setup.clock, nil, w.setup.signer)
	return &next
}

func (w *modelWorld) enabled(op int) bool {
	if w.done[op] {
		return false
	}
	if op == modelRefreshRotated {
		return w.rotated != nil
	}
	if op == modelRefreshCompletedLogin {
		return w.loginPair != nil
	}
	return true
}

func (w *modelWorld) run(t *testing.T, op int) string {
	t.Helper()
	ctx := context.Background()
	switch op {
	case modelRefreshOld:
		pair, err := w.service.RefreshTokens(ctx, w.setup.oldPair.RefreshToken)
		if err == nil {
			w.rotated = &pair
			if w.resetDone {
				w.violation = fmt.Sprintf("old refresh token issued generation %d after reset %s at epoch %d",
					pair.Generation, w.resetBy, pair.SessionEpoch)
			}
		}
		return errString(err)
	case modelRefreshRotated:
		pair, err := w.service.RefreshTokens(ctx, w.rotated.RefreshToken)
		if err == nil && w.resetDone {
			w.violation = fmt.Sprintf("pre-reset rotated refresh token issued generation %d after reset %s",
				pair.Generation, w.resetBy)
		}
		return errString(err)
	case modelCompleteLogin:
		pair, err := w.service.CompleteLogin(ctx, w.setup.oldLogin)
		if err == nil {
			w.loginPair = &pair
			if w.resetDone {
				w.violation = fmt.Sprintf("pre-reset login transaction issued family %s after reset %s",
					pair.FamilyID, w.resetBy)
			}
		}
		return errString(err)
	case modelRefreshCompletedLogin:
		pair, err := w.service.RefreshTokens(ctx, w.loginPair.RefreshToken)
		if err == nil && w.resetDone {
			w.violation = fmt.Sprintf("refresh token from pre-reset login family %s issued generation %d after reset %s",
				pair.FamilyID, pair.Generation, w.resetBy)
		}
		return errString(err)
	case modelCommitA:
		return w.commitReset(t, "A", w.setup.resetA, w.setup.prepA)
	case modelCommitB:
		return w.commitReset(t, "B", w.setup.resetB, w.setup.prepB)
	default:
		t.Fatalf("unknown model op %d", op)
		return ""
	}
}

func (w *modelWorld) commitReset(t *testing.T, name string, transaction *ResetTransaction, prepared *PreparedPassword) string {
	t.Helper()
	receipt, err := w.service.CommitReset(context.Background(), transaction, prepared)
	if err == nil {
		if w.resetDone {
			w.violation = fmt.Sprintf("old proof for reset %s committed after reset %s (session epoch %d)",
				name, w.resetBy, receipt.SessionEpoch)
		}
		w.resetDone = true
		w.resetBy = name
		return fmt.Sprintf("committed se=%d ce=%d", receipt.SessionEpoch, receipt.CredentialEpoch)
	}
	return errString(err)
}

func errString(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

type modelNode struct {
	world *modelWorld
	trace []string
}

func searchStaleSessionTrace(t *testing.T, setup *modelSetup) (trace []string, nodes, leaves int) {
	t.Helper()
	root := &modelWorld{
		setup:   setup,
		service: newTestService(t, setup.store.clone(), setup.clock, nil, setup.signer),
	}
	queue := []modelNode{{world: root}}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		nodes++
		expanded := 0
		for op := 0; op < modelOpCount; op++ {
			if !node.world.enabled(op) {
				continue
			}
			expanded++
			next := node.world.fork(t)
			result := next.run(t, op)
			next.done[op] = true
			nextTrace := append(append([]string(nil), node.trace...), fmt.Sprintf("%s => %s", modelOpNames[op], result))
			if next.violation != "" {
				return append(nextTrace, "VIOLATION: "+next.violation), nodes, leaves
			}
			queue = append(queue, modelNode{world: next, trace: nextTrace})
		}
		if expanded == 0 {
			leaves++
		}
	}
	return nil, nodes, leaves
}

func TestDeterministicInterleavingsFindNoStaleSessionAfterReset(t *testing.T) {
	setup := newModelSetup(t)
	trace, nodes, leaves := searchStaleSessionTrace(t, setup)
	if trace != nil {
		t.Fatalf("shortest stale-session trace found:\n  %v", trace)
	}
	if nodes < 30 || leaves == 0 {
		t.Fatalf("model search was unexpectedly small: nodes=%d leaves=%d", nodes, leaves)
	}
	t.Logf("explored %d nodes / %d terminal schedules; no stale proof or token issued a session after reset", nodes, leaves)
}

// The next test checks the search itself on a deliberately broken abstract
// implementation. The secure transition never lets refresh succeed after a
// reset, while the broken transition forgets the epoch check. BFS must report
// the minimal two-step trace for the broken variant and nothing for the secure
// one.
func TestModelSearchFindsShortestTraceInDeliberatelyBrokenRefresh(t *testing.T) {
	secure := shortestAbstractTrace(false)
	if secure != nil {
		t.Fatalf("secure abstract model produced trace %v", secure)
	}
	broken := shortestAbstractTrace(true)
	want := []string{"commit-reset", "refresh-old"}
	if fmt.Sprint(broken) != fmt.Sprint(want) {
		t.Fatalf("broken trace = %v, want shortest %v", broken, want)
	}
}

func shortestAbstractTrace(broken bool) []string {
	type state struct {
		done  [2]bool
		reset bool
		trace []string
	}
	queue := []state{{}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for op := 0; op < 2; op++ {
			if current.done[op] {
				continue
			}
			next := current
			next.done[op] = true
			switch op {
			case 0:
				next.reset = true
				next.trace = append(append([]string(nil), current.trace...), "commit-reset")
			case 1:
				if next.reset && !broken {
					next.trace = append(append([]string(nil), current.trace...), "refresh-old-rejected")
					continue
				}
				next.trace = append(append([]string(nil), current.trace...), "refresh-old")
				if next.reset {
					return next.trace
				}
			}
			queue = append(queue, next)
		}
	}
	return nil
}
