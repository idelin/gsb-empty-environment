package joinselim_test

import (
	"fmt"
	"testing"

	js "prescreen.local/ps-0f39c054e3/joinselim"
)

func assertEquivalent(t *testing.T, q js.Query, schema *js.Schema, data js.DataSet) *js.Result {
	t.Helper()
	result, err := js.Rewrite(q, schema)
	if err != nil {
		t.Fatalf("Rewrite returned error: %v", err)
	}
	before, err := js.Evaluate(q, data)
	if err != nil {
		t.Fatalf("evaluate original: %v\nquery=%#v", err, q)
	}
	after, err := js.Evaluate(result.Query, data)
	if err != nil {
		t.Fatalf("evaluate rewritten: %v\nquery=%#v", err, result.Query)
	}
	if !js.EqualMultiset(before, after) {
		t.Fatalf("multiset mismatch for data %#v\n%s\noriginal=%v\nrewritten=%v\nresult=%#v",
			data, js.MultisetDiff(before, after), before, after, result)
	}
	return result
}

func fkSchema(nullableFK bool) *js.Schema {
	return js.NewSchema().
		AddTable(js.Table{
			Name:       "p",
			Columns:    []js.Column{{Name: "id", Nullable: false}, {Name: "name", Nullable: true}},
			PrimaryKey: []string{"id"},
		}).
		AddTable(js.Table{
			Name:       "l",
			Columns:    []js.Column{{Name: "id", Nullable: false}, {Name: "pid", Nullable: nullableFK}},
			PrimaryKey: []string{"id"},
		}).
		AddForeignKey(js.ForeignKey{
			Name:              "l_pid_fk",
			Table:             "l",
			Columns:           []string{"pid"},
			ReferencedTable:   "p",
			ReferencedColumns: []string{"id"},
		}).
		AddFD(js.FunctionalDependency{Table: "p", LHS: []string{"id"}, RHS: []string{"name"}})
}

func allFKData(includeNullFK bool) []js.DataSet {
	parentIDSet := [][]int{{}, {1}, {2}, {1, 2}}
	var out []js.DataSet
	for _, ids := range parentIDSet {
		pRows := make([]js.Row, 0, len(ids))
		for _, id := range ids {
			pRows = append(pRows, js.Row{"id": id, "name": fmt.Sprintf("p%d", id)})
		}

		type pidChoice struct {
			absent bool
			value  any
		}
		choices := []pidChoice{{absent: true}}
		if includeNullFK {
			choices = append(choices, pidChoice{value: nil})
		}
		for _, id := range ids {
			choices = append(choices, pidChoice{value: id})
		}

		for _, c1 := range choices {
			for _, c2 := range choices {
				lRows := []js.Row{}
				if !c1.absent {
					lRows = append(lRows, js.Row{"id": 1, "pid": c1.value})
				}
				if !c2.absent {
					lRows = append(lRows, js.Row{"id": 2, "pid": c2.value})
				}
				out = append(out, js.DataSet{"p": pRows, "l": lRows})
			}
		}
	}
	return out
}

func fkQuery(withNotNull bool, includeParentID bool) js.Query {
	projection := []js.Ref{js.Col("l", "id"), js.Col("l", "pid")}
	if includeParentID {
		projection = append(projection, js.Col("p", "id"))
	}
	q := js.Query{
		From: js.TableRef{Alias: "l", Table: "l"},
		Joins: []js.Join{{
			Type:  js.InnerJoin,
			Alias: "p",
			Table: "p",
			On:    []js.Equality{{Left: js.Col("l", "pid"), Right: js.Col("p", "id")}},
		}},
		Projection: projection,
	}
	if withNotNull {
		q.Where = []js.Predicate{js.NotNull(js.Col("l", "pid"))}
	}
	return q
}

func TestNullableForeignKeyGateAndNotNullElimination(t *testing.T) {
	schema := fkSchema(true)
	q := fkQuery(false, false)

	blocked, err := js.Rewrite(q, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.RemovedAliases) != 0 || len(blocked.RetainedAliases) != 2 {
		t.Fatalf("nullable FK without IS NOT NULL must remain: retained=%v removed=%v",
			blocked.RetainedAliases, blocked.RemovedAliases)
	}

	instances := allFKData(true)
	if len(instances) == 0 {
		t.Fatal("no FK instances generated")
	}
	for i, data := range instances {
		assertEquivalent(t, q, schema, data)
		_ = i
	}

	// The inner equality itself rejects NULLs, but the package deliberately
	// follows the stated rewrite gate: without an explicit/schema non-null FK
	// proof it must not silently drop the filtering join.
	unsafe := q
	unsafe.Joins = nil
	nullInstance := js.DataSet{
		"l": []js.Row{{"id": 1, "pid": nil}},
		"p": []js.Row{{"id": 1}},
	}
	original, err := js.Evaluate(q, nullInstance)
	if err != nil {
		t.Fatal(err)
	}
	naive, err := js.Evaluate(unsafe, nullInstance)
	if err != nil {
		t.Fatal(err)
	}
	if js.EqualMultiset(original, naive) {
		t.Fatal("expected naive nullable-FK join removal to change the bag output")
	}

	nnQ := fkQuery(true, true)
	removed, err := js.Rewrite(nnQ, schema)
	if err != nil {
		t.Fatal(err)
	}
	if got := removed.RetainedAliases; len(got) != 1 || got[0] != "l" {
		t.Fatalf("expected only l retained, got %+v", got)
	}
	if removed.ColumnMap["p.id"] != "l.pid" {
		t.Fatalf("expected p.id -> l.pid mapping, got %q in %+v",
			removed.ColumnMap["p.id"], removed.ColumnMap)
	}
	if len(removed.Predicates) != 1 || removed.Predicates[0].Op != js.OpNotNull {
		t.Fatalf("expected IS NOT NULL predicate to remain, got %#v", removed.Predicates)
	}
	for _, data := range instances {
		assertEquivalent(t, nnQ, schema, data)
	}
}

func TestSchemaNotNullForeignKeyEliminatesExhaustively(t *testing.T) {
	schema := fkSchema(false)
	q := fkQuery(false, true)
	result, err := js.Rewrite(q, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedAliases) != 1 || result.RemovedAliases[0] != "p" {
		t.Fatalf("expected schema NOT NULL FK elimination, got %+v", result)
	}
	// Invalid NULL-FK instances are intentionally excluded.
	for _, data := range allFKData(false) {
		assertEquivalent(t, q, schema, data)
	}
}

func allSingleColumnBags(column string, values []any, maxMultiplicity int) []js.DataSet {
	rowTypes := make([]js.Row, len(values))
	for i, value := range values {
		rowTypes[i] = js.Row{column: value}
	}
	multiplicities := make([]int, len(rowTypes))
	var rows [][]js.Row
	var generate func(pos int)
	generate = func(pos int) {
		if pos == len(multiplicities) {
			var instance []js.Row
			for i, count := range multiplicities {
				for n := 0; n < count; n++ {
					instance = append(instance, cloneRow(rowTypes[i]))
				}
			}
			rows = append(rows, instance)
			return
		}
		for count := 0; count <= maxMultiplicity; count++ {
			multiplicities[pos] = count
			generate(pos + 1)
		}
	}
	generate(0)

	out := make([]js.DataSet, 0, len(rows)*len(rows))
	for _, leftRows := range rows {
		for _, rightRows := range rows {
			out = append(out, js.DataSet{"l": leftRows, "r": rightRows})
		}
	}
	return out
}

func cloneRow(in js.Row) js.Row {
	out := js.Row{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func noUniqueSchema() *js.Schema {
	return js.NewSchema().
		AddTable(js.Table{Name: "l", Columns: []js.Column{{Name: "k", Nullable: true}}}).
		AddTable(js.Table{Name: "r", Columns: []js.Column{{Name: "k", Nullable: true}}})
}

func leftNoUniqueQuery(distinct bool, projectRight bool) js.Query {
	projection := []js.Ref{js.Col("x", "k")}
	if projectRight {
		projection = append(projection, js.Col("y", "k"))
	}
	return js.Query{
		From: js.TableRef{Alias: "x", Table: "l"},
		Joins: []js.Join{{
			Type:  js.LeftJoin,
			Alias: "y",
			Table: "r",
			On:    []js.Equality{{Left: js.Col("x", "k"), Right: js.Col("y", "k")}},
		}},
		Distinct:   distinct,
		Projection: projection,
	}
}

func TestLeftJoinWithoutUniqueKeyBagVersusDistinct(t *testing.T) {
	schema := noUniqueSchema()
	bagQ := leftNoUniqueQuery(false, false)
	bagResult, err := js.Rewrite(bagQ, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(bagResult.RemovedAliases) != 0 {
		t.Fatalf("non-unique left join must not be removed in BAG mode: %+v", bagResult.RemovedAliases)
	}

	distinctQ := leftNoUniqueQuery(true, false)
	distinctResult, err := js.Rewrite(distinctQ, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(distinctResult.RemovedAliases) != 1 || distinctResult.RemovedAliases[0] != "y" {
		t.Fatalf("DISTINCT should permit ghost left-join removal: %+v", distinctResult)
	}

	instances := allSingleColumnBags("k", []any{nil, 1, 2}, 2)
	for _, data := range instances {
		assertEquivalent(t, bagQ, schema, data)
		assertEquivalent(t, distinctQ, schema, data)
	}

	doubling := js.DataSet{
		"l": []js.Row{{"k": 1}},
		"r": []js.Row{{"k": 1}, {"k": 1}},
	}
	original, err := js.Evaluate(bagQ, doubling)
	if err != nil {
		t.Fatal(err)
	}
	if got := js.Multiset(original)["[i:1;]"]; got != 2 {
		t.Fatalf("expected join to double row to multiplicity 2, got %d (%v)", got, original)
	}
	naive := bagQ
	naive.Joins = nil
	naiveRows, err := js.Evaluate(naive, doubling)
	if err != nil {
		t.Fatal(err)
	}
	if js.EqualMultiset(original, naiveRows) {
		t.Fatal("expected naive removal of non-unique BAG left join to change multiplicities")
	}

	projectRightQ := leftNoUniqueQuery(false, true)
	blocked, err := js.Rewrite(projectRightQ, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.RemovedAliases) != 0 {
		t.Fatalf("left join with referenced right column must not be removed: %+v", blocked.RemovedAliases)
	}
	for _, data := range instances {
		assertEquivalent(t, projectRightQ, schema, data)
	}
}

func TestLeftJoinUniqueKeyExhaustive(t *testing.T) {
	schema := js.NewSchema().
		AddTable(js.Table{Name: "l", Columns: []js.Column{{Name: "k", Nullable: true}}}).
		AddTable(js.Table{
			Name:       "r",
			Columns:    []js.Column{{Name: "id", Nullable: false}},
			PrimaryKey: []string{"id"},
		})
	q := js.Query{
		From: js.TableRef{Alias: "x", Table: "l"},
		Joins: []js.Join{{
			Type:  js.LeftJoin,
			Alias: "y",
			Table: "r",
			On:    []js.Equality{{Left: js.Col("x", "k"), Right: js.Col("y", "id")}},
		}},
		Projection: []js.Ref{js.Col("x", "k")},
	}
	result, err := js.Rewrite(q, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedAliases) != 1 || result.Eliminations[0].Rule != "left-outer-unique-key" {
		t.Fatalf("expected unique left elimination, got %+v", result.Eliminations)
	}

	leftBags := allSingleColumnBags("k", []any{nil, 1, 2}, 2)
	for _, data := range leftBags {
		// allSingleColumnBags creates both l and r; replace r with valid PK subsets.
		for _, ids := range [][]int{{}, {1}, {2}, {1, 2}} {
			pkData := js.DataSet{"l": data["l"]}
			for _, id := range ids {
				pkData["r"] = append(pkData["r"], js.Row{"id": id})
			}
			assertEquivalent(t, q, schema, pkData)
		}
	}
}

func selfSchema() *js.Schema {
	return js.NewSchema().AddTable(js.Table{
		Name:       "t",
		Columns:    []js.Column{{Name: "k", Nullable: false}, {Name: "v", Nullable: true}},
		PrimaryKey: []string{"k"},
	})
}

type absentValue struct{}

func allSelfInstances() []js.DataSet {
	choices := []any{absentValue{}, nil, 0, 1}
	var out []js.DataSet
	for _, v1 := range choices {
		for _, v2 := range choices {
			var rows []js.Row
			if _, absent := v1.(absentValue); !absent {
				rows = append(rows, js.Row{"k": 1, "v": v1})
			}
			if _, absent := v2.(absentValue); !absent {
				rows = append(rows, js.Row{"k": 2, "v": v2})
			}
			out = append(out, js.DataSet{"t": rows})
		}
	}
	return out
}

func selfJoinQuery(where ...js.Predicate) js.Query {
	return js.Query{
		From: js.TableRef{Alias: "a", Table: "t"},
		Joins: []js.Join{{
			Type:  js.InnerJoin,
			Alias: "b",
			Table: "t",
			On:    []js.Equality{{Left: js.Col("a", "k"), Right: js.Col("b", "k")}},
		}},
		Projection: []js.Ref{
			js.Col("a", "k"), js.Col("a", "v"),
			js.Col("b", "k"), js.Col("b", "v"),
		},
		Where: where,
	}
}

func TestSelfJoinCollapseMergesFiltersExhaustively(t *testing.T) {
	schema := selfSchema()
	filterSets := [][]js.Predicate{
		nil,
		{js.EqualValue(js.Col("a", "v"), 0)},
		{js.EqualValue(js.Col("b", "v"), 1)},
		{js.EqualValue(js.Col("a", "v"), 0), js.EqualValue(js.Col("b", "v"), 1)},
		{js.NotNull(js.Col("b", "v"))},
	}
	for fi, where := range filterSets {
		q := selfJoinQuery(where...)
		result, err := js.Rewrite(q, schema)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.RemovedAliases) != 1 || result.RemovedAliases[0] != "b" {
			t.Fatalf("filter set %d: expected self collapse, got %+v", fi, result)
		}
		if result.ColumnMap["b.v"] != "a.v" || result.ColumnMap["b.k"] != "a.k" {
			t.Fatalf("filter set %d: bad self mapping %+v", fi, result.ColumnMap)
		}
		for _, data := range allSelfInstances() {
			assertEquivalent(t, q, schema, data)
		}
	}
}

func nullableSelfSchema() *js.Schema {
	return js.NewSchema().AddTable(js.Table{
		Name:       "u",
		Columns:    []js.Column{{Name: "k", Nullable: true}},
		UniqueKeys: [][]string{{"k"}},
	})
}

func allNullableSelfInstances() []js.DataSet {
	var out []js.DataSet
	for mask := 0; mask < 4; mask++ {
		for nullMultiplicity := 0; nullMultiplicity <= 2; nullMultiplicity++ {
			var rows []js.Row
			if mask&1 != 0 {
				rows = append(rows, js.Row{"k": 1})
			}
			if mask&2 != 0 {
				rows = append(rows, js.Row{"k": 2})
			}
			for i := 0; i < nullMultiplicity; i++ {
				rows = append(rows, js.Row{"k": nil})
			}
			out = append(out, js.DataSet{"u": rows})
		}
	}
	return out
}

func nullableSelfQuery(where ...js.Predicate) js.Query {
	return js.Query{
		From: js.TableRef{Alias: "a", Table: "u"},
		Joins: []js.Join{{
			Type:  js.InnerJoin,
			Alias: "b",
			Table: "u",
			On:    []js.Equality{{Left: js.Col("a", "k"), Right: js.Col("b", "k")}},
		}},
		Projection: []js.Ref{js.Col("a", "k"), js.Col("b", "k")},
		Where:      where,
	}
}

func TestNullableSelfJoinRequiresNonNullProof(t *testing.T) {
	schema := nullableSelfSchema()
	q := nullableSelfQuery()
	blocked, err := js.Rewrite(q, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.RemovedAliases) != 0 {
		t.Fatalf("nullable unique self key must not collapse without proof: %+v", blocked.RemovedAliases)
	}
	for _, data := range allNullableSelfInstances() {
		assertEquivalent(t, q, schema, data)
	}

	for _, predicate := range []js.Predicate{
		js.NotNull(js.Col("a", "k")),
		js.NotNull(js.Col("b", "k")),
	} {
		nnQ := nullableSelfQuery(predicate)
		result, err := js.Rewrite(nnQ, schema)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.RemovedAliases) != 1 {
			t.Fatalf("expected non-null proof to allow collapse: %#v", result)
		}
		for _, data := range allNullableSelfInstances() {
			assertEquivalent(t, nnQ, schema, data)
		}
	}
}

func chainSchema() *js.Schema {
	return js.NewSchema().
		AddTable(js.Table{
			Name:       "c",
			Columns:    []js.Column{{Name: "id", Nullable: false}},
			PrimaryKey: []string{"id"},
		}).
		AddTable(js.Table{
			Name: "b",
			Columns: []js.Column{
				{Name: "id", Nullable: false},
				{Name: "cid", Nullable: false},
			},
			PrimaryKey: []string{"id"},
		}).
		AddTable(js.Table{
			Name: "a",
			Columns: []js.Column{
				{Name: "id", Nullable: false},
				{Name: "bid", Nullable: false},
			},
			PrimaryKey: []string{"id"},
		}).
		AddForeignKey(js.ForeignKey{
			Table: "b", Columns: []string{"cid"},
			ReferencedTable: "c", ReferencedColumns: []string{"id"},
		}).
		AddForeignKey(js.ForeignKey{
			Table: "a", Columns: []string{"bid"},
			ReferencedTable: "b", ReferencedColumns: []string{"id"},
		})
}

func allChainData() []js.DataSet {
	var out []js.DataSet
	for _, cIDs := range [][]int{{}, {1}, {2}, {1, 2}} {
		cRows := []js.Row{}
		for _, id := range cIDs {
			cRows = append(cRows, js.Row{"id": id})
		}

		// option 0 means absent; options 1..len(cIDs) reference cIDs[option-1].
		for b1Opt := 0; b1Opt <= len(cIDs); b1Opt++ {
			for b2Opt := 0; b2Opt <= len(cIDs); b2Opt++ {
				bRows := []js.Row{}
				bIDs := []int{}
				addB := func(id, opt int) {
					if opt == 0 {
						return
					}
					cid := cIDs[opt-1]
					bRows = append(bRows, js.Row{"id": id, "cid": cid})
					bIDs = append(bIDs, id)
				}
				addB(1, b1Opt)
				addB(2, b2Opt)

				for a1Opt := 0; a1Opt <= len(bIDs); a1Opt++ {
					for a2Opt := 0; a2Opt <= len(bIDs); a2Opt++ {
						aRows := []js.Row{}
						addA := func(id, opt int) {
							if opt == 0 {
								return
							}
							bid := bIDs[opt-1]
							aRows = append(aRows, js.Row{"id": id, "bid": bid})
						}
						addA(1, a1Opt)
						addA(2, a2Opt)
						out = append(out, js.DataSet{"c": cRows, "b": bRows, "a": aRows})
					}
				}
			}
		}
	}
	return out
}

func chainQuery(projection ...js.Ref) js.Query {
	if len(projection) == 0 {
		projection = []js.Ref{js.Col("a", "id"), js.Col("a", "bid")}
	}
	return js.Query{
		From: js.TableRef{Alias: "a", Table: "a"},
		Joins: []js.Join{
			{
				Type: js.InnerJoin, Alias: "b", Table: "b",
				On: []js.Equality{{Left: js.Col("a", "bid"), Right: js.Col("b", "id")}},
			},
			{
				Type: js.InnerJoin, Alias: "c", Table: "c",
				On: []js.Equality{{Left: js.Col("b", "cid"), Right: js.Col("c", "id")}},
			},
		},
		Projection: projection,
	}
}

func TestChainForeignKeyCascadeExhaustive(t *testing.T) {
	schema := chainSchema()
	q := chainQuery()
	result, err := js.Rewrite(q, schema)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.RetainedAliases; len(got) != 1 || got[0] != "a" {
		t.Fatalf("expected cascade to retain only a, got %+v", result)
	}
	if len(result.Eliminations) != 2 {
		t.Fatalf("expected two eliminations, got %#v", result.Eliminations)
	}
	instances := allChainData()
	if len(instances) != 90 {
		t.Fatalf("expected 90 exhaustive valid chain instances, got %d", len(instances))
	}
	for _, data := range instances {
		assertEquivalent(t, q, schema, data)
	}

	bIDQ := chainQuery(js.Col("a", "id"), js.Col("b", "id"))
	bIDResult := assertEquivalent(t, bIDQ, schema, allChainData()[0])
	if len(bIDResult.RetainedAliases) != 1 {
		t.Fatalf("b.id should remap to a.bid and allow cascade: %+v", bIDResult)
	}
	for _, data := range instances {
		assertEquivalent(t, bIDQ, schema, data)
	}

	cIDQ := chainQuery(js.Col("a", "id"), js.Col("c", "id"))
	cIDResult, err := js.Rewrite(cIDQ, schema)
	if err != nil {
		t.Fatal(err)
	}
	if got := cIDResult.RetainedAliases; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("c.id maps to b.cid, so b must stay: %+v", got)
	}
	if cIDResult.ColumnMap["c.id"] != "b.cid" {
		t.Fatalf("expected c.id -> b.cid, got %q", cIDResult.ColumnMap["c.id"])
	}
	for _, data := range instances {
		assertEquivalent(t, cIDQ, schema, data)
	}
}
