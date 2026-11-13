package netsec_test

import (
	"errors"
	"sync"
	"testing"

	netsec "example.org/netsec/q0121"
)

// Scenario 5: MIN cannot be retracted, so an incremental request must fall
// back to a full refresh and still produce the correct answer.
func TestMinFallsBackToFullRefresh(t *testing.T) {
	host, _, _ := newTestHost(t)
	tenantPartitionView(t, host, "v_min", netsec.AggMin)
	staff := netsec.Subject{ID: "u1", Role: "staff"}
	_, err := host.FullRefresh("v_min", staff)
	must(t, err)

	stats, err := host.ApplyAndIncrementalRefresh("v_min", staff, []netsec.RowEvent{{
		Kind: netsec.EventUpdate,
		Key:  "r1",
		Old:  netsec.Row{"id": "r1", "tenant_id": int64(1), "region": int64(10), "amount": int64(100)},
		New:  netsec.Row{"id": "r1", "tenant_id": int64(2), "region": int64(10), "amount": int64(100)},
	}})
	must(t, err)
	if !stats.FullRebuild {
		t.Fatal("MIN update must trigger a full rebuild")
	}

	rows, _, err := host.Query("v_min", staff)
	must(t, err)
	if _, ok := valueOf(rows, "10", "total"); ok {
		t.Fatalf("region 10 should vanish after its only visible row left: %+v", rows)
	}
	if got, _ := valueOf(rows, "20", "total"); got != 50 {
		t.Fatalf("region 20 min = %d, want 50", got)
	}
	diffs, err := host.VerifyAgainstReference("v_min", staff)
	must(t, err)
	if len(diffs) != 0 {
		t.Fatalf("full-rebuild cache diverged from reference: %+v", diffs)
	}
}

// Scenario 6: rebuilding the source under the same name mints a new object ID
// and invalidates every cache entry bound to the old incarnation.
func TestSameNameRebuild(t *testing.T) {
	host, _, schema := newTestHost(t)
	view := tenantPartitionView(t, host, "v_sum", netsec.AggSum)
	staff := netsec.Subject{ID: "u1", Role: "staff"}
	_, err := host.FullRefresh("v_sum", staff)
	must(t, err)

	oldID := view.SourceID
	newSource, err := host.RebuildSource(schema, []netsec.Row{
		{"id": "z1", "tenant_id": int64(9), "region": int64(10), "amount": int64(7)},
	})
	must(t, err)
	if newSource.ID() == oldID {
		t.Fatal("rebuilt source reused the old object ID")
	}

	if _, _, err := host.Query("v_sum", staff); !errors.Is(err, netsec.ErrSourceRebuilt) {
		t.Fatalf("expected ErrSourceRebuilt, got %v", err)
	}

	// The new incarnation needs its own RLS policy before partition views work.
	rebuiltPolicy := &netsec.Policy{
		SourceID: newSource.ID(),
		Name:     "tenant-isolation",
		Body:     "tenant_id == 1 unless admin",
		Predicate: func(row netsec.Row, sub netsec.Subject, roles []string) bool {
			for _, r := range roles {
				if r == "admin" {
					return true
				}
			}
			return row["tenant_id"] == int64(1)
		},
	}
	must(t, host.SetPolicy(rebuiltPolicy))

	// The old view points at the dead incarnation; a fresh view over the new
	// source must be created and refreshed.
	tenantPartitionViewNamed(t, host, "v_sum_v2", newSource)
	_, err = host.FullRefresh("v_sum_v2", staff)
	must(t, err)
	rows, _, err := host.Query("v_sum_v2", staff)
	must(t, err)
	if len(rows) != 0 {
		t.Fatalf("expected empty staff partition over rebuilt table, got %+v", rows)
	}
}

func tenantPartitionViewNamed(t *testing.T, host *netsec.Host, name string, src *netsec.Source) *netsec.ViewDef {
	t.Helper()
	view, err := netsec.NewViewDef(name, "dba", src, netsec.PerPolicyPartition,
		netsec.Projection{
			GroupBy: []string{"region"},
			Outputs: []netsec.Output{
				{Name: "region", Expr: netsec.ColExpr{Column: "region"}},
				{Name: "total", Expr: netsec.AggExpr{Kind: netsec.AggSum, Arg: netsec.ColExpr{Column: "amount"}}},
			},
		})
	must(t, err)
	must(t, host.CreateView(view))
	return view
}

// Scenario 7: an interrupted refresh keeps the previous entry readable; the
// partial staging entry is retained under review and is never queryable.
func TestInterruptedRefreshKeepsOldEntry(t *testing.T) {
	host, _, _ := newTestHost(t)
	tenantPartitionView(t, host, "v_sum", netsec.AggSum)
	staff := netsec.Subject{ID: "u1", Role: "staff"}
	first, err := host.FullRefresh("v_sum", staff)
	must(t, err)

	var once sync.Once
	host.SetRefreshHook(func(viewName string) error {
		var fail error
		once.Do(func() { fail = errors.New("simulated crash") })
		return fail
	})

	if _, err := host.FullRefresh("v_sum", staff); !errors.Is(err, netsec.ErrRefreshFailed) {
		t.Fatalf("expected refresh failure, got %v", err)
	}

	rows, entry, err := host.Query("v_sum", staff)
	must(t, err)
	if entry.RefreshLSN != first.LSN {
		t.Fatalf("old entry LSN changed: %d != %d", entry.RefreshLSN, first.LSN)
	}
	if got, _ := valueOf(rows, "10", "total"); got != 100 {
		t.Fatalf("old entry altered after failed refresh: %+v", rows)
	}

	pending, ok := host.PendingEntry(first.Key)
	if !ok {
		t.Fatal("failed refresh did not retain a staging entry for review")
	}
	if pending.Complete {
		t.Fatal("staging entry must be marked incomplete")
	}

	host.SetRefreshHook(nil)
	_, err = host.FullRefresh("v_sum", staff)
	must(t, err)
	rows, _, err = host.Query("v_sum", staff)
	must(t, err)
	if got, _ := valueOf(rows, "10", "total"); got != 100 {
		t.Fatalf("post-recovery value wrong: %+v", rows)
	}
}

// OwnerSnapshot proof enforcement: protected columns, error-capable and
// side-effecting expressions are rejected, as are missing proofs and raw rows.
func TestOwnerSnapshotProofs(t *testing.T) {
	host, _, schema := newTestHost(t)
	src, _ := host.Source("orders")

	protectedAgg := netsec.AggExpr{Kind: netsec.AggSum, Arg: netsec.ColExpr{Column: "tenant_id"}}
	if _, err := netsec.NewViewDef("bad1", "dba", src, netsec.OwnerSnapshot,
		netsec.Projection{Outputs: []netsec.Output{
			{Name: "x", Expr: protectedAgg, Proof: proofFor(t, protectedAgg, schema)},
		}}); !errors.Is(err, netsec.ErrInvalidProof) {
		t.Fatalf("protected column proof accepted: %v", err)
	}

	divExpr := netsec.AggExpr{Kind: netsec.AggSum, Arg: netsec.BinExpr{
		Op:    netsec.OpDiv,
		Left:  netsec.ColExpr{Column: "amount"},
		Right: netsec.LitExpr{Value: 0},
	}}
	if _, err := netsec.NewViewDef("bad2", "dba", src, netsec.OwnerSnapshot,
		netsec.Projection{Outputs: []netsec.Output{
			{Name: "x", Expr: divExpr, Proof: proofFor(t, divExpr, schema)},
		}}); !errors.Is(err, netsec.ErrInvalidProof) {
		t.Fatalf("error-capable proof accepted: %v", err)
	}

	sideEffect := netsec.AggExpr{Kind: netsec.AggCount, Arg: netsec.FuncExpr{
		Name: "audit",
		Args: []netsec.Expr{netsec.ColExpr{Column: "amount"}},
	}}
	if _, err := netsec.NewViewDef("bad3", "dba", src, netsec.OwnerSnapshot,
		netsec.Projection{Outputs: []netsec.Output{
			{Name: "x", Expr: sideEffect, Proof: proofFor(t, sideEffect, schema)},
		}}); !errors.Is(err, netsec.ErrInvalidProof) {
		t.Fatalf("side-effecting proof accepted: %v", err)
	}

	amountSum := netsec.AggExpr{Kind: netsec.AggSum, Arg: netsec.ColExpr{Column: "amount"}}
	if _, err := netsec.NewViewDef("bad4", "dba", src, netsec.OwnerSnapshot,
		netsec.Projection{Outputs: []netsec.Output{
			{Name: "x", Expr: amountSum},
		}}); !errors.Is(err, netsec.ErrInvalidProof) {
		t.Fatalf("missing proof accepted: %v", err)
	}

	regionCol := netsec.ColExpr{Column: "region"}
	if _, err := netsec.NewViewDef("bad5", "dba", src, netsec.OwnerSnapshot,
		netsec.Projection{Outputs: []netsec.Output{
			{Name: "region", Expr: regionCol, Proof: proofFor(t, regionCol, schema)},
		}}); err == nil {
		t.Fatal("non-aggregate OwnerSnapshot (raw tenant rows) accepted")
	}

	// A valid public aggregate is shared across subjects without tenant data.
	regionSumOwnerView(t, host, "dba")
	_, err := host.FullRefresh("public_region_sum", netsec.Subject{})
	must(t, err)
	rows, _, err := host.Query("public_region_sum", netsec.Subject{ID: "low", Role: "guest"})
	must(t, err)
	if got, _ := valueOf(rows, "10", "total"); got != 300 {
		t.Fatalf("public region 10 total = %d, want 300", got)
	}
	if got, _ := valueOf(rows, "20", "total"); got != 50 {
		t.Fatalf("public region 20 total = %d, want 50", got)
	}
}

// Pending-review differential search surfaces added/removed/value-changed
// groups between a staged candidate and the live entry.
func TestPendingKeyDifferentialSearch(t *testing.T) {
	host, _, _ := newTestHost(t)
	tenantPartitionView(t, host, "v_sum", netsec.AggSum)
	staff := netsec.Subject{ID: "u1", Role: "staff"}
	first, err := host.FullRefresh("v_sum", staff)
	must(t, err)

	// Commit the insert (bumping the source version), then hold the next
	// refresh under review so a staged candidate can be diffed against the
	// previous live entry.
	version, _, err := host.ApplyEvents("orders", []netsec.RowEvent{{
		Kind: netsec.EventInsert,
		Key:  "r5",
		New:  netsec.Row{"id": "r5", "tenant_id": int64(1), "region": int64(30), "amount": int64(9)},
	}})
	must(t, err)
	if version != 2 {
		t.Fatalf("source version = %d, want 2", version)
	}
	host.SetRefreshHook(func(string) error { return errors.New("hold for review") })
	if _, err := host.FullRefresh("v_sum", staff); !errors.Is(err, netsec.ErrRefreshFailed) {
		t.Fatalf("expected held refresh, got %v", err)
	}

	diffs, err := host.SearchPendingKeyDiffs(first.Key)
	must(t, err)
	if len(diffs) != 1 || diffs[0].Kind != netsec.DiffExtra || diffs[0].Group != netsec.GroupKey("30") {
		t.Fatalf("expected one extra group 30, got %+v", diffs)
	}

	// While held under review the previous live entry is still bound to the
	// pre-change source version, so it is now stale and unreadable; the
	// pending group must never leak through the query path.
	if _, _, err := host.Query("v_sum", staff); !errors.Is(err, netsec.ErrStalePartition) {
		t.Fatalf("expected stale live entry while refresh is pending, got %v", err)
	}

	must(t, host.ApprovePending(first.Key))
	rows, _, err := host.Query("v_sum", staff)
	must(t, err)
	if got, _ := valueOf(rows, "30", "total"); got != 9 {
		t.Fatalf("approved group 30 total = %d, want 9", got)
	}

	// A value-change differential: update an existing row without another
	// failed publication, this time exercised through the full approve flow.
	host.SetRefreshHook(nil)
	host.DiscardPending(first.Key)
	_, _, err = host.ApplyEvents("orders", []netsec.RowEvent{{
		Kind: netsec.EventUpdate,
		Key:  "r3",
		Old:  netsec.Row{"id": "r3", "tenant_id": int64(1), "region": int64(20), "amount": int64(50)},
		New:  netsec.Row{"id": "r3", "tenant_id": int64(1), "region": int64(20), "amount": int64(60)},
	}})
	must(t, err)
	host.SetRefreshHook(func(string) error { return errors.New("hold again") })
	if _, err := host.FullRefresh("v_sum", staff); !errors.Is(err, netsec.ErrRefreshFailed) {
		t.Fatalf("expected held refresh, got %v", err)
	}
	diffs, err = host.SearchPendingKeyDiffs(first.Key)
	must(t, err)
	var sawValue bool
	for _, d := range diffs {
		if d.Kind == netsec.DiffValue && d.Group == netsec.GroupKey("20") {
			sawValue = true
		}
	}
	if !sawValue {
		t.Fatalf("expected a value diff on group 20, got %+v", diffs)
	}
	must(t, host.ApprovePending(first.Key))
	rows, _, err = host.Query("v_sum", staff)
	must(t, err)
	if got, _ := valueOf(rows, "20", "total"); got != 60 {
		t.Fatalf("approved region 20 total = %d, want 60", got)
	}
}

// Concurrent refreshes publish whole entries; readers never observe partial
// or torn state.
func TestConcurrentRefreshAndQuery(t *testing.T) {
	host, _, _ := newTestHost(t)
	tenantPartitionView(t, host, "v_sum", netsec.AggSum)
	staff := netsec.Subject{ID: "u1", Role: "staff"}
	_, err := host.FullRefresh("v_sum", staff)
	must(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := host.FullRefresh("v_sum", staff); err != nil {
				t.Errorf("refresh: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			rows, _, err := host.Query("v_sum", staff)
			if err != nil {
				t.Errorf("query: %v", err)
				return
			}
			got, ok := valueOf(rows, "10", "total")
			if !ok || got != 100 {
				t.Errorf("torn read: region 10 total = %d ok=%v", got, ok)
			}
		}()
	}
	wg.Wait()
}

// A proof whose summary was tampered with (or computed for another
// expression) must be rejected.
func TestTamperedProofRejected(t *testing.T) {
	host, _, schema := newTestHost(t)
	src, _ := host.Source("orders")

	amountSum := netsec.AggExpr{Kind: netsec.AggSum, Arg: netsec.ColExpr{Column: "amount"}}
	summary, err := netsec.AnalyzeFlow(amountSum, schema)
	must(t, err)

	forged := summary
	forged.Protected = []string{"tenant_id"}
	if _, err := netsec.NewViewDef("forged", "dba", src, netsec.OwnerSnapshot,
		netsec.Projection{Outputs: []netsec.Output{
			{Name: "x", Expr: amountSum, Proof: &netsec.FlowProof{Summary: forged}},
		}}); !errors.Is(err, netsec.ErrInvalidProof) {
		t.Fatalf("forged protected list accepted: %v", err)
	}

	// Attach a valid-looking proof but for a different expression digest.
	other := netsec.AggExpr{Kind: netsec.AggCount, Arg: netsec.ColExpr{Column: "amount"}}
	otherSummary, err := netsec.AnalyzeFlow(other, schema)
	must(t, err)
	if _, err := netsec.NewViewDef("mismatch", "dba", src, netsec.OwnerSnapshot,
		netsec.Projection{Outputs: []netsec.Output{
			{Name: "x", Expr: amountSum, Proof: &netsec.FlowProof{Summary: otherSummary}},
		}}); !errors.Is(err, netsec.ErrInvalidProof) {
		t.Fatalf("digest-mismatched proof accepted: %v", err)
	}
}

// Cached entries must carry every required security binding.
func TestEntryBindings(t *testing.T) {
	host, _, _ := newTestHost(t)
	view := tenantPartitionView(t, host, "v_sum", netsec.AggSum)
	staff := netsec.Subject{ID: "u1", Role: "staff"}
	stats, err := host.FullRefresh("v_sum", staff)
	must(t, err)
	_, entry, err := host.Query("v_sum", staff)
	must(t, err)

	if entry.SourceID != view.SourceID {
		t.Fatal("entry not bound to source ID")
	}
	if entry.SourceVersion != stats.SourceVersion {
		t.Fatal("entry source version mismatch")
	}
	if entry.PolicyEpoch != 1 || entry.PolicyDigest == "" {
		t.Fatal("entry missing policy epoch/digest binding")
	}
	if entry.SubjectClass == "" || entry.SubjectRole != "staff" {
		t.Fatal("entry missing subject class binding")
	}
	if entry.RefreshLSN != stats.LSN || entry.RefreshLSN <= 0 {
		t.Fatal("entry missing refresh-transaction LSN")
	}
	if !entry.Complete {
		t.Fatal("published entry must be complete")
	}
}

func TestSubjectClassDigests(t *testing.T) {
	host, roles, _ := newTestHost(t)
	tenantPartitionView(t, host, "v_sum", netsec.AggSum)

	staffClass, err := host.SubjectClass("staff")
	must(t, err)
	analystClass, err := host.SubjectClass("analyst")
	must(t, err)
	if staffClass == analystClass {
		t.Fatal("different closures produced the same subject class")
	}

	analyst := netsec.Subject{ID: "a", Role: "analyst"}
	_, err = host.FullRefresh("v_sum", analyst)
	must(t, err)
	rows, _, err := host.Query("v_sum", analyst)
	must(t, err)
	if got, _ := valueOf(rows, "10", "total"); got != 100 {
		t.Fatalf("analyst region 10 total = %d, want 100", got)
	}

	// Changing the inherited staff closure changes analyst's class too.
	must(t, roles.AddRole("staff", "admin"))
	if _, _, err := host.Query("v_sum", analyst); !errors.Is(err, netsec.ErrStalePartition) {
		t.Fatalf("expected stale analyst partition after role change, got %v", err)
	}
}
