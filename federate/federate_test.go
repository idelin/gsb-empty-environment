package federate_test

import (
	"context"
	"testing"

	"prescreen.local/ps-523914d1f0/federate"
)

func row(vals ...federate.Value) federate.Row {
	return federate.Row{Values: vals}
}

func singleTable(t *testing.T, name string, dist federate.Distribution, rows []federate.Row) *federate.Table {
	t.Helper()
	schema := &federate.Schema{Columns: []federate.Column{
		{Name: "id", Type: federate.TypeInt},
		{Name: "grp", Type: federate.TypeString},
		{Name: "val", Type: federate.TypeInt},
	}}
	tbl, err := federate.NewTable(name, schema, dist, rows)
	if err != nil {
		t.Fatalf("new table %s: %v", name, err)
	}
	return tbl
}

func ordersRows(n int) []federate.Row {
	out := make([]federate.Row, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, row(
			federate.Int(int64(i)),
			federate.Str(string(rune('a'+i%5))),
			federate.Int(int64((i*7)%17)),
		))
	}
	return out
}

func mustVerify(t *testing.T, cat *federate.Catalog, q *federate.Query) *federate.Verification {
	t.Helper()
	v, err := federate.Verify(context.Background(), cat, q)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !v.Match {
		t.Fatalf("distributed result differs from reference: %s", v.Diff)
	}
	return v
}

func TestTopKLimitOffsetCorrection(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 4), ordersRows(41)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{
		federate.Select(federate.Col("id"), "id"),
		federate.Select(federate.Col("val"), "val"),
	}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.Col("val")), federate.Asc(federate.Col("id"))}
	q.Limit = 7
	q.Offset = 13
	v := mustVerify(t, cat, q)
	if !v.Plan.TopK.Pushed {
		t.Fatalf("top-k should be pushed: %s", v.Plan.TopK.Reason)
	}
	if v.Plan.TopK.ShardLimit != 20 {
		t.Fatalf("shard limit = %d, want L+O = 20", v.Plan.TopK.ShardLimit)
	}
	if v.Plan.TopK.ShardOffset != 0 {
		t.Fatalf("shard offset = %d, want 0", v.Plan.TopK.ShardOffset)
	}
}

func TestLimitOffsetWithoutOrder(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 5), ordersRows(33)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{federate.Select(federate.Col("id"), "id")}
	q.Limit = 4
	q.Offset = 9
	v := mustVerify(t, cat, q)
	if !v.Plan.TopK.Pushed || v.Plan.TopK.ShardLimit != 13 {
		t.Fatalf("expected safe L+O shard limit 13, got %+v", v.Plan.TopK)
	}
}

func TestNonShardKeyHavingAndAvgTwoStage(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 4), ordersRows(60)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{
		federate.Select(federate.Col("grp"), "grp"),
		federate.Select(federate.Count(nil), "n"),
		federate.Select(federate.Avg(federate.Col("val")), "avg_val"),
	}
	q.GroupBy = []federate.Expr{federate.Col("grp")}
	q.Having = federate.Gt(federate.Count(nil), federate.Const(federate.Int(5)))
	v := mustVerify(t, cat, q)
	ap := v.Plan.Aggregation
	if ap.Mode != federate.AggPartialFinal {
		t.Fatalf("mode = %s, want partial_final", ap.Mode)
	}
	if ap.HavingPushdown {
		t.Fatal("HAVING must not be pushed when GROUP BY omits the shard key")
	}
	foundAvg := false
	for _, s := range ap.Specs {
		if s.Agg.Kind == federate.AggAvg {
			foundAvg = true
			if s.Partial != federate.PartialSumCount || s.Final != federate.FinalAvg {
				t.Fatalf("AVG rewrite = %s/%s", s.Partial, s.Final)
			}
		}
		if s.Agg.Kind == federate.AggCount && s.Partial != federate.PartialCount {
			t.Fatalf("COUNT rewrite = %s", s.Partial)
		}
	}
	if !foundAvg {
		t.Fatal("AVG spec not found")
	}
}

func TestColocatedGroupPushesHaving(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 3), ordersRows(40)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{
		federate.Select(federate.Col("id"), "id"),
		federate.Select(federate.Count(nil), "n"),
	}
	q.GroupBy = []federate.Expr{federate.Col("id")}
	q.Having = federate.Ge(federate.Count(nil), federate.Const(federate.Int(1)))
	v := mustVerify(t, cat, q)
	if v.Plan.Aggregation.Mode != federate.AggSingleStage {
		t.Fatalf("mode = %s, want single_stage", v.Plan.Aggregation.Mode)
	}
	if !v.Plan.Aggregation.HavingPushdown {
		t.Fatal("co-located HAVING should be pushed")
	}
}

func TestCountDistinctNonShardKey(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 4), ordersRows(55)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{
		federate.Select(federate.Col("grp"), "grp"),
		federate.Select(federate.CountDistinct(federate.Col("val")), "n"),
	}
	q.GroupBy = []federate.Expr{federate.Col("grp")}
	v := mustVerify(t, cat, q)
	if v.Plan.Aggregation.Mode != federate.AggPartialFinal {
		t.Fatalf("mode = %s, want partial_final", v.Plan.Aggregation.Mode)
	}
	for _, s := range v.Plan.Aggregation.Specs {
		if s.Agg.Kind == federate.AggCount && s.Agg.Distinct {
			if s.Partial != federate.PartialDistinctSet || s.Final != federate.FinalCountDistinct {
				t.Fatalf("COUNT DISTINCT rewrite = %s/%s", s.Partial, s.Final)
			}
			if s.Decomposable {
				t.Fatal("COUNT(DISTINCT non_shard_key) must be marked non-decomposable")
			}
		}
	}
}

func joinQuery(from, to string, jt federate.JoinType, on federate.Expr) *federate.Query {
	q := federate.NewQuery(from)
	q.Join = &federate.JoinSpec{Table: to, Type: jt, On: on}
	return q
}

func TestColocatedJoinHash(t *testing.T) {
	left := singleTable(t, "left_orders", federate.HashDist([]string{"id"}, 4), ordersRows(31))
	right := singleTable(t, "right_orders", federate.HashDist([]string{"id"}, 4), ordersRows(47))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("left_orders", "right_orders", federate.InnerJoin,
		federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "id"),
		federate.Select(federate.ColOf("left_orders", "val"), "lv"),
		federate.Select(federate.ColOf("right_orders", "val"), "rv"),
	}
	q.OrderBy = []federate.OrderItem{
		federate.Asc(federate.ColOf("left_orders", "val")),
		federate.Asc(federate.ColOf("left_orders", "id")),
	}
	q.Limit = 6
	q.Offset = 2
	v := mustVerify(t, cat, q)
	if !v.Plan.Join.Pushed {
		t.Fatalf("co-located join should be pushed: %s", v.Plan.Join.Reason)
	}
	if !v.Plan.TopK.Pushed || v.Plan.TopK.ShardLimit != 8 {
		t.Fatalf("join top-k should use L+O=8: %+v", v.Plan.TopK)
	}
}

func TestNonColocatedJoinFallsBack(t *testing.T) {
	left := singleTable(t, "left_orders", federate.HashDist([]string{"id"}, 4), ordersRows(31))
	right := singleTable(t, "right_orders", federate.HashDist([]string{"id"}, 5), ordersRows(47))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("left_orders", "right_orders", federate.InnerJoin,
		federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "id"),
		federate.Select(federate.ColOf("right_orders", "val"), "rv"),
	}
	q.OrderBy = []federate.OrderItem{federate.Desc(federate.ColOf("left_orders", "id"))}
	q.Limit = 5
	q.Offset = 4
	v := mustVerify(t, cat, q)
	if v.Plan.Join.Pushed {
		t.Fatal("join with different shard mappings must not be pushed")
	}
}

func TestRangeColocatedJoin(t *testing.T) {
	bounds := []federate.Value{federate.Int(10), federate.Int(20), federate.Int(30)}
	left := singleTable(t, "left_orders", federate.RangeDist("id", bounds...), ordersRows(31))
	right := singleTable(t, "right_orders", federate.RangeDist("id", bounds...), ordersRows(31))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("left_orders", "right_orders", federate.InnerJoin,
		federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "id"),
		federate.Select(federate.ColOf("right_orders", "val"), "rv"),
	}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.ColOf("left_orders", "id"))}
	v := mustVerify(t, cat, q)
	if !v.Plan.Join.Pushed {
		t.Fatalf("range co-located join should be pushed: %s", v.Plan.Join.Reason)
	}
}

func TestOuterJoinSafety(t *testing.T) {
	left := singleTable(t, "left_orders", federate.HashDist([]string{"id"}, 3), ordersRows(21))
	right := singleTable(t, "right_orders", federate.HashDist([]string{"id"}, 3), ordersRows(27))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	for _, jt := range []federate.JoinType{federate.LeftJoin, federate.RightJoin, federate.FullJoin} {
		q := joinQuery("left_orders", "right_orders", jt,
			federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")))
		q.Select = []federate.SelectItem{
			federate.Select(federate.ColOf("left_orders", "id"), "lid"),
			federate.Select(federate.ColOf("right_orders", "id"), "rid"),
		}
		q.OrderBy = []federate.OrderItem{
			federate.Asc(federate.ColOf("left_orders", "id")),
			federate.Asc(federate.ColOf("right_orders", "id")),
		}
		v := mustVerify(t, cat, q)
		if !v.Plan.Join.Pushed {
			t.Fatalf("%s co-located join should be pushed: %s", jt, v.Plan.Join.Reason)
		}
	}

	// A nullable outer-join key must be treated conservatively.
	nullableSchema := &federate.Schema{Columns: []federate.Column{
		{Name: "id", Type: federate.TypeInt, Nullable: true},
		{Name: "grp", Type: federate.TypeString},
		{Name: "val", Type: federate.TypeInt},
	}}
	ln, err := federate.NewTable("ln", nullableSchema, federate.HashDist([]string{"id"}, 3), ordersRows(11))
	if err != nil {
		t.Fatal(err)
	}
	rn, err := federate.NewTable("rn", nullableSchema, federate.HashDist([]string{"id"}, 3), ordersRows(13))
	if err != nil {
		t.Fatal(err)
	}
	cat2, _ := federate.NewCatalog(ln, rn)
	q := joinQuery("ln", "rn", federate.LeftJoin,
		federate.Eq(federate.ColOf("ln", "id"), federate.ColOf("rn", "id")))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("ln", "id"), "lid"),
		federate.Select(federate.ColOf("rn", "id"), "rid"),
	}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.ColOf("ln", "id"))}
	v := mustVerify(t, cat2, q)
	if v.Plan.Join.Pushed {
		t.Fatal("nullable outer join key must block join pushdown")
	}
}

func TestDifferentCollationBlocksJoinPushdown(t *testing.T) {
	schema := &federate.Schema{Columns: []federate.Column{
		{Name: "name", Type: federate.TypeString},
		{Name: "val", Type: federate.TypeInt},
	}}
	mk := func(name string, vals []string) *federate.Table {
		rows := make([]federate.Row, len(vals))
		for i, s := range vals {
			rows[i] = row(federate.Str(s), federate.Int(int64(i)))
		}
		tbl, err := federate.NewTable(name, schema, federate.HashDist([]string{"name"}, 4), rows)
		if err != nil {
			t.Fatal(err)
		}
		return tbl
	}
	left := mk("l", []string{"A", "a", "b", "B", "c"})
	right := mk("r", []string{"a", "A", "B", "b", "d"})
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("l", "r", federate.InnerJoin,
		federate.Eq(
			federate.Collate(federate.ColOf("l", "name"), "nocase"),
			federate.Collate(federate.ColOf("r", "name"), "nocase"),
		))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("l", "name"), "ln"),
		federate.Select(federate.ColOf("r", "name"), "rn"),
	}
	q.OrderBy = []federate.OrderItem{
		federate.Asc(federate.ColOf("l", "name")),
		federate.Asc(federate.ColOf("r", "name")),
	}
	v := mustVerify(t, cat, q)
	if v.Plan.Join.Pushed {
		t.Fatal("non-binary collation join must not be pushed")
	}
	if v.Plan.Join.Reason == "" {
		t.Fatal("expected a collation rejection reason")
	}
}

func TestVolatileFilterAndJoinAreNotPushed(t *testing.T) {
	cat, err := federate.NewCatalog(
		singleTable(t, "left_orders", federate.HashDist([]string{"id"}, 3), ordersRows(41)),
		singleTable(t, "right_orders", federate.HashDist([]string{"id"}, 3), ordersRows(45)),
	)
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("left_orders")
	q.Where = federate.Lt(federate.Func("random"), federate.Const(federate.Float(0.55)))
	q.Select = []federate.SelectItem{federate.Select(federate.Col("id"), "id")}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.Col("id"))}
	q.Limit = 5
	q.Offset = 3
	v := mustVerify(t, cat, q)
	if v.Plan.Filter.Pushed {
		t.Fatal("VOLATILE filter must stay at coordinator")
	}
	if v.Plan.TopK.Pushed {
		t.Fatal("top-k must not be pushed across a volatile filter")
	}

	qj := joinQuery("left_orders", "right_orders", federate.InnerJoin,
		federate.And(
			federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")),
			federate.Lt(federate.Func("random"), federate.Const(federate.Float(0.7))),
		))
	qj.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "id"),
		federate.Select(federate.ColOf("right_orders", "val"), "rv"),
	}
	qj.OrderBy = []federate.OrderItem{federate.Asc(federate.ColOf("left_orders", "id"))}
	vj := mustVerify(t, cat, qj)
	if vj.Plan.Join.Pushed {
		t.Fatal("join with VOLATILE ON predicate must not be pushed")
	}
}

func TestVolatileFilterWithAggregation(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 4), ordersRows(70)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Where = federate.Lt(federate.Func("random"), federate.Const(federate.Float(0.6)))
	q.Select = []federate.SelectItem{
		federate.Select(federate.Col("grp"), "grp"),
		federate.Select(federate.Count(nil), "n"),
	}
	q.GroupBy = []federate.Expr{federate.Col("grp")}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.Col("grp"))}
	v := mustVerify(t, cat, q)
	if v.Plan.Aggregation.Mode != federate.AggCoordinator {
		t.Fatalf("mode = %s, want coordinator because filter stays remote", v.Plan.Aggregation.Mode)
	}
}

func TestReplicatedTableAndReplicatedJoin(t *testing.T) {
	repl := singleTable(t, "regions", federate.ReplicatedDist(4), ordersRows(25))
	cat, err := federate.NewCatalog(repl)
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("regions")
	q.Select = []federate.SelectItem{
		federate.Select(federate.Col("grp"), "grp"),
		federate.Select(federate.Count(nil), "n"),
		federate.Select(federate.Avg(federate.Col("val")), "avg"),
	}
	q.GroupBy = []federate.Expr{federate.Col("grp")}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.Col("grp"))}
	v := mustVerify(t, cat, q)
	if v.Stats.ShardRowsScanned != 25 {
		t.Fatalf("replicated scan read %d rows, want 25 (one replica)", v.Stats.ShardRowsScanned)
	}
	if v.Plan.Aggregation.Mode != federate.AggSingleStage {
		t.Fatalf("mode = %s, want single_stage for replicated table", v.Plan.Aggregation.Mode)
	}

	left := singleTable(t, "left_orders", federate.HashDist([]string{"id"}, 4), ordersRows(30))
	right := singleTable(t, "right_orders", federate.ReplicatedDist(4), ordersRows(30))
	cat2, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	qj := joinQuery("left_orders", "right_orders", federate.InnerJoin,
		federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")))
	qj.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "id"),
		federate.Select(federate.ColOf("right_orders", "val"), "rv"),
	}
	qj.OrderBy = []federate.OrderItem{federate.Asc(federate.ColOf("left_orders", "id"))}
	vj := mustVerify(t, cat2, qj)
	if !vj.Plan.Join.Pushed {
		t.Fatalf("hash x replicated join should be pushed: %s", vj.Plan.Join.Reason)
	}
}

func TestGroupTopKAndFinalLimit(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 4), ordersRows(80)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{
		federate.Select(federate.Col("id"), "id"),
		federate.Select(federate.Count(nil), "n"),
	}
	q.GroupBy = []federate.Expr{federate.Col("id")}
	q.OrderBy = []federate.OrderItem{
		federate.Desc(federate.Count(nil)),
		federate.Asc(federate.Col("id")),
	}
	q.Limit = 4
	q.Offset = 6
	v := mustVerify(t, cat, q)
	if !v.Plan.TopK.Pushed || v.Plan.TopK.ShardLimit != 10 {
		t.Fatalf("co-located group top-k should push L+O=10: %+v", v.Plan.TopK)
	}

	q2 := federate.NewQuery("orders")
	q2.Select = []federate.SelectItem{
		federate.Select(federate.Col("grp"), "grp"),
		federate.Select(federate.Count(nil), "n"),
		federate.Select(federate.Avg(federate.Col("val")), "avg"),
	}
	q2.GroupBy = []federate.Expr{federate.Col("grp")}
	q2.Having = federate.Gt(federate.Count(nil), federate.Const(federate.Int(2)))
	q2.OrderBy = []federate.OrderItem{federate.Desc(federate.Count(nil)), federate.Asc(federate.Col("grp"))}
	q2.Limit = 3
	q2.Offset = 1
	v2 := mustVerify(t, cat, q2)
	if v2.Plan.TopK.Pushed {
		t.Fatal("partial/final aggregation must defer LIMIT to coordinator")
	}
}

func TestCountDistinctOnShardKeyCanBeDecomposed(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 4), ordersRows(50)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{
		federate.Select(federate.Col("grp"), "grp"),
		federate.Select(federate.CountDistinct(federate.Col("id")), "n"),
	}
	q.GroupBy = []federate.Expr{federate.Col("grp")}
	v := mustVerify(t, cat, q)
	for _, s := range v.Plan.Aggregation.Specs {
		if s.Agg.Distinct {
			if !s.Decomposable {
				t.Fatal("COUNT(DISTINCT shard_key) should be decomposable")
			}
			if s.Partial != federate.PartialCount || s.Final != federate.FinalSumCounts {
				t.Fatalf("COUNT(DISTINCT shard_key) rewrite = %s/%s", s.Partial, s.Final)
			}
		}
	}
}

func TestOrderByNulls(t *testing.T) {
	schema := &federate.Schema{Columns: []federate.Column{
		{Name: "id", Type: federate.TypeInt},
		{Name: "val", Type: federate.TypeInt, Nullable: true},
	}}
	rows := []federate.Row{
		row(federate.Int(1), federate.Int(5)),
		row(federate.Int(2), federate.Null()),
		row(federate.Int(3), federate.Int(1)),
		row(federate.Int(4), federate.Null()),
		row(federate.Int(5), federate.Int(5)),
		row(federate.Int(6), federate.Int(2)),
	}
	tbl, err := federate.NewTable("nulls", schema, federate.HashDist([]string{"id"}, 3), rows)
	if err != nil {
		t.Fatal(err)
	}
	cat, _ := federate.NewCatalog(tbl)
	nullsFirst := true
	for _, tc := range []struct {
		desc bool
		nf   *bool
	}{
		{false, nil},
		{true, nil},
		{false, &nullsFirst},
	} {
		q := federate.NewQuery("nulls")
		q.Select = []federate.SelectItem{
			federate.Select(federate.Col("id"), "id"),
			federate.Select(federate.Col("val"), "val"),
		}
		q.OrderBy = []federate.OrderItem{{Expr: federate.Col("val"), Desc: tc.desc, NullsFirst: tc.nf}}
		q.Limit = 4
		q.Offset = 1
		mustVerify(t, cat, q)
	}
}

func TestOrderBySelectAliasWithTopK(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 4), ordersRows(45)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{
		federate.Select(federate.Add(federate.Col("val"), federate.Col("id")), "score"),
		federate.Select(federate.Col("id"), "id"),
	}
	q.OrderBy = []federate.OrderItem{
		federate.Desc(federate.Col("score")),
		federate.Asc(federate.Col("id")),
	}
	q.Limit = 5
	q.Offset = 7
	v := mustVerify(t, cat, q)
	if !v.Plan.TopK.Pushed {
		t.Fatal("alias ORDER BY should still permit corrected top-k pushdown")
	}

	q2 := federate.NewQuery("orders")
	q2.Select = []federate.SelectItem{
		federate.Select(federate.Col("id"), "id"),
		federate.Select(federate.Count(nil), "n"),
	}
	q2.GroupBy = []federate.Expr{federate.Col("id")}
	q2.OrderBy = []federate.OrderItem{federate.Desc(federate.Col("n")), federate.Asc(federate.Col("id"))}
	q2.Limit = 3
	q2.Offset = 4
	mustVerify(t, cat, q2)
}

func TestEmptyTableGlobalAggregate(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 4), nil))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{
		federate.Select(federate.Count(nil), "n"),
		federate.Select(federate.Avg(federate.Col("val")), "avg"),
	}
	v := mustVerify(t, cat, q)
	if len(v.Reference.Rows) != 1 {
		t.Fatalf("global aggregate over empty input returned %d rows, want 1", len(v.Reference.Rows))
	}
	if !federate.Equal(v.Reference.Rows[0][0], federate.Int(0)) || !v.Reference.Rows[0][1].IsNull() {
		t.Fatalf("unexpected empty aggregate row: %v", v.Reference.Rows[0])
	}
}

func TestDifferentNodesBlockJoinPushdown(t *testing.T) {
	leftDist := federate.HashDist([]string{"id"}, 3)
	leftDist.Nodes = []string{"a", "b", "c"}
	rightDist := federate.HashDist([]string{"id"}, 3)
	rightDist.Nodes = []string{"c", "b", "a"}
	left := singleTable(t, "left_orders", leftDist, ordersRows(12))
	right := singleTable(t, "right_orders", rightDist, ordersRows(14))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("left_orders", "right_orders", federate.InnerJoin,
		federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "id"),
		federate.Select(federate.ColOf("right_orders", "val"), "rv"),
	}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.ColOf("left_orders", "id"))}
	v := mustVerify(t, cat, q)
	if v.Plan.Join.Pushed {
		t.Fatal("reversed shard placement must block join pushdown")
	}
}

func TestReplicatedLeftJoinNeedsOrder(t *testing.T) {
	left := singleTable(t, "left_orders", federate.ReplicatedDist(4), ordersRows(20))
	right := singleTable(t, "right_orders", federate.HashDist([]string{"id"}, 4), ordersRows(20))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	mk := func() *federate.Query {
		q := joinQuery("left_orders", "right_orders", federate.InnerJoin,
			federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")))
		q.Select = []federate.SelectItem{
			federate.Select(federate.ColOf("left_orders", "id"), "id"),
			federate.Select(federate.ColOf("right_orders", "val"), "rv"),
		}
		return q
	}
	q := mk()
	v := mustVerify(t, cat, q)
	if v.Plan.Join.Pushed {
		t.Fatal("replicated-left join without ORDER BY must not be pushed")
	}
	q2 := mk()
	q2.OrderBy = []federate.OrderItem{federate.Asc(federate.ColOf("left_orders", "id"))}
	q2.Limit = 5
	q2.Offset = 2
	v2 := mustVerify(t, cat, q2)
	if !v2.Plan.Join.Pushed {
		t.Fatalf("replicated-left join with ORDER BY can be pushed: %s", v2.Plan.Join.Reason)
	}
}

func TestStablePredicatePushdown(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 4), ordersRows(25)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Where = federate.Ge(federate.Col("id"), federate.Func("now"))
	q.Select = []federate.SelectItem{federate.Select(federate.Col("id"), "id")}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.Col("id"))}
	q.Limit = 4
	v := mustVerify(t, cat, q)
	if !v.Plan.Filter.Pushed || v.Plan.Filter.Shard == nil {
		t.Fatal("STABLE predicate should be safe to evaluate at shard scan")
	}
}

func TestReplicatedOuterJoinBlocked(t *testing.T) {
	left := singleTable(t, "left_orders", federate.ReplicatedDist(4), ordersRows(10))
	right := singleTable(t, "right_orders", federate.HashDist([]string{"id"}, 4), ordersRows(20))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("left_orders", "right_orders", federate.LeftJoin,
		federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "lid"),
		federate.Select(federate.ColOf("right_orders", "id"), "rid"),
	}
	q.OrderBy = []federate.OrderItem{
		federate.Asc(federate.ColOf("left_orders", "id")),
		federate.Asc(federate.ColOf("right_orders", "id")),
	}
	v := mustVerify(t, cat, q)
	if v.Plan.Join.Pushed {
		t.Fatal("outer join preserving a replicated side must not be pushed")
	}
}

func TestJoinEqualityMustCoverShardKey(t *testing.T) {
	left := singleTable(t, "left_orders", federate.HashDist([]string{"id"}, 4), ordersRows(25))
	right := singleTable(t, "right_orders", federate.HashDist([]string{"id"}, 4), ordersRows(25))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("left_orders", "right_orders", federate.InnerJoin,
		federate.Eq(federate.ColOf("left_orders", "grp"), federate.ColOf("right_orders", "grp")))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "lid"),
		federate.Select(federate.ColOf("right_orders", "id"), "rid"),
	}
	q.OrderBy = []federate.OrderItem{
		federate.Asc(federate.ColOf("left_orders", "id")),
		federate.Asc(federate.ColOf("right_orders", "id")),
	}
	v := mustVerify(t, cat, q)
	if v.Plan.Join.Pushed {
		t.Fatal("join on a non-shard key must not be pushed")
	}
}

func TestCompositeShardKeyJoin(t *testing.T) {
	dist := federate.HashDist([]string{"grp", "id"}, 4)
	left := singleTable(t, "left_orders", dist, ordersRows(25))
	right := singleTable(t, "right_orders", dist, ordersRows(25))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("left_orders", "right_orders", federate.InnerJoin,
		federate.And(
			federate.Eq(federate.ColOf("left_orders", "grp"), federate.ColOf("right_orders", "grp")),
			federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")),
		))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "id"),
		federate.Select(federate.ColOf("right_orders", "val"), "rv"),
	}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.ColOf("left_orders", "id"))}
	v := mustVerify(t, cat, q)
	if !v.Plan.Join.Pushed {
		t.Fatalf("composite shard-key join should be pushed: %s", v.Plan.Join.Reason)
	}
}

func TestTopKReducesTransfer(t *testing.T) {
	dist := federate.HashDist([]string{"id"}, 4)
	cat, err := federate.NewCatalog(singleTable(t, "orders", dist, ordersRows(400)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{federate.Select(federate.Col("id"), "id")}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.Col("id"))}
	q.Limit = 10
	q.Offset = 5
	v := mustVerify(t, cat, q)
	if v.Stats.RowsTransferred >= 400 {
		t.Fatalf("top-k pushdown transferred %d rows, expected far fewer than 400", v.Stats.RowsTransferred)
	}
}

func TestSumMinMaxTwoStage(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.RangeDist("id", federate.Int(15), federate.Int(30)), ordersRows(60)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{
		federate.Select(federate.Col("grp"), "grp"),
		federate.Select(federate.Sum(federate.Col("val")), "sum"),
		federate.Select(federate.Min(federate.Col("val")), "min"),
		federate.Select(federate.Max(federate.Col("val")), "max"),
	}
	q.GroupBy = []federate.Expr{federate.Col("grp")}
	q.Having = federate.Gt(federate.Sum(federate.Col("val")), federate.Const(federate.Int(0)))
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.Col("grp"))}
	v := mustVerify(t, cat, q)
	if v.Plan.Aggregation.Mode != federate.AggPartialFinal {
		t.Fatalf("mode = %s, want partial_final", v.Plan.Aggregation.Mode)
	}
}

func TestOuterJoinWhereOnNullSide(t *testing.T) {
	left := singleTable(t, "left_orders", federate.HashDist([]string{"id"}, 3), ordersRows(31))
	right := singleTable(t, "right_orders", federate.HashDist([]string{"id"}, 3), ordersRows(35))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("left_orders", "right_orders", federate.LeftJoin,
		federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")))
	q.Where = federate.Gt(federate.ColOf("right_orders", "val"), federate.Const(federate.Int(8)))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "lid"),
		federate.Select(federate.ColOf("right_orders", "val"), "rv"),
	}
	q.OrderBy = []federate.OrderItem{
		federate.Asc(federate.ColOf("left_orders", "id")),
		federate.Asc(federate.ColOf("right_orders", "val")),
	}
	q.Limit = 4
	q.Offset = 2
	v := mustVerify(t, cat, q)
	if v.Plan.Filter.Coordinator == nil {
		t.Fatal("right-side WHERE of a left join must stay at coordinator")
	}
	if v.Plan.TopK.Pushed {
		t.Fatal("top-k must wait for the coordinator-side outer-join filter")
	}
}

func TestOffsetOnlyAndZeroLimit(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 4), ordersRows(20)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Select = []federate.SelectItem{federate.Select(federate.Col("id"), "id")}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.Col("id"))}
	q.Offset = 15
	v := mustVerify(t, cat, q)
	if v.Plan.TopK.Pushed {
		t.Fatal("OFFSET without LIMIT should not push a shard limit")
	}
	if len(v.Distributed.Rows) != 5 {
		t.Fatalf("offset-only returned %d rows, want 5", len(v.Distributed.Rows))
	}

	q2 := federate.NewQuery("orders")
	q2.Select = []federate.SelectItem{federate.Select(federate.Col("id"), "id")}
	q2.OrderBy = []federate.OrderItem{federate.Asc(federate.Col("id"))}
	q2.Limit = 0
	q2.Offset = 7
	v2 := mustVerify(t, cat, q2)
	if len(v2.Distributed.Rows) != 0 {
		t.Fatalf("LIMIT 0 returned %d rows", len(v2.Distributed.Rows))
	}
}

func TestMixedVolatileAndImmutableConjuncts(t *testing.T) {
	cat, err := federate.NewCatalog(singleTable(t, "orders", federate.HashDist([]string{"id"}, 4), ordersRows(80)))
	if err != nil {
		t.Fatal(err)
	}
	q := federate.NewQuery("orders")
	q.Where = federate.And(
		federate.Gt(federate.Col("id"), federate.Const(federate.Int(10))),
		federate.Lt(federate.Func("random"), federate.Const(federate.Float(0.5))),
	)
	q.Select = []federate.SelectItem{federate.Select(federate.Col("id"), "id")}
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.Col("id"))}
	q.Limit = 5
	v := mustVerify(t, cat, q)
	if v.Plan.Filter.Shard == nil {
		t.Fatal("immutable conjunct should still be pushed")
	}
	if v.Plan.Filter.Coordinator == nil {
		t.Fatal("volatile conjunct must stay at coordinator")
	}
}

func TestFullJoinWithoutOrderFallsBack(t *testing.T) {
	left := singleTable(t, "left_orders", federate.HashDist([]string{"id"}, 3), ordersRows(15))
	right := singleTable(t, "right_orders", federate.HashDist([]string{"id"}, 3), ordersRows(20))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("left_orders", "right_orders", federate.FullJoin,
		federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "lid"),
		federate.Select(federate.ColOf("right_orders", "id"), "rid"),
	}
	v := mustVerify(t, cat, q)
	if v.Plan.Join.Pushed {
		t.Fatal("full join without ORDER BY must not be pushed")
	}
}

func TestReplicatedJoinTieOrderIsStable(t *testing.T) {
	left := singleTable(t, "left_orders", federate.ReplicatedDist(4), ordersRows(24))
	right := singleTable(t, "right_orders", federate.HashDist([]string{"id"}, 4), ordersRows(24))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("left_orders", "right_orders", federate.InnerJoin,
		federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "id")))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "id"),
		federate.Select(federate.ColOf("left_orders", "val"), "lv"),
		federate.Select(federate.ColOf("right_orders", "val"), "rv"),
	}
	// Deliberately order only on a low-cardinality expression. Many rows tie,
	// so the hidden deterministic tie-break must agree with the reference.
	q.OrderBy = []federate.OrderItem{federate.Asc(federate.ColOf("left_orders", "val"))}
	q.Limit = 6
	q.Offset = 5
	v := mustVerify(t, cat, q)
	if !v.Plan.Join.Pushed || !v.Plan.TopK.Pushed {
		t.Fatalf("expected pushed replicated-left join: %+v", v.Plan.Join)
	}
}

func TestCrossedCompositeJoinKeyBlocked(t *testing.T) {
	dist := federate.HashDist([]string{"grp", "id"}, 4)
	left := singleTable(t, "left_orders", dist, ordersRows(25))
	right := singleTable(t, "right_orders", dist, ordersRows(25))
	cat, err := federate.NewCatalog(left, right)
	if err != nil {
		t.Fatal(err)
	}
	q := joinQuery("left_orders", "right_orders", federate.InnerJoin,
		federate.And(
			federate.Eq(federate.ColOf("left_orders", "grp"), federate.ColOf("right_orders", "id")),
			federate.Eq(federate.ColOf("left_orders", "id"), federate.ColOf("right_orders", "grp")),
		))
	q.Select = []federate.SelectItem{
		federate.Select(federate.ColOf("left_orders", "id"), "lid"),
		federate.Select(federate.ColOf("right_orders", "id"), "rid"),
	}
	q.OrderBy = []federate.OrderItem{
		federate.Asc(federate.ColOf("left_orders", "id")),
		federate.Asc(federate.ColOf("right_orders", "id")),
	}
	v := mustVerify(t, cat, q)
	if v.Plan.Join.Pushed {
		t.Fatal("crossed composite shard-key join must not be pushed")
	}
}
