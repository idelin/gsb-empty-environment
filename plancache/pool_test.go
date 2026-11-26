package plancache_test

import (
	"errors"
	"testing"

	"formal.local/w3-bc80ec5616/plancache"
)

// TestPooledConnectionSwitchPrincipal: switching user kills every
// session-local handle and clears the SESSION cache; GLOBAL entries stay and
// are reachable only through the new principal's own full key.
func TestPooledConnectionSwitchPrincipal(t *testing.T) {
	f := newFixture(t)
	pool := plancache.NewConnectionPool(f.cat, f.obs)
	conn := pool.Acquire(alice)
	exec := conn.Executor()

	var calls int
	factory := countingFactory(&calls)
	aliceResolve := func() (plancache.CompileInput, error) {
		return baselineInput(alice, "roles:alice:v1"), nil
	}
	tx, _ := exec.Begin(alice)
	alicePlan, aliceHandle, err := exec.Prepare(tx, aliceResolve, factory)
	if err != nil {
		t.Fatal(err)
	}
	exec.InstallGlobal(alicePlan)
	if pool.GlobalCache().Len() != 1 {
		t.Fatal("global cache should hold the published plan")
	}
	if exec.SessionCache().Len() != 1 {
		t.Fatal("session cache should hold the plan")
	}

	// Switch the pooled connection to mallory.
	conn.SwitchPrincipal(mallory)
	if conn.Principal() != mallory {
		t.Fatal("principal switch did not take effect")
	}
	if exec.SessionCache().Len() != 0 {
		t.Fatal("session cache must be cleared on principal switch")
	}
	if pool.GlobalCache().Len() != 1 {
		t.Fatal("global cache must survive principal switch")
	}

	// The old session handle is dead.
	txM, _ := exec.Begin(mallory)
	err = exec.BindAndExecute(txM, aliceHandle, alicePlan, []any{int64(1)})
	if !errors.Is(err, plancache.ErrHandleExpired) {
		t.Fatalf("expected ErrHandleExpired, got %v", err)
	}

	// Mallory's full key differs (caller ID), so she cannot reach alice's
	// GLOBAL entry; she gets her own fresh compilation.
	malloryResolve := func() (plancache.CompileInput, error) {
		return baselineInput(mallory, "roles:mallory:v1"), nil
	}
	mPlan, mHandle, err := exec.Prepare(txM, malloryResolve, factory)
	if err != nil {
		t.Fatalf("mallory prepare: %v", err)
	}
	if mPlan.ID == alicePlan.ID {
		t.Fatal("different principal must not reuse alice's plan")
	}
	if mPlan.Key.Hash() == alicePlan.Key.Hash() {
		t.Fatal("full keys must differ across principals")
	}
	if err := exec.BindAndExecute(txM, mHandle, mPlan, []any{int64(1)}); err != nil {
		t.Fatalf("mallory execute: %v", err)
	}

	// Switching back, alice's GLOBAL entry is reachable again via her key.
	conn.SwitchPrincipal(alice)
	exec2 := conn.Executor()
	txA, _ := exec2.Begin(alice)
	reused, hA, err := exec2.Prepare(txA, aliceResolve, factory)
	if err != nil {
		t.Fatalf("alice re-prepare: %v", err)
	}
	if reused.ID != alicePlan.ID {
		t.Fatalf("alice should hit her surviving GLOBAL plan, got %s want %s", reused.ID, alicePlan.ID)
	}
	if err := exec2.BindAndExecute(txA, hA, reused, []any{int64(1)}); err != nil {
		t.Fatalf("reused global plan execute: %v", err)
	}
}

// TestStructuredBindingRejectsBadValues: parameter values never enter plan
// identity, but execution enforces structured, typed bindings and runs no
// operator on a mismatch.
func TestStructuredBindingRejectsBadValues(t *testing.T) {
	f := newFixture(t)
	var calls int
	tx, _ := f.exec.Begin(alice)
	plan, h, err := f.exec.Prepare(tx,
		func() (plancache.CompileInput, error) { return baselineInput(alice, "roles:alice:v1"), nil },
		countingFactory(&calls))
	if err != nil {
		t.Fatal(err)
	}

	if err := f.exec.BindAndExecute(tx, h, plan, []any{"not-an-int"}); !errors.Is(err, plancache.ErrBinding) {
		t.Fatalf("type mismatch should be ErrBinding, got %v", err)
	}
	if err := f.exec.BindAndExecute(tx, h, plan, []any{int64(1), int64(2)}); !errors.Is(err, plancache.ErrBinding) {
		t.Fatalf("arity mismatch should be ErrBinding, got %v", err)
	}
	if err := f.exec.BindAndExecute(tx, h, plan, []any{int64(1)}); err != nil {
		t.Fatalf("valid binding should execute, got %v", err)
	}
	if f.obs.OperatorCount() != 1 {
		t.Fatalf("only the valid binding should run an operator, got %d", f.obs.OperatorCount())
	}

	// Same plan with different values: identity unchanged, one compilation.
	if err := f.exec.BindAndExecute(tx, h, plan, []any{int64(999)}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("parameter values must not trigger recompilation, got %d compiles", calls)
	}
}
