package plancache_test

import (
	"testing"

	"formal.local/w3-bc80ec5616/plancache"
)

const (
	alice    = plancache.PrincipalID("alice")
	mallory  = plancache.PrincipalID("mallory")
	ordersO  = plancache.ObjectID("obj-orders-1")
	ordersO2 = plancache.ObjectID("obj-orders-2")
	innerV   = plancache.ViewID("view-v_inner-1")
	outerV   = plancache.ViewID("view-v_outer-1")
	outerV2  = plancache.ViewID("view-v_outer-2")
	rlsV1    = plancache.Digest("rls:allow-v1")
	rlsV2    = plancache.Digest("rls:tight-v2")
	ast1     = plancache.Digest("ast:canonical:select-orders")
)

type fixture struct {
	bus  *plancache.InvalidationBus
	cat  *plancache.MemCatalog
	obs  *plancache.TraceObserver
	exec *plancache.Executor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	bus := plancache.NewInvalidationBus()
	cat := plancache.NewMemCatalog(bus)
	obs := &plancache.TraceObserver{}

	cat.AddPrincipal(alice, plancache.Digest("roles:alice:v1"))
	cat.AddPrincipal(mallory, plancache.Digest("roles:mallory:v1"))
	cat.AddTable(ordersO, "orders", 1, rlsV1)
	cat.AddView(innerV, "v_inner", 1, "", ordersO)
	cat.AddView(outerV, "v_outer", 1, "", ordersO)
	cat.Grant(alice, ordersO, "SELECT")
	cat.Grant(mallory, ordersO, "SELECT")

	session := plancache.NewPlanCache(plancache.SESSION)
	global := plancache.NewPlanCache(plancache.GLOBAL)
	exec := plancache.NewExecutor(cat, session, global, obs)
	return &fixture{bus: bus, cat: cat, obs: obs, exec: exec}
}

// selectInput builds a compile input for a query expanded through the
// nested view chain v_outer -> v_inner -> orders.
func selectInput(p plancache.PrincipalID, roles plancache.Digest, epoch int64,
	outerVersion, innerVersion, schemaVersion int64, rls plancache.Digest,
	outerID, innerID plancache.ViewID) plancache.CompileInput {
	return plancache.CompileInput{
		ASTDigest:   ast1,
		ParamTypes:  []string{"int64"},
		Caller:      p,
		RoleClosure: roles,
		PolicyEpoch: epoch,
		RequiredOps: []plancache.Operation{"SELECT"},
		DepGraph: []plancache.DependencyNode{
			{RefType: "view", View: plancache.ViewRef{
				ViewID: outerID, ViewDefVersion: outerVersion,
				UnderlyingTable: ordersO,
			}},
			{RefType: "view", View: plancache.ViewRef{
				ViewID: innerID, ViewDefVersion: innerVersion,
				UnderlyingTable: ordersO,
			}},
			{RefType: "table", Table: plancache.TableRef{
				ObjectID: ordersO, SchemaVersion: schemaVersion, RLSPolicy: rls,
			}},
		},
		RLSPolicies: map[plancache.ObjectID]plancache.Digest{ordersO: rls},
	}
}

func baselineInput(p plancache.PrincipalID, roles plancache.Digest) plancache.CompileInput {
	return selectInput(p, roles, 0, 1, 1, 1, rlsV1, outerV, innerV)
}

// countingFactory returns one observable operator per call and records how
// often compilation materialized operators.
func countingFactory(calls *int) plancache.OperatorFactory {
	return func(in plancache.CompileInput) ([]plancache.Operator, error) {
		*calls++
		return []plancache.Operator{{
			Name: "scan-orders",
			Func: func([]any) error { return nil },
		}}, nil
	}
}

func assertNoOperators(t *testing.T, obs *plancache.TraceObserver) {
	t.Helper()
	if n := obs.OperatorCount(); n != 0 {
		t.Fatalf("expected zero operator callbacks, got %d: %v", n, obs.ExecutedOperators())
	}
}
