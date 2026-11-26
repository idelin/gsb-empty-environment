package plancache_test

import (
	"errors"
	"testing"

	"formal.local/w3-bc80ec5616/plancache"
)

// TestCacheHitAndReuse: an unchanged environment yields a real cache hit and
// the cached plan executes with observable operator callbacks.
func TestCacheHitAndReuse(t *testing.T) {
	f := newFixture(t)
	var calls int
	factory := countingFactory(&calls)
	resolve := func() (plancache.CompileInput, error) {
		return baselineInput(alice, "roles:alice:v1"), nil
	}

	tx, err := f.exec.Begin(alice)
	if err != nil {
		t.Fatal(err)
	}
	plan1, h1, err := f.exec.Prepare(tx, resolve, factory)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if err := f.exec.BindAndExecute(tx, h1, plan1, []any{int64(7)}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	plan2, h2, err := f.exec.Prepare(tx, resolve, factory)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if plan1.ID != plan2.ID {
		t.Fatalf("expected cache hit reusing plan %s, got new plan %s", plan1.ID, plan2.ID)
	}
	if calls != 1 {
		t.Fatalf("expected one compilation, got %d", calls)
	}
	if err := f.exec.BindAndExecute(tx, h2, plan2, []any{int64(8)}); err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if f.obs.OperatorCount() != 2 {
		t.Fatalf("expected 2 operator callbacks, got %d", f.obs.OperatorCount())
	}
}

// TestImmediateRevokeWaitingPlan: an enqueued plan must fail with zero
// operator callbacks when an immediate REVOKE lands while it is waiting.
func TestImmediateRevokeWaitingPlan(t *testing.T) {
	f := newFixture(t)
	var calls int
	tx, _ := f.exec.Begin(alice)
	plan, h, err := f.exec.Prepare(tx,
		func() (plancache.CompileInput, error) { return baselineInput(alice, "roles:alice:v1"), nil },
		countingFactory(&calls))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.exec.Enqueue(tx, h, plan)
	if err != nil {
		t.Fatal(err)
	}

	f.cat.Revoke(alice, ordersO, "SELECT", true)

	err = f.exec.Dispatch(pending, []any{int64(1)})
	if !errors.Is(err, plancache.ErrImmediateInvalidation) {
		t.Fatalf("expected ErrImmediateInvalidation, got %v", err)
	}
	assertNoOperators(t, f.obs)
}

// TestImmediateRevokeBeforeExecute: even a direct (non-queued) cached plan
// is killed at the execution gate after an immediate revoke.
func TestImmediateRevokeBeforeExecute(t *testing.T) {
	f := newFixture(t)
	tx, _ := f.exec.Begin(alice)
	var calls int
	plan, h, err := f.exec.Prepare(tx,
		func() (plancache.CompileInput, error) { return baselineInput(alice, "roles:alice:v1"), nil },
		countingFactory(&calls))
	if err != nil {
		t.Fatal(err)
	}
	f.cat.Revoke(alice, ordersO, "SELECT", true)

	err = f.exec.BindAndExecute(tx, h, plan, []any{int64(1)})
	if !errors.Is(err, plancache.ErrImmediateInvalidation) {
		t.Fatalf("expected immediate invalidation, got %v", err)
	}
	assertNoOperators(t, f.obs)
}

// TestNonImmediateRevokeTransactionBoundary: a plain REVOKE inside a running
// transaction does not disturb its pinned snapshot; the next transaction
// sees the revocation and must recompile/deny.
func TestNonImmediateRevokeTransactionBoundary(t *testing.T) {
	f := newFixture(t)
	var calls int
	factory := countingFactory(&calls)
	resolve := func() (plancache.CompileInput, error) {
		return baselineInput(alice, "roles:alice:v1"), nil
	}

	tx1, _ := f.exec.Begin(alice)
	plan, h, err := f.exec.Prepare(tx1, resolve, factory)
	if err != nil {
		t.Fatal(err)
	}
	f.cat.Revoke(alice, ordersO, "SELECT", false)

	// Same transaction: snapshot still grants, plan still executes.
	if err := f.exec.BindAndExecute(tx1, h, plan, []any{int64(1)}); err != nil {
		t.Fatalf("pinned snapshot should survive non-immediate revoke, got %v", err)
	}

	// New transaction: privilege is gone; recompilation is rejected and no
	// operator from any plan runs.
	opsBeforeReject := f.obs.OperatorCount()
	tx2, _ := f.exec.Begin(alice)
	_, _, err = f.exec.Prepare(tx2, resolve, factory)
	if !errors.Is(err, plancache.ErrCompileRejected) {
		t.Fatalf("expected compile rejection after revoke, got %v", err)
	}
	if !errors.Is(err, plancache.ErrUnauthorized) {
		t.Fatalf("expected wrapped ErrUnauthorized, got %v", err)
	}
	if f.obs.OperatorCount() != opsBeforeReject {
		t.Fatal("rejected recompilation must not execute any operator")
	}
}

// TestImmediateRevokeBlocksRecompile: an immediate revoke hard-stops the
// transaction, so even a fresh request that would be a cache miss cannot
// recompile into an executable plan.
func TestImmediateRevokeBlocksRecompile(t *testing.T) {
	f := newFixture(t)
	var calls int
	factory := countingFactory(&calls)
	resolve := func() (plancache.CompileInput, error) {
		return baselineInput(alice, "roles:alice:v1"), nil
	}
	tx, _ := f.exec.Begin(alice)
	if _, _, err := f.exec.Prepare(tx, resolve, factory); err != nil {
		t.Fatal(err)
	}
	f.cat.Revoke(alice, ordersO, "SELECT", true)

	_, _, err := f.exec.Prepare(tx, resolve, factory)
	if !errors.Is(err, plancache.ErrImmediateInvalidation) {
		t.Fatalf("expected immediate hard stop, got %v", err)
	}

	// A later transaction starts after the fence and must simply fail
	// authorization through normal compilation, with no operator activity.
	tx2, _ := f.exec.Begin(alice)
	_, _, err = f.exec.Prepare(tx2, resolve, factory)
	if !errors.Is(err, plancache.ErrUnauthorized) {
		t.Fatalf("next transaction should be unauthorized, got %v", err)
	}
	assertNoOperators(t, f.obs)
}

// TestPrincipalDeactivated: a disabled principal loses at revalidation
// without running operators.
func TestPrincipalDeactivated(t *testing.T) {
	f := newFixture(t)
	tx, _ := f.exec.Begin(alice)
	var calls int
	plan, h, err := f.exec.Prepare(tx,
		func() (plancache.CompileInput, error) { return baselineInput(alice, "roles:alice:v1"), nil },
		countingFactory(&calls))
	if err != nil {
		t.Fatal(err)
	}
	f.cat.SetActive(alice, false)

	err = f.exec.BindAndExecute(tx, h, plan, []any{int64(1)})
	if !errors.Is(err, plancache.ErrPrincipalInactive) {
		t.Fatalf("expected ErrPrincipalInactive, got %v", err)
	}
	assertNoOperators(t, f.obs)
}

// TestRoleClosureDrift: a role change inside a new snapshot is a miss; the
// stale plan is not executed.
func TestRoleClosureDrift(t *testing.T) {
	f := newFixture(t)
	var calls int
	factory := countingFactory(&calls)
	resolve := func() (plancache.CompileInput, error) {
		return baselineInput(alice, "roles:alice:v1"), nil
	}
	tx, _ := f.exec.Begin(alice)
	if _, _, err := f.exec.Prepare(tx, resolve, factory); err != nil {
		t.Fatal(err)
	}
	f.cat.SetRoleClosure(alice, "roles:alice:v2")

	// A new transaction pins the new role closure; the cached key still
	// encodes the old digest, so the hit fails revalidation and recompilation
	// is rejected.
	tx2, _ := f.exec.Begin(alice)
	_, _, err := f.exec.Prepare(tx2, resolve, factory)
	if !errors.Is(err, plancache.ErrCompileRejected) {
		t.Fatalf("expected rejection on role drift, got %v", err)
	}
	assertNoOperators(t, f.obs)
}
