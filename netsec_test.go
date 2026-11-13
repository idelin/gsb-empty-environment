package netsec_test

import (
	"errors"
	"testing"

	netsec "example.org/netsec/q0121"
)

// newTestHost builds a host with a role hierarchy:
//
//	admin -> (none)
//	analyst -> staff
//	staff -> (none)
//	guest -> (none)
//
// and an "orders" table with protected tenant column tenant_id, keyed by id.
func newTestHost(t *testing.T) (*netsec.Host, *netsec.RoleGraph, netsec.Schema) {
	t.Helper()
	roles := netsec.NewRoleGraph()
	must(t, roles.AddRole("admin"))
	must(t, roles.AddRole("staff"))
	must(t, roles.AddRole("analyst", "staff"))
	must(t, roles.AddRole("guest"))

	host := netsec.NewHost(roles)
	schema := netsec.Schema{
		Name:      "orders",
		Columns:   []string{"id", "tenant_id", "region", "amount"},
		Protected: []string{"tenant_id"},
		KeyColumn: "id",
	}
	seed := []netsec.Row{
		{"id": "r1", "tenant_id": int64(1), "region": int64(10), "amount": int64(100)},
		{"id": "r2", "tenant_id": int64(2), "region": int64(10), "amount": int64(200)},
		{"id": "r3", "tenant_id": int64(1), "region": int64(20), "amount": int64(50)},
	}
	host.CreateSource(schema, seed)

	// RLS: staff/analyst may only see tenant 1; admin sees everything.
	policy := &netsec.Policy{
		Name: "tenant-isolation",
		Body: "tenant_id == 1 unless admin",
		Predicate: func(row netsec.Row, sub netsec.Subject, roles []string) bool {
			for _, r := range roles {
				if r == "admin" {
					return true
				}
			}
			if sub.Role == "admin" {
				return true
			}
			return row["tenant_id"] == int64(1)
		},
	}
	src, _ := host.Source("orders")
	policy.SourceID = src.ID()
	must(t, host.SetPolicy(policy))

	return host, roles, schema
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func proofFor(t *testing.T, e netsec.Expr, schema netsec.Schema) *netsec.FlowProof {
	t.Helper()
	summary, err := netsec.AnalyzeFlow(e, schema)
	must(t, err)
	return &netsec.FlowProof{Summary: summary}
}

func ordersSchema() netsec.Schema {
	return netsec.Schema{
		Name:      "orders",
		Columns:   []string{"id", "tenant_id", "region", "amount"},
		Protected: []string{"tenant_id"},
		KeyColumn: "id",
	}
}

func regionSumOwnerView(t *testing.T, host *netsec.Host, owner string) *netsec.ViewDef {
	t.Helper()
	src, _ := host.Source("orders")
	schema := ordersSchema()
	amount := netsec.AggExpr{Kind: netsec.AggSum, Arg: netsec.ColExpr{Column: "amount"}}
	region := netsec.ColExpr{Column: "region"}
	view, err := netsec.NewViewDef("public_region_sum", owner, src, netsec.OwnerSnapshot,
		netsec.Projection{
			GroupBy: []string{"region"},
			Outputs: []netsec.Output{
				{Name: "region", Expr: region, Proof: proofFor(t, region, schema)},
				{Name: "total", Expr: amount, Proof: proofFor(t, amount, schema)},
			},
		})
	must(t, err)
	must(t, host.CreateView(view))
	return view
}

func tenantPartitionView(t *testing.T, host *netsec.Host, name string, kind netsec.AggKind) *netsec.ViewDef {
	t.Helper()
	src, _ := host.Source("orders")
	view, err := netsec.NewViewDef(name, "dba", src, netsec.PerPolicyPartition,
		netsec.Projection{
			GroupBy: []string{"region"},
			Outputs: []netsec.Output{
				{Name: "region", Expr: netsec.ColExpr{Column: "region"}},
				{Name: "total", Expr: netsec.AggExpr{Kind: kind, Arg: netsec.ColExpr{Column: "amount"}}},
			},
		})
	must(t, err)
	must(t, host.CreateView(view))
	return view
}

func valueOf(rows []netsec.ResultRow, group netsec.GroupKey, col string) (int64, bool) {
	for _, r := range rows {
		if r.Group == group {
			v, ok := r.Values[col]
			return v, ok
		}
	}
	return 0, false
}

// Scenario 1: owner sees all rows; a low-privilege subject can never read the
// owner's full cache and only hits its own RLS partition.
func TestOwnerVisibleUserInvisible(t *testing.T) {
	host, _, _ := newTestHost(t)
	tenantPartitionView(t, host, "v_sum", netsec.AggSum)

	admin := netsec.Subject{ID: "u-admin", Role: "admin"}
	staff := netsec.Subject{ID: "u-staff", Role: "staff"}

	if _, err := host.FullRefresh("v_sum", admin); err != nil {
		t.Fatalf("admin refresh: %v", err)
	}
	if _, err := host.FullRefresh("v_sum", staff); err != nil {
		t.Fatalf("staff refresh: %v", err)
	}

	adminRows, adminEntry, err := host.Query("v_sum", admin)
	must(t, err)
	if got, _ := valueOf(adminRows, "10", "total"); got != 300 {
		t.Fatalf("admin region 10 total = %d, want 300", got)
	}
	if got, _ := valueOf(adminRows, "20", "total"); got != 50 {
		t.Fatalf("admin region 20 total = %d, want 50", got)
	}

	staffRows, staffEntry, err := host.Query("v_sum", staff)
	must(t, err)
	if got, _ := valueOf(staffRows, "10", "total"); got != 100 {
		t.Fatalf("staff region 10 total = %d, want 100 (tenant 1 only)", got)
	}
	if _, ok := valueOf(staffRows, "20", "total"); !ok {
		t.Fatal("staff partition missing region 20")
	}

	adminKey, err := host.KeyFor("v_sum", admin)
	must(t, err)
	staffKey, err := host.KeyFor("v_sum", staff)
	must(t, err)
	if adminKey == staffKey {
		t.Fatal("admin and staff share a cache partition")
	}
	if staffEntry.Key == adminEntry.Key {
		t.Fatal("staff was served the admin partition")
	}

	diffs, err := host.VerifyAgainstReference("v_sum", staff)
	must(t, err)
	if len(diffs) != 0 {
		t.Fatalf("reference mismatch: %+v", diffs)
	}
}

// Scenario 2: role migration changes the role-closure class, making the old
// partition unreadable until a refresh under the new class.
func TestRoleMigration(t *testing.T) {
	host, roles, _ := newTestHost(t)
	tenantPartitionView(t, host, "v_sum", netsec.AggSum)
	staff := netsec.Subject{ID: "u1", Role: "staff"}

	_, err := host.FullRefresh("v_sum", staff)
	must(t, err)
	oldKey, err := host.KeyFor("v_sum", staff)
	must(t, err)

	// Promote staff to inherit admin powers.
	must(t, roles.AddRole("staff", "admin"))

	if _, _, err := host.Query("v_sum", staff); !errors.Is(err, netsec.ErrStalePartition) {
		t.Fatalf("expected stale partition after role migration, got %v", err)
	}
	newKey, err := host.KeyFor("v_sum", staff)
	must(t, err)
	if newKey == oldKey {
		t.Fatal("subject class did not change after role migration")
	}

	_, err = host.FullRefresh("v_sum", staff)
	must(t, err)
	rows, _, err := host.Query("v_sum", staff)
	must(t, err)
	if got, _ := valueOf(rows, "10", "total"); got != 300 {
		t.Fatalf("post-migration total = %d, want 300", got)
	}
}

// Scenario 3: tightening an RLS policy advances its epoch and invalidates the
// old partition.
func TestRLSTightening(t *testing.T) {
	host, _, _ := newTestHost(t)
	tenantPartitionView(t, host, "v_sum", netsec.AggSum)
	staff := netsec.Subject{ID: "u1", Role: "staff"}
	_, err := host.FullRefresh("v_sum", staff)
	must(t, err)

	src, _ := host.Source("orders")
	tightened := &netsec.Policy{
		SourceID: src.ID(),
		Name:     "tenant-isolation",
		Body:     "tenant_id == 1 AND region == 10",
		Predicate: func(row netsec.Row, sub netsec.Subject, roles []string) bool {
			for _, r := range roles {
				if r == "admin" {
					return true
				}
			}
			return row["tenant_id"] == int64(1) && row["region"] == int64(10)
		},
	}
	must(t, host.SetPolicy(tightened))

	if _, _, err := host.Query("v_sum", staff); !errors.Is(err, netsec.ErrStalePartition) {
		t.Fatalf("expected stale partition after RLS tightening, got %v", err)
	}
	_, err = host.FullRefresh("v_sum", staff)
	must(t, err)
	rows, entry, err := host.Query("v_sum", staff)
	must(t, err)
	if len(rows) != 1 {
		t.Fatalf("expected one visible group after tightening, got %d rows", len(rows))
	}
	if got, _ := valueOf(rows, "10", "total"); got != 100 {
		t.Fatalf("tightened total = %d, want 100", got)
	}
	if entry.PolicyEpoch != 2 {
		t.Fatalf("policy epoch = %d, want 2", entry.PolicyEpoch)
	}
}

// Scenario 4: incremental SUM refresh retracts a contribution when a row
// moves from visible to invisible.
func TestSumRetraction(t *testing.T) {
	host, _, _ := newTestHost(t)
	tenantPartitionView(t, host, "v_sum", netsec.AggSum)
	staff := netsec.Subject{ID: "u1", Role: "staff"}
	_, err := host.FullRefresh("v_sum", staff)
	must(t, err)

	oldRow := netsec.Row{"id": "r1", "tenant_id": int64(1), "region": int64(10), "amount": int64(100)}
	newRow := netsec.Row{"id": "r1", "tenant_id": int64(2), "region": int64(10), "amount": int64(100)}
	stats, err := host.ApplyAndIncrementalRefresh("v_sum", staff, []netsec.RowEvent{{
		Kind: netsec.EventUpdate,
		Key:  "r1",
		Old:  oldRow,
		New:  newRow,
	}})
	must(t, err)
	if stats.FullRebuild {
		t.Fatal("SUM should retract incrementally, not fall back")
	}

	rows, _, err := host.Query("v_sum", staff)
	must(t, err)
	if _, ok := valueOf(rows, "10", "total"); ok {
		t.Fatalf("region 10 group should be removed after sole visible row left: %+v", rows)
	}
	if got, _ := valueOf(rows, "20", "total"); got != 50 {
		t.Fatalf("region 20 total = %d, want 50", got)
	}

	insertRow := netsec.Row{"id": "r4", "tenant_id": int64(1), "region": int64(20), "amount": int64(25)}
	stats, err = host.ApplyAndIncrementalRefresh("v_sum", staff, []netsec.RowEvent{{
		Kind: netsec.EventInsert,
		Key:  "r4",
		New:  insertRow,
	}})
	must(t, err)
	if stats.FullRebuild {
		t.Fatal("insert should stay incremental")
	}
	rows, _, err = host.Query("v_sum", staff)
	must(t, err)
	if got, _ := valueOf(rows, "20", "total"); got != 75 {
		t.Fatalf("region 20 total after insert = %d, want 75", got)
	}

	_, err = host.ApplyAndIncrementalRefresh("v_sum", staff, []netsec.RowEvent{{
		Kind: netsec.EventDelete,
		Key:  "r4",
		Old:  insertRow,
	}})
	must(t, err)
	rows, _, err = host.Query("v_sum", staff)
	must(t, err)
	if got, _ := valueOf(rows, "20", "total"); got != 50 {
		t.Fatalf("region 20 total after delete = %d, want 50", got)
	}

	diffs, err := host.VerifyAgainstReference("v_sum", staff)
	must(t, err)
	if len(diffs) != 0 {
		t.Fatalf("incremental cache diverged from reference: %+v", diffs)
	}
}
