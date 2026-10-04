package main

import (
	"strings"
	"testing"
)

func eq(l, r Expr) Expr    { return Cmp{Op: "eq", L: l, R: r} }
func col(name string) Expr { return Col{Name: name} }
func lit(i int64) Expr     { return Lit{V: IntV(i)} }

func corrExists(extra ...Expr) Plan {
	preds := append([]Expr{eq(col("S.c"), col("R.a"))}, extra...)
	return Filter{Input: Scan{Table: "R"}, Pred: Exists{Sub: Filter{Input: Scan{Table: "S"}, Pred: andOf(preds)}}}
}

func corrCount(arg string, extra ...Expr) Plan {
	preds := append([]Expr{eq(col("S.c"), col("R.a"))}, extra...)
	return Project{Input: Scan{Table: "R"}, Cols: []ProjCol{
		{Name: "a", Expr: col("R.a")},
		{Name: "cnt", Expr: CountSubq{Arg: arg, Sub: Filter{Input: Scan{Table: "S"}, Pred: andOf(preds)}}},
	}}
}

func twoFactCount() Plan {
	return Project{Input: Scan{Table: "R"}, Cols: []ProjCol{
		{Name: "a", Expr: col("R.a")},
		{Name: "cnt_s", Expr: CountSubq{Arg: "*", Sub: Filter{Input: Scan{Table: "S"}, Pred: eq(col("S.c"), col("R.a"))}}},
		{Name: "cnt_t", Expr: CountSubq{Arg: "*", Sub: Filter{Input: Scan{Table: "T"}, Pred: eq(col("T.e"), col("R.a"))}}},
	}}
}

func checkEquivalent(t *testing.T, env *Env, plan Plan) []Row {
	t.Helper()
	rewritten, _, err := Rewrite(plan, env)
	if err != nil {
		t.Fatalf("rewrite rejected: %v", err)
	}
	_, origRows, err := evalPlan(plan, env, nil)
	if err != nil {
		t.Fatalf("original plan failed: %v", err)
	}
	_, rwRows, err := evalPlan(rewritten, env, nil)
	if err != nil {
		t.Fatalf("rewritten plan failed: %v", err)
	}
	if bagOf(origRows) != bagOf(rwRows) {
		t.Fatalf("rewritten plan changed results:\nplan:  %s\norig:  %v\nrewr:  %v\nenv:   %+v",
			planString(plan), bagOfStrings(origRows), bagOfStrings(rwRows), env.Tables)
	}
	return origRows
}

func TestEmptyTables(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}, Rows: []Row{{IntV(1)}, {IntV(2)}}},
		"S": {Cols: []string{"c", "d"}},
	}}
	rows := checkEquivalent(t, env, corrCount("*"))
	assertBag(t, rows, "1,0", "2,0")
	rows = checkEquivalent(t, env, corrExists())
	assertBag(t, rows)

	env2 := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}},
		"S": {Cols: []string{"c", "d"}, Rows: []Row{{IntV(1), IntV(1)}}},
	}}
	checkEquivalent(t, env2, corrCount("*"))
	checkEquivalent(t, env2, corrExists())
}

func TestDuplicateKeys(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}, Rows: []Row{{IntV(1)}, {IntV(1)}}},
		"S": {Cols: []string{"c", "d"}, Rows: []Row{{IntV(1), IntV(5)}, {IntV(1), IntV(6)}}},
	}}
	rows := checkEquivalent(t, env, corrCount("*"))
	assertBag(t, rows, "1,2", "1,2")
	rows = checkEquivalent(t, env, corrExists())
	assertBag(t, rows, "1", "1")
}

func TestNullEqualityNeverMatches(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}, Rows: []Row{{NullV()}, {IntV(1)}}},
		"S": {Cols: []string{"c", "d"}, Rows: []Row{{NullV(), IntV(5)}, {IntV(1), IntV(7)}}},
	}}
	rows := checkEquivalent(t, env, corrCount("*"))
	assertBag(t, rows, "NULL,0", "1,1")
	rows = checkEquivalent(t, env, corrExists())
	assertBag(t, rows, "1")
	rows = checkEquivalent(t, env, corrCount("S.d"))
	assertBag(t, rows, "NULL,0", "1,1")
}

func TestCountColumnSkipsNulls(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}, Rows: []Row{{IntV(1)}}},
		"S": {Cols: []string{"c", "d"}, Rows: []Row{{IntV(1), NullV()}, {IntV(1), NullV()}, {IntV(1), IntV(9)}}},
	}}
	rows := checkEquivalent(t, env, corrCount("*"))
	assertBag(t, rows, "1,3")
	rows = checkEquivalent(t, env, corrCount("S.d"))
	assertBag(t, rows, "1,1")
}

func TestInnerPredicateInSubquery(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}, Rows: []Row{{IntV(1)}, {IntV(2)}}},
		"S": {Cols: []string{"c", "d"}, Rows: []Row{{IntV(1), IntV(1)}, {IntV(1), IntV(2)}, {IntV(2), IntV(1)}}},
	}}
	rows := checkEquivalent(t, env, corrCount("*", eq(col("S.d"), lit(1))))
	assertBag(t, rows, "1,1", "2,1")
	rows = checkEquivalent(t, env, corrExists(eq(col("S.d"), lit(2))))
	assertBag(t, rows, "1")
}

func TestOnVsWhereDiffer(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}, Rows: []Row{{IntV(1)}}},
		"S": {Cols: []string{"c", "d"}, Rows: []Row{{IntV(2), IntV(1)}}},
	}}
	cond := eq(col("S.d"), lit(1))
	onPlan := Join{Kind: LeftJoin, Left: Scan{Table: "R"}, Right: Scan{Table: "S"},
		On: AndE{Args: []Expr{eq(col("R.a"), col("S.c")), cond}}}
	wherePlan := Filter{
		Input: Join{Kind: LeftJoin, Left: Scan{Table: "R"}, Right: Scan{Table: "S"},
			On: eq(col("R.a"), col("S.c"))},
		Pred: cond}
	_, onRows, err := evalPlan(onPlan, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, whereRows, err := evalPlan(wherePlan, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertBag(t, onRows, "1,NULL,NULL")
	assertBag(t, whereRows)
	if bagOf(onRows) == bagOf(whereRows) {
		t.Fatal("ON vs WHERE should differ for LEFT JOIN")
	}
}

func TestTwoFactTablesNoMultiplication(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}, Rows: []Row{{IntV(1)}}},
		"S": {Cols: []string{"c", "d"}, Rows: []Row{{IntV(1), IntV(10)}, {IntV(1), IntV(20)}}},
		"T": {Cols: []string{"e"}, Rows: []Row{{IntV(1)}, {IntV(1)}, {IntV(1)}}},
	}}
	rows := checkEquivalent(t, env, twoFactCount())
	assertBag(t, rows, "1,2,3")
}

func TestRewriteRejections(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a", "b"}, Rows: []Row{{IntV(1), IntV(1)}}},
		"S": {Cols: []string{"c", "d"}, Rows: []Row{{IntV(1), IntV(1)}}},
	}}
	cases := []struct {
		name string
		plan Plan
		want string
	}{
		{"non-equality correlation",
			Filter{Input: Scan{Table: "R"}, Pred: Exists{Sub: Filter{Input: Scan{Table: "S"}, Pred: Cmp{Op: "gt", L: col("S.c"), R: col("R.a")}}}},
			"等值"},
		{"exists under OR",
			Filter{Input: Scan{Table: "R"}, Pred: OrE{Args: []Expr{
				Exists{Sub: Filter{Input: Scan{Table: "S"}, Pred: eq(col("S.c"), col("R.a"))}},
				eq(col("R.a"), lit(1))}}},
			"OR"},
		{"exists under NOT",
			Filter{Input: Scan{Table: "R"}, Pred: NotE{Arg: Exists{Sub: Filter{Input: Scan{Table: "S"}, Pred: eq(col("S.c"), col("R.a"))}}}},
			"NOT"},
		{"count in predicate",
			Filter{Input: Scan{Table: "R"}, Pred: Cmp{Op: "gt",
				L: CountSubq{Arg: "*", Sub: Filter{Input: Scan{Table: "S"}, Pred: eq(col("S.c"), col("R.a"))}},
				R: lit(0)}},
			"COUNT"},
		{"project inside subquery",
			Filter{Input: Scan{Table: "R"}, Pred: Exists{Sub: Project{
				Input: Filter{Input: Scan{Table: "S"}, Pred: eq(col("S.c"), col("R.a"))},
				Cols:  []ProjCol{{Name: "c", Expr: col("S.c")}}}}},
			"FILTER over SCAN"},
		{"outer-only predicate",
			Filter{Input: Scan{Table: "R"}, Pred: Exists{Sub: Filter{Input: Scan{Table: "S"}, Pred: eq(col("R.a"), col("R.b"))}}},
			"外层"},
		{"count nested in expression",
			Project{Input: Scan{Table: "R"}, Cols: []ProjCol{{Name: "x", Expr: Coalesce{Args: []Expr{
				CountSubq{Arg: "*", Sub: Filter{Input: Scan{Table: "S"}, Pred: eq(col("S.c"), col("R.a"))}}, lit(0)}}}}},
			"嵌套"},
		{"correlation with expression",
			Filter{Input: Scan{Table: "R"}, Pred: Exists{Sub: Filter{Input: Scan{Table: "S"},
				Pred: eq(col("S.c"), Coalesce{Args: []Expr{col("R.a"), lit(0)}})}}},
			"列引用"},
	}
	for _, tc := range cases {
		_, _, err := Rewrite(tc.plan, env)
		if err == nil {
			t.Errorf("%s: expected rejection, got success", tc.name)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: rejection %q should contain %q", tc.name, err.Error(), tc.want)
		}
	}
}

func multisets(universe []Row, maxSize int) [][]Row {
	var out [][]Row
	var rec func(start int, cur []Row)
	rec = func(start int, cur []Row) {
		out = append(out, append([]Row(nil), cur...))
		if len(cur) == maxSize {
			return
		}
		for i := start; i < len(universe); i++ {
			rec(i, append(cur, universe[i]))
		}
	}
	rec(0, nil)
	return out
}

func TestExhaustiveSmallDomain(t *testing.T) {
	dom := []Value{NullV(), IntV(0), IntV(1)}
	var u1, u2 []Row
	for _, a := range dom {
		u1 = append(u1, Row{a})
	}
	for _, c := range dom {
		for _, d := range dom {
			u2 = append(u2, Row{c, d})
		}
	}
	rBags := multisets(u1, 2)
	sBags := multisets(u2, 2)
	tBags := multisets(u1, 2)

	shapes := []struct {
		name  string
		build func() Plan
	}{
		{"exists", func() Plan { return corrExists() }},
		{"count_star", func() Plan { return corrCount("*") }},
		{"count_col", func() Plan { return corrCount("S.d") }},
		{"exists_inner_pred", func() Plan { return corrExists(eq(col("S.d"), lit(1))) }},
		{"count_inner_pred", func() Plan { return corrCount("S.d", eq(col("S.d"), lit(1))) }},
	}
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			for _, rb := range rBags {
				for _, sb := range sBags {
					env := &Env{Tables: map[string]Table{
						"R": {Cols: []string{"a"}, Rows: rb},
						"S": {Cols: []string{"c", "d"}, Rows: sb},
					}}
					checkEquivalent(t, env, sh.build())
				}
			}
		})
	}

	t.Run("two_fact_counts", func(t *testing.T) {
		for _, rb := range rBags {
			for _, sb := range sBags {
				for _, tb := range tBags {
					env := &Env{Tables: map[string]Table{
						"R": {Cols: []string{"a"}, Rows: rb},
						"S": {Cols: []string{"c", "d"}, Rows: sb},
						"T": {Cols: []string{"e"}, Rows: tb},
					}}
					checkEquivalent(t, env, twoFactCount())
				}
			}
		}
	})
}
