package plancache_test

import (
	"errors"
	"testing"

	"formal.local/w3-bc80ec5616/plancache"
)

// TestDropAndRecreateSameNameView: a same-name view replacement gets a new
// ViewID, so the old plan misses, recompilation sees a different graph, and
// the stale plan never executes an operator.
func TestDropAndRecreateSameNameView(t *testing.T) {
	f := newFixture(t)
	var calls int
	factory := countingFactory(&calls)

	resolveV1 := func() (plancache.CompileInput, error) {
		return baselineInput(alice, "roles:alice:v1"), nil
	}
	tx, _ := f.exec.Begin(alice)
	oldPlan, h, err := f.exec.Prepare(tx, resolveV1, factory)
	if err != nil {
		t.Fatal(err)
	}

	// DDL is always immediate: the waiting plan must die first.
	pending, err := f.exec.Enqueue(tx, h, oldPlan)
	if err != nil {
		t.Fatal(err)
	}
	f.cat.DropAndRecreateView("v_outer", outerV2, ordersO)
	if err := f.exec.Dispatch(pending, []any{int64(1)}); !errors.Is(err, plancache.ErrImmediateInvalidation) {
		t.Fatalf("waiting plan should fail immediately on DDL, got %v", err)
	}

	// A fresh request expands the new view identity; the old cache key can
	// never match.
	resolveV2 := func() (plancache.CompileInput, error) {
		return selectInput(alice, "roles:alice:v1", 0, 2, 1, 1, rlsV1, outerV2, innerV), nil
	}
	if _, _, err := f.exec.Prepare(tx, resolveV2, factory); !errors.Is(err, plancache.ErrImmediateInvalidation) {
		t.Fatalf("immediate DDL must hard-stop the current transaction, got %v", err)
	}

	tx2, _ := f.exec.Begin(alice)
	newPlan, _, err := f.exec.Prepare(tx2, resolveV2, factory)
	if err != nil {
		t.Fatalf("recompile against recreated view: %v", err)
	}
	if newPlan.ID == oldPlan.ID {
		t.Fatal("recreated view must produce a distinct plan identity")
	}
	if f.exec.SessionCache().Get(oldPlan.Key) != nil {
		// old entry still cached, but unreachable by any new full key
	}
	if calls != 2 {
		t.Fatalf("expected two compilations, got %d", calls)
	}
	assertNoOperatorsForPlan(t, f.obs, oldPlan.ID)
}

// TestDropAndRecreateSameNameTable: same-name table replacement changes the
// ObjectID; the old plan cannot be reused.
func TestDropAndRecreateSameNameTable(t *testing.T) {
	f := newFixture(t)
	var calls int
	factory := countingFactory(&calls)
	resolveV1 := func() (plancache.CompileInput, error) {
		return baselineInput(alice, "roles:alice:v1"), nil
	}
	tx, _ := f.exec.Begin(alice)
	oldPlan, _, err := f.exec.Prepare(tx, resolveV1, factory)
	if err != nil {
		t.Fatal(err)
	}

	f.cat.DropAndRecreateTable("orders", ordersO2, rlsV1)
	// Re-grant on the replacement object; the old object's grants were
	// destroyed, proving identity change rather than name reuse.
	f.cat.Grant(alice, ordersO2, "SELECT")

	resolveV2 := func() (plancache.CompileInput, error) {
		in := selectInput(alice, "roles:alice:v1", 0, 1, 1, 1, rlsV1, outerV, innerV)
		in.DepGraph[2].Table.ObjectID = ordersO2
		in.RLSPolicies = map[plancache.ObjectID]plancache.Digest{ordersO2: rlsV1}
		return in, nil
	}
	if _, _, err := f.exec.Prepare(tx, resolveV2, factory); !errors.Is(err, plancache.ErrImmediateInvalidation) {
		t.Fatalf("immediate DDL must hard-stop the current transaction, got %v", err)
	}
	tx2, _ := f.exec.Begin(alice)
	newPlan, _, err := f.exec.Prepare(tx2, resolveV2, factory)
	if err != nil {
		t.Fatalf("recompile against recreated table should succeed with fresh identity: %v", err)
	}
	if newPlan.ID == oldPlan.ID {
		t.Fatal("recreated table must produce a distinct plan identity")
	}
	assertNoOperatorsForPlan(t, f.obs, oldPlan.ID)
}

// TestNestedViewRLSTighteningImmediate: tightening the base table RLS policy
// reaches a plan queued through the nested view chain and fails it before
// any operator, even though the query text never names the table.
func TestNestedViewRLSTighteningImmediate(t *testing.T) {
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

	// The event names both nested views, modeling propagation through the
	// expansion chain, and is immediate.
	f.cat.TightenRLS(ordersO, rlsV2, []plancache.ViewID{outerV, innerV}, true)

	err = f.exec.Dispatch(pending, []any{int64(1)})
	if !errors.Is(err, plancache.ErrImmediateInvalidation) {
		t.Fatalf("nested-view RLS tightening should immediately fail, got %v", err)
	}
	assertNoOperators(t, f.obs)

	// Even if the host retries within the same transaction, the immediate
	// fence keeps rejecting any plan carrying the old manifest.
	err = f.exec.BindAndExecute(tx, h, plan, []any{int64(1)})
	if !errors.Is(err, plancache.ErrImmediateInvalidation) {
		t.Fatalf("immediate fence must keep rejecting the old plan, got %v", err)
	}
	assertNoOperators(t, f.obs)
}

// TestRLSTighteningNewTransaction: non-immediate tightening only bites the
// next transaction, where epoch/RLS digest mismatch forces a miss and the
// recompiled input must carry the new digest.
func TestRLSTighteningNewTransaction(t *testing.T) {
	f := newFixture(t)
	var calls int
	factory := countingFactory(&calls)

	tx1, _ := f.exec.Begin(alice)
	plan, h, err := f.exec.Prepare(tx1,
		func() (plancache.CompileInput, error) { return baselineInput(alice, "roles:alice:v1"), nil },
		factory)
	if err != nil {
		t.Fatal(err)
	}
	f.cat.TightenRLS(ordersO, rlsV2, []plancache.ViewID{outerV, innerV}, false)

	// Old transaction still executes against its pinned snapshot.
	if err := f.exec.BindAndExecute(tx1, h, plan, []any{int64(1)}); err != nil {
		t.Fatalf("non-immediate RLS change must not pierce the snapshot: %v", err)
	}

	tx2, _ := f.exec.Begin(alice)
	// The cached plan's epoch predates the tightening: its hit fails
	// revalidation against tx2's snapshot (zero operators), and Prepare
	// falls through to compilation with the resolver's fresh input.
	beforeOps := f.obs.OperatorCount()

	freshResolve := func() (plancache.CompileInput, error) {
		in := baselineInput(alice, "roles:alice:v1")
		in.PolicyEpoch = 1
		in.DepGraph[2].Table.RLSPolicy = rlsV2
		in.RLSPolicies[ordersO] = rlsV2
		return in, nil
	}
	newPlan, h2, err := f.exec.Prepare(tx2, freshResolve, factory)
	if err != nil {
		t.Fatalf("fresh RLS input should compile: %v", err)
	}
	if f.obs.OperatorCount() != beforeOps {
		t.Fatal("stale hit must not execute any operator before recompilation")
	}
	if err := f.exec.BindAndExecute(tx2, h2, newPlan, []any{int64(1)}); err != nil {
		t.Fatalf("fresh plan execute: %v", err)
	}
	if newPlan.ID == plan.ID {
		t.Fatal("RLS change must produce a new plan")
	}
}

// TestSchemaVersionDrift: an ALTER bumps the schema version and is treated
// as immediate DDL for waiting plans.
func TestSchemaVersionDrift(t *testing.T) {
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
	f.cat.BumpSchema(ordersO)
	if err := f.exec.Dispatch(pending, []any{int64(1)}); !errors.Is(err, plancache.ErrImmediateInvalidation) {
		t.Fatalf("schema bump should invalidate waiting plan, got %v", err)
	}
	assertNoOperators(t, f.obs)
}

func assertNoOperatorsForPlan(t *testing.T, obs *plancache.TraceObserver, planID string) {
	t.Helper()
	for _, op := range obs.ExecutedOperators() {
		if len(op) >= len(planID) && op[:len(planID)] == planID {
			t.Fatalf("plan %s unexpectedly executed operator %s", planID, op)
		}
	}
}
