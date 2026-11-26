package joinselim_test

import (
	"testing"

	js "prescreen.local/ps-0f39c054e3/joinselim"
)

func TestForeignKeyKeyReferencesAndOnFiltersRemap(t *testing.T) {
	schema := fkSchema(true)
	q := js.Query{
		From: js.TableRef{Alias: "l", Table: "l"},
		Joins: []js.Join{{
			Type:  js.InnerJoin,
			Alias: "p",
			Table: "p",
			On: []js.Equality{
				{Left: js.Col("l", "pid"), Right: js.Col("p", "id")},
			},
			OnPredicates: []js.Predicate{
				js.CompareValue(js.OpGE, js.Col("p", "id"), 1),
			},
		}},
		Projection: []js.Ref{js.Col("l", "id"), js.Col("p", "id")},
		Where:      []js.Predicate{js.NotNull(js.Col("l", "pid"))},
	}

	result, err := js.Rewrite(q, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedAliases) != 1 {
		t.Fatalf("expected FK elimination with key-column predicate remap: %#v", result)
	}
	if result.ColumnMap["p.id"] != "l.pid" {
		t.Fatalf("bad p.id mapping: %+v", result.ColumnMap)
	}
	foundMappedFilter := false
	for _, p := range result.Predicates {
		if p.Op == js.OpGE && p.Col == js.Col("l", "pid") {
			foundMappedFilter = true
		}
	}
	if !foundMappedFilter {
		t.Fatalf("expected mapped p.id >= ? predicate, got %#v", result.Predicates)
	}
	for _, data := range allFKData(true) {
		assertEquivalent(t, q, schema, data)
	}
}

func TestForeignKeyNonKeyParentReferenceBlocks(t *testing.T) {
	schema := fkSchema(true)
	q := fkQuery(true, false)
	q.Projection = append(q.Projection, js.Col("p", "name"))
	result, err := js.Rewrite(q, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedAliases) != 0 {
		t.Fatalf("parent non-key column must block FK elimination: %#v", result)
	}
	for _, data := range allFKData(true) {
		assertEquivalent(t, q, schema, data)
	}

	q = fkQuery(true, false)
	q.Joins[0].OnPredicates = []js.Predicate{js.NotNull(js.Col("p", "name"))}
	result, err = js.Rewrite(q, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedAliases) != 0 {
		t.Fatalf("predicate on unmanaged parent column must block elimination: %#v", result)
	}
	for _, data := range allFKData(true) {
		assertEquivalent(t, q, schema, data)
	}
}

func TestFDClosureAvailable(t *testing.T) {
	schema := chainSchema().AddFD(js.FunctionalDependency{
		Table: "b", LHS: []string{"id"}, RHS: []string{"cid"},
	})
	got := schema.FDClosure("b", []string{"id"})
	if !stringSetEqual(got, []string{"id", "cid"}) {
		t.Fatalf("unexpected FD closure: %v", got)
	}
}

func stringSetEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	m := map[string]int{}
	for _, s := range got {
		m[s]++
	}
	for _, s := range want {
		m[s]--
		if m[s] < 0 {
			return false
		}
	}
	return true
}
