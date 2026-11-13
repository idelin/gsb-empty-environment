package netsec_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	netsec "example.org/netsec/q0117"
)

type countingCompiler struct {
	mu    sync.Mutex
	calls int
}

func (c *countingCompiler) Compile(in netsec.CompileInput) ([]netsec.Operator, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return (netsec.DefaultCompiler{}).Compile(in)
}

func (c *countingCompiler) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type eventLog struct {
	mu     sync.Mutex
	events []netsec.Event
}

func (l *eventLog) Observe(event netsec.Event) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *eventLog) Count(typ netsec.EventType) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for _, event := range l.events {
		if event.Type == typ {
			count++
		}
	}
	return count
}

func (l *eventLog) Snapshot() []netsec.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]netsec.Event(nil), l.events...)
}

func testSnapshot() *netsec.SecuritySnapshot {
	s := netsec.NewSnapshot(1)
	s.Principals["alice"] = netsec.PrincipalState{
		Active: true,
		Grants: map[netsec.ObjectID]netsec.OperationSet{
			"t1": netsec.OpsSelect,
		},
	}
	s.Objects["t1"] = netsec.ObjectState{
		ObjectID:      "t1",
		Name:          "users",
		SchemaVersion: 1,
		RLSDigest:     "none",
	}
	return s
}

func selectQuery() netsec.Query {
	return netsec.Query{
		ASTDigest: "ast:canonical-select-users",
		ParamTypes: []netsec.ParamType{
			netsec.ParamInt,
			netsec.ParamText,
		},
		Objects: []netsec.ObjectRef{{ObjectID: "t1", Operations: netsec.OpsSelect}},
	}
}

func request(params netsec.BoundParameters) netsec.ExecutionRequest {
	return netsec.ExecutionRequest{Query: selectQuery(), Params: params}
}

func mustBind(t *testing.T, types []netsec.ParamType, values ...any) netsec.BoundParameters {
	t.Helper()
	params, err := netsec.Bind(types, values...)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	return params
}

func TestCacheKeyCoversEveryCompileInput(t *testing.T) {
	base := netsec.CompileInput{
		QueryASTDigest:  "ast-1",
		ParamTypes:      []netsec.ParamType{netsec.ParamInt, netsec.ParamText},
		ParamTypeDigest: netsec.DigestParamTypes([]netsec.ParamType{netsec.ParamInt, netsec.ParamText}),
		PrincipalID:     "alice",
		Roles:           []string{"reader"},
		Objects: []netsec.ObjectDependency{{
			ObjectID:      "t1",
			SchemaVersion: 1,
			Operations:    netsec.OpsSelect,
			RLSDigest:     "rls-1",
		}},
		PolicyEpoch: 10,
	}
	baseKey, err := base.Key()
	if err != nil {
		t.Fatalf("base key: %v", err)
	}

	changes := []struct {
		name   string
		change func(*netsec.CompileInput)
	}{
		{"AST digest", func(in *netsec.CompileInput) { in.QueryASTDigest = "ast-2" }},
		{"parameter types", func(in *netsec.CompileInput) {
			in.ParamTypes = []netsec.ParamType{netsec.ParamInt, netsec.ParamInt64}
		}},
		{"principal", func(in *netsec.CompileInput) { in.PrincipalID = "bob" }},
		{"role closure", func(in *netsec.CompileInput) { in.Roles = []string{"writer"} }},
		{"object identity", func(in *netsec.CompileInput) { in.Objects[0].ObjectID = "t2" }},
		{"schema version", func(in *netsec.CompileInput) { in.Objects[0].SchemaVersion = 2 }},
		{"required operations", func(in *netsec.CompileInput) { in.Objects[0].Operations |= netsec.OpsUpdate }},
		{"RLS digest", func(in *netsec.CompileInput) { in.Objects[0].RLSDigest = "rls-2" }},
		{"policy epoch", func(in *netsec.CompileInput) { in.PolicyEpoch = 11 }},
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			in := cloneInput(base)
			tc.change(&in)
			key, err := in.Key()
			if err != nil {
				t.Fatalf("key: %v", err)
			}
			if key == baseKey {
				t.Fatalf("key did not change for %s", tc.name)
			}
		})
	}

	alias := base
	alias.ASTDigest = base.QueryASTDigest
	alias.ParamTypeListDigest = base.ParamTypeDigest
	alias.ObjectDependencies = append([]netsec.ObjectDependency(nil), base.Objects...)
	aliasKey, err := alias.Key()
	if err != nil {
		t.Fatalf("alias key: %v", err)
	}
	if aliasKey != baseKey {
		t.Fatalf("consistent aliases produced different keys")
	}
}

func TestRoleGrantAndLowLevelHostCompile(t *testing.T) {
	s := netsec.NewSnapshot(4)
	s.Principals["alice"] = netsec.PrincipalState{Active: true, Roles: []string{"reader"}}
	s.Roles["reader"] = netsec.RoleState{
		Grants: map[netsec.ObjectID]netsec.OperationSet{"t1": netsec.OpsSelect},
	}
	s.Objects["t1"] = netsec.ObjectState{
		ObjectID: "t1", Name: "users", SchemaVersion: 7, RLSDigest: "rls-7",
	}
	host := netsec.NewHost(s)
	input, err := host.Snapshot().CompileInput(selectQuery(), "alice")
	if err != nil {
		t.Fatalf("compile input: %v", err)
	}
	compiler := &countingCompiler{}
	first, err := host.Compile(input, compiler)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	second, err := host.Compile(input, compiler)
	if err != nil {
		t.Fatalf("compile hit: %v", err)
	}
	if compiler.Calls() != 1 {
		t.Fatalf("host compile calls = %d, want 1", compiler.Calls())
	}
	if first.Key() != second.Key() {
		t.Fatal("low-level cache hit returned a different plan")
	}
	deps := first.Dependencies()
	if len(deps) != 1 || deps[0].SchemaVersion != 7 || deps[0].RLSDigest != "rls-7" ||
		!deps[0].Operations.Has(netsec.OpSelect) {
		t.Fatalf("unexpected dependency manifest: %+v", deps)
	}
}

func cloneInput(in netsec.CompileInput) netsec.CompileInput {
	out := in
	out.ParamTypes = append([]netsec.ParamType(nil), in.ParamTypes...)
	out.Roles = append([]string(nil), in.Roles...)
	out.Objects = append([]netsec.ObjectDependency(nil), in.Objects...)
	return out
}

func TestParameterValuesAreNotPartOfPlanIdentity(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	compiler := &countingCompiler{}
	log := &eventLog{}
	host.AddObserver(log)
	var executedParams []netsec.BoundParameters
	host.SetExecutor(netsec.ExecutorFunc(func(_ netsec.Operator, params netsec.BoundParameters) error {
		executedParams = append(executedParams, params)
		return nil
	}))

	first := mustBind(t, selectQuery().ParamTypes, 1, "first")
	second := mustBind(t, selectQuery().ParamTypes, 2, "second")

	r1, err := session.Execute(request(first), compiler)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	r2, err := session.Execute(request(second), compiler)
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if compiler.Calls() != 1 {
		t.Fatalf("compiler called %d times, want 1", compiler.Calls())
	}
	if !r2.CacheHit {
		t.Fatal("second execution did not hit the cache")
	}
	if r1.PlanKey != r2.PlanKey {
		t.Fatal("parameter values changed plan identity")
	}
	if log.Count(netsec.EventOperatorStart) != r1.OperatorsExecuted+r2.OperatorsExecuted {
		t.Fatalf("operator callbacks do not match executed operators")
	}
	if r2.OperatorsExecuted == 0 {
		t.Fatal("second execution ran no operators")
	}
	if len(executedParams) != r1.OperatorsExecuted+r2.OperatorsExecuted {
		t.Fatalf("executor calls = %d", len(executedParams))
	}
	last := executedParams[len(executedParams)-1].Values()
	if len(last) != 2 || last[0].Value != 2 || last[1].Value != "second" {
		t.Fatalf("executor did not receive the second structured bind: %+v", last)
	}
}

func TestNonImmediateRevokeOnlyAffectsNextTransaction(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	compiler := &countingCompiler{}
	log := &eventLog{}
	host.AddObserver(log)
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")

	tx1, err := session.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx1.Execute(request(params), compiler); err != nil {
		t.Fatalf("first tx execute: %v", err)
	}
	if err := host.Revoke(netsec.Revocation{
		Principal:  "alice",
		ObjectID:   "t1",
		Operations: netsec.OpsSelect,
	}); err != nil {
		t.Fatal(err)
	}
	// The transaction keeps its pinned snapshot.
	if _, err := tx1.Execute(request(params), compiler); err != nil {
		t.Fatalf("transaction should keep its pinned snapshot: %v", err)
	}
	if compiler.Calls() != 1 {
		t.Fatalf("compiler calls = %d, want 1", compiler.Calls())
	}
	if err := tx1.Commit(); err != nil {
		t.Fatal(err)
	}

	before := log.Count(netsec.EventOperatorStart)
	tx2, err := session.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx2.Execute(request(params), compiler)
	if !errors.Is(err, netsec.ErrPermissionDenied) {
		t.Fatalf("next transaction error = %v, want permission denied", err)
	}
	if got := log.Count(netsec.EventOperatorStart) - before; got != 0 {
		t.Fatalf("revoked transaction executed %d operators", got)
	}
	if compiler.Calls() != 1 {
		t.Fatalf("revoked transaction reused/compiled a plan: calls=%d", compiler.Calls())
	}
	_ = tx2.Rollback()
}

func TestImmediateRevokeFailsWaitingPlanWithZeroOperators(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	compiler := &countingCompiler{}
	log := &eventLog{}
	host.AddObserver(log)
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")

	tx, err := session.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Execute(request(params), compiler); err != nil {
		t.Fatalf("warm plan: %v", err)
	}
	pending, err := tx.Prepare(request(params), compiler)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !pending.CacheHit() {
		t.Fatal("expected a reviewed cache hit")
	}
	before := log.Count(netsec.EventOperatorStart)
	if err := host.RevokeImmediate("alice", "t1", netsec.OpsSelect); err != nil {
		t.Fatal(err)
	}
	_, err = pending.Execute()
	if !errors.Is(err, netsec.ErrImmediateRevocation) {
		t.Fatalf("waiting plan error = %v, want immediate revocation", err)
	}
	if got := log.Count(netsec.EventOperatorStart) - before; got != 0 {
		t.Fatalf("fenced plan executed %d operators", got)
	}
	before = log.Count(netsec.EventOperatorStart)
	if _, err := tx.Execute(request(params), compiler); !errors.Is(err, netsec.ErrImmediateRevocation) {
		t.Fatalf("transaction error = %v", err)
	}
	if got := log.Count(netsec.EventOperatorStart) - before; got != 0 {
		t.Fatalf("fenced transaction executed %d operators", got)
	}
	_ = tx.Rollback()
}

func TestImmediateRevokeFailsNonTransactionalWaitingPlan(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")
	pending, err := session.Prepare(request(params))
	if err != nil {
		t.Fatal(err)
	}
	if err := host.RevokeImmediate("alice", "t1", netsec.OpsSelect); err != nil {
		t.Fatal(err)
	}
	_, err = pending.Execute()
	if !errors.Is(err, netsec.ErrImmediateRevocation) {
		t.Fatalf("waiting plan error = %v, want immediate revocation", err)
	}
}

func TestRevocationInjectedAtOperatorBoundaryPreventsExecutorCall(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")
	executorCalls := 0
	host.SetExecutor(netsec.ExecutorFunc(func(netsec.Operator, netsec.BoundParameters) error {
		executorCalls++
		return nil
	}))
	var once sync.Once
	host.AddObserver(netsec.ObserverFunc(func(event netsec.Event) {
		if event.Type == netsec.EventOperatorStart {
			once.Do(func() {
				_ = host.RevokeImmediate("alice", "t1", netsec.OpsSelect)
			})
		}
	}))

	_, err = session.Execute(request(params))
	if !errors.Is(err, netsec.ErrImmediateRevocation) {
		t.Fatalf("error = %v, want immediate revocation", err)
	}
	if executorCalls != 0 {
		t.Fatalf("executor ran %d operators after revocation at the operator boundary", executorCalls)
	}
}

func TestDropAndRecreateSameNameInvalidatesPlan(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	compiler := &countingCompiler{}
	log := &eventLog{}
	host.AddObserver(log)
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")

	first, err := session.Execute(request(params), compiler)
	if err != nil {
		t.Fatal(err)
	}
	if compiler.Calls() != 1 {
		t.Fatalf("calls = %d, want 1", compiler.Calls())
	}
	if err := host.DropObject("t1"); err != nil {
		t.Fatal(err)
	}
	if err := host.RecreateObject("t1", "users", "none"); err != nil {
		t.Fatal(err)
	}
	before := len(log.Snapshot())
	second, err := session.Execute(request(params), compiler)
	if err != nil {
		t.Fatal(err)
	}
	if compiler.Calls() != 2 {
		t.Fatalf("recreated object reused old plan: calls=%d", compiler.Calls())
	}
	if second.CacheHit {
		t.Fatal("recreated object was reported as a cache hit")
	}
	if first.PlanKey == second.PlanKey {
		t.Fatal("drop/recreate kept the old plan key")
	}
	for _, event := range log.Snapshot()[before:] {
		if event.Type == netsec.EventOperatorStart && event.PlanKey == first.PlanKey {
			t.Fatal("operator from the dropped object's plan executed")
		}
	}
}

func TestRLSTighteningInvalidatesPlan(t *testing.T) {
	s := testSnapshot()
	obj := s.Objects["t1"]
	obj.RLSDigest = "rls:v1"
	s.Objects["t1"] = obj
	host := netsec.NewHost(s)
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	compiler := &countingCompiler{}
	log := &eventLog{}
	host.AddObserver(log)
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")

	first, err := session.Execute(request(params), compiler)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.TightenRLS("t1", "rls:v2-tightened"); err != nil {
		t.Fatal(err)
	}
	before := len(log.Snapshot())
	second, err := session.Execute(request(params), compiler)
	if err != nil {
		t.Fatal(err)
	}
	if compiler.Calls() != 2 {
		t.Fatalf("tightened RLS reused old plan: calls=%d", compiler.Calls())
	}
	if first.PlanKey == second.PlanKey {
		t.Fatal("RLS tightening kept the old plan key")
	}
	foundNewRLS := false
	for _, event := range log.Snapshot()[before:] {
		if event.Type == netsec.EventOperatorStart && event.PlanKey == first.PlanKey {
			t.Fatal("operator from the pre-tightening plan executed")
		}
		if event.Type == netsec.EventOperatorStart && event.Operator.Kind() == netsec.OperatorRowSecurity {
			if event.Operator.Detail() == "t1:rls:v2-tightened" {
				foundNewRLS = true
			}
		}
	}
	if !foundNewRLS {
		t.Fatal("compiled replacement plan did not contain the tightened RLS primitive")
	}
}

func TestRoleClosureChangeWithSameEpochInvalidatesPlan(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	compiler := &countingCompiler{}
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")

	first, err := session.Execute(request(params), compiler)
	if err != nil {
		t.Fatal(err)
	}
	snap := host.Snapshot()
	principal := snap.Principals["alice"]
	principal.Roles = []string{"reader"}
	snap.Principals["alice"] = principal
	snap.Roles["reader"] = netsec.RoleState{Grants: map[netsec.ObjectID]netsec.OperationSet{}}
	if err := host.ReplaceSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	second, err := session.Execute(request(params), compiler)
	if err != nil {
		t.Fatal(err)
	}
	if compiler.Calls() != 2 {
		t.Fatalf("role closure change reused old plan: calls=%d", compiler.Calls())
	}
	if first.PlanKey == second.PlanKey {
		t.Fatal("role closure is not part of plan identity")
	}
}

func TestPolicyEpochChangeWithSameCatalogInvalidatesPlan(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	compiler := &countingCompiler{}
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")

	first, err := session.Execute(request(params), compiler)
	if err != nil {
		t.Fatal(err)
	}
	host.SetPolicyEpoch(99)
	second, err := session.Execute(request(params), compiler)
	if err != nil {
		t.Fatal(err)
	}
	if compiler.Calls() != 2 {
		t.Fatalf("policy epoch change reused old plan: calls=%d", compiler.Calls())
	}
	if first.PlanKey == second.PlanKey {
		t.Fatal("policy epoch is not part of plan identity")
	}
}

func TestConnectionSwitchClearsLocalButKeepsGlobalEntries(t *testing.T) {
	s := testSnapshot()
	s.Principals["bob"] = netsec.PrincipalState{
		Active: true,
		Grants: map[netsec.ObjectID]netsec.OperationSet{"t1": netsec.OpsSelect},
	}
	host := netsec.NewHost(s)
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	compiler := &countingCompiler{}
	log := &eventLog{}
	host.AddObserver(log)
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")

	aliceResult, err := session.Execute(request(params), compiler)
	if err != nil {
		t.Fatal(err)
	}
	if session.LocalPlanCount() != 1 {
		t.Fatalf("local plans = %d, want 1", session.LocalPlanCount())
	}
	if host.GlobalCache().Len() != 1 {
		t.Fatalf("global cache len = %d, want 1", host.GlobalCache().Len())
	}
	if err := session.SwitchPrincipal("bob"); err != nil {
		t.Fatal(err)
	}
	if session.LocalPlanCount() != 0 {
		t.Fatal("principal switch did not clear session-local plan handles")
	}
	if host.GlobalCache().Len() != 1 {
		t.Fatal("principal switch cleared the global cache")
	}
	before := len(log.Snapshot())
	bobResult, err := session.Execute(request(params), compiler)
	if err != nil {
		t.Fatal(err)
	}
	if compiler.Calls() != 2 {
		t.Fatalf("new principal reused another principal's key: calls=%d", compiler.Calls())
	}
	if aliceResult.PlanKey == bobResult.PlanKey {
		t.Fatal("principal is not part of plan identity")
	}
	for _, event := range log.Snapshot()[before:] {
		if event.Type == netsec.EventOperatorStart && event.PlanKey == aliceResult.PlanKey {
			t.Fatal("new principal executed the old principal's plan")
		}
	}

	if err := session.SwitchPrincipal("alice"); err != nil {
		t.Fatal(err)
	}
	if session.LocalPlanCount() != 0 {
		t.Fatal("second principal switch did not clear local handles")
	}
	back, err := session.Execute(request(params), compiler)
	if err != nil {
		t.Fatal(err)
	}
	if compiler.Calls() != 2 {
		t.Fatalf("global entry was not reusable under the original principal key: calls=%d", compiler.Calls())
	}
	if !back.CacheHit {
		t.Fatal("original principal did not hit its global entry")
	}
}

func TestPermissionRemovalWithSameEpochFailsClosed(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	compiler := &countingCompiler{}
	log := &eventLog{}
	host.AddObserver(log)
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")

	if _, err := session.Execute(request(params), compiler); err != nil {
		t.Fatal(err)
	}
	snap := host.Snapshot()
	principal := snap.Principals["alice"]
	principal.Grants = map[netsec.ObjectID]netsec.OperationSet{"t1": 0}
	snap.Principals["alice"] = principal
	if err := host.ReplaceSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	before := log.Count(netsec.EventOperatorStart)
	_, err = session.Execute(request(params), compiler)
	if !errors.Is(err, netsec.ErrPermissionDenied) {
		t.Fatalf("error = %v, want permission denied", err)
	}
	if compiler.Calls() != 1 {
		t.Fatalf("stale cached plan was recompiled/executed: calls=%d", compiler.Calls())
	}
	if got := log.Count(netsec.EventOperatorStart) - before; got != 0 {
		t.Fatalf("stale plan executed %d operators", got)
	}
}

func TestCacheHitRechecksPrincipalActiveBeforeExecution(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	compiler := &countingCompiler{}
	log := &eventLog{}
	host.AddObserver(log)
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")

	if _, err := session.Execute(request(params), compiler); err != nil {
		t.Fatal(err)
	}

	var once sync.Once
	host.AddObserver(netsec.ObserverFunc(func(event netsec.Event) {
		if event.Type != netsec.EventCacheHit {
			return
		}
		once.Do(func() {
			snap := host.Snapshot()
			principal := snap.Principals["alice"]
			principal.Active = false
			snap.Principals["alice"] = principal
			_ = host.ReplaceSnapshot(snap)
		})
	}))

	before := log.Count(netsec.EventOperatorStart)
	_, err = session.Execute(request(params), compiler)
	if !errors.Is(err, netsec.ErrPrincipalInactive) {
		t.Fatalf("error = %v, want inactive principal", err)
	}
	if got := log.Count(netsec.EventOperatorStart) - before; got != 0 {
		t.Fatalf("cache hit executed %d operators after principal deactivation", got)
	}
	if compiler.Calls() != 1 {
		t.Fatalf("deactivated principal caused a new compile: calls=%d", compiler.Calls())
	}
}

func TestStructuredBindingMismatchRunsZeroOperators(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	compiler := &countingCompiler{}
	log := &eventLog{}
	host.AddObserver(log)

	bad := netsec.NewBoundParameters(
		netsec.BoundValue{Type: netsec.ParamText, Value: "not-an-int"},
		netsec.BoundValue{Type: netsec.ParamText, Value: "ok"},
	)
	_, err = session.Execute(request(bad), compiler)
	if !errors.Is(err, netsec.ErrParameterType) {
		t.Fatalf("error = %v, want parameter type", err)
	}
	if compiler.Calls() != 0 {
		t.Fatalf("compiled a plan for invalid bind values")
	}
	if log.Count(netsec.EventOperatorStart) != 0 {
		t.Fatal("invalid bind values executed operators")
	}

	if _, err := netsec.BindParameters([]netsec.ParamType{netsec.ParamInt}, []any{"wrong"}); !errors.Is(err, netsec.ErrParameterType) {
		t.Fatalf("BindParameters error = %v", err)
	}
}

func TestPlanDependenciesAndOperatorsAreImmutableCopies(t *testing.T) {
	host := netsec.NewHost(testSnapshot())
	session, err := host.NewSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	params := mustBind(t, selectQuery().ParamTypes, 1, "x")
	pending, err := session.Prepare(request(params))
	if err != nil {
		t.Fatal(err)
	}
	plan := pending.Plan()
	if key, err := plan.Input().Key(); err != nil || key != plan.Key() {
		t.Fatalf("plan input key = %q/%v, plan key = %q", key, err, plan.Key())
	}

	deps := plan.Dependencies()
	deps[0].SchemaVersion = 999
	if plan.Dependencies()[0].SchemaVersion == 999 {
		t.Fatal("dependency copy mutated the plan")
	}
	ops := plan.Operators()
	if len(ops) == 0 {
		t.Fatal("plan has no operators")
	}
	ops[0] = netsec.NewOperator(netsec.OperatorQuery, "tampered")
	if plan.Operators()[0].Detail() == "tampered" {
		t.Fatal("operator slice mutation reached the plan")
	}
	input := plan.Input()
	input.Objects[0].SchemaVersion = 999
	if plan.Input().Objects[0].SchemaVersion == 999 {
		t.Fatal("input copy mutated the plan")
	}
}

func TestWeakCacheKeyFiniteDifferentialSearch(t *testing.T) {
	domain := netsec.KeyDomain{
		Principals:     [2]netsec.PrincipalID{"alice", "bob"},
		RoleClosures:   [2]string{"role-a", "role-b"},
		ObjectVersions: [2]uint64{1, 2},
		RLSDigests:     [2]string{"rls-a", "rls-b"},
		PolicyEpochs:   [2]uint64{10, 11},
	}
	authorize := func(in netsec.KeyInput) bool {
		return in.PrincipalID == "alice" &&
			in.RoleClosureDigest == "role-a" &&
			in.ObjectVersion == 1 &&
			in.RLSDigest == "rls-a" &&
			in.PolicyEpoch == 10
	}
	weak := func(in netsec.KeyInput) string {
		return fmt.Sprintf("v=%d/e=%d", in.ObjectVersion, in.PolicyEpoch)
	}
	example, found := netsec.FindWeakKeyCounterexample(weak, authorize, domain)
	if !found {
		t.Fatal("did not find the principal-blind weak key")
	}
	if example.Distance != 1 {
		t.Fatalf("shortest witness distance = %d, want 1", example.Distance)
	}
	if example.FirstAuthorized == example.SecondAuthorized {
		t.Fatal("witness does not distinguish authorization")
	}
	if err := netsec.AuditKeyCoverage(weak, authorize, domain); err == nil {
		t.Fatal("weak key audit unexpectedly passed")
	}

	strong := func(in netsec.KeyInput) string {
		return fmt.Sprintf("%s|%s|%d|%s|%d",
			in.PrincipalID, in.RoleClosureDigest, in.ObjectVersion, in.RLSDigest, in.PolicyEpoch)
	}
	if _, found := netsec.FindWeakKeyCounterexample(strong, authorize, domain); found {
		t.Fatal("complete key unexpectedly reported weak")
	}
	if err := netsec.AuditKeyCoverage(strong, authorize, domain); err != nil {
		t.Fatalf("complete key audit: %v", err)
	}
}
