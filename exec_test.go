package cteopt

import (
	"fmt"
	"sort"
	"testing"
)

func sortedStrings(rows []Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprint(r))
	}
	sort.Strings(out)
	return out
}

func TestMaterializedExecutionMatchesInlined(t *testing.T) {
	tables := DemoTables()
	root, _ := BuildDemoPlan()
	optimized := (&Optimizer{Tables: tables}).Optimize(root)

	inlineRows := NewExecutor(tables).Collect(root)
	matRows := NewExecutor(tables).Collect(optimized)

	a, b := sortedStrings(inlineRows), sortedStrings(matRows)
	if len(a) != len(b) {
		t.Fatalf("row count differs: inlined=%d materialized=%d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("row %d differs: %q vs %q", i, a[i], b[i])
		}
	}
	if len(a) != 8 {
		t.Fatalf("expected 8 result rows (4 regions x 2 branches), got %d", len(a))
	}
}

func TestMaterializeSavesRecomputation(t *testing.T) {
	tables := DemoTables()
	root, cte := BuildDemoPlan()
	optimized := (&Optimizer{Tables: tables}).Optimize(root)

	inlineExec := NewExecutor(tables)
	inlineExec.Collect(root)
	matExec := NewExecutor(tables)
	matExec.Collect(optimized)

	if got := inlineExec.Stats.Opens[cte]; got != 2 {
		t.Fatalf("inlined CTE should be executed twice, opened %d times", got)
	}
	if got := matExec.Stats.Opens[cte]; got != 1 {
		t.Fatalf("materialized CTE should be executed once, opened %d times", got)
	}
	if got := matExec.Stats.RowsProduced[cte]; got != inlineExec.Stats.RowsProduced[cte]/2 {
		t.Fatalf("materialized CTE should produce rows once: got %d, inlined produced %d",
			got, inlineExec.Stats.RowsProduced[cte])
	}

	// The Materialize node itself serves both readers.
	var mats []*Node
	findOp(optimized, OpMaterialize, &mats)
	if got := matExec.Stats.Opens[mats[0]]; got != 2 {
		t.Fatalf("Materialize buffer should be read twice, opened %d times", got)
	}
}

func TestExecutorOperators(t *testing.T) {
	tables := map[string][]Row{
		"t": {Row{1, "a"}, Row{2, "b"}, Row{3, "a"}},
	}
	plan := Aggregate("count per letter",
		func(r Row) any { return r[1] }, "count", 0,
		Filter("x > 1", func(r Row) bool { return r[0].(int) > 1 }, Scan("t")))
	got := sortedStrings(NewExecutor(tables).Collect(plan))
	want := []string{"[a 1]", "[b 1]"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
