package main

import (
	"sort"
	"strings"
	"testing"
)

func TestTriLogic(t *testing.T) {
	if And(True, Unknown) != Unknown || And(Unknown, False) != False || And(True, True) != True || And(False, True) != False {
		t.Error("AND truth table wrong")
	}
	if Or(False, Unknown) != Unknown || Or(Unknown, True) != True || Or(False, False) != False || Or(True, False) != True {
		t.Error("OR truth table wrong")
	}
	if Not(Unknown) != Unknown || Not(True) != False || Not(False) != True {
		t.Error("NOT truth table wrong")
	}
}

func TestWhereKeepsOnlyTrue(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}, Rows: []Row{{IntV(1)}, {NullV()}, {IntV(2)}}},
	}}
	plan := Filter{Input: Scan{Table: "R"}, Pred: Cmp{Op: "gt", L: Col{Name: "R.a"}, R: Lit{V: IntV(1)}}}
	_, rows, err := evalPlan(plan, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0][0] != IntV(2) {
		t.Fatalf("expected only [2] (NULL comparison is unknown, dropped), got %v", rows)
	}
}

func TestCountStarVsCountColumn(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"S": {Cols: []string{"c", "d"}, Rows: []Row{{IntV(1), NullV()}, {IntV(1), IntV(5)}, {IntV(2), NullV()}}},
	}}
	star, err := evalScalar(CountSubq{Arg: "*", Sub: Scan{Table: "S"}}, nil, env)
	if err != nil {
		t.Fatal(err)
	}
	if star != IntV(3) {
		t.Fatalf("COUNT(*) should count all rows incl. NULLs, got %v", star)
	}
	col, err := evalScalar(CountSubq{Arg: "S.d", Sub: Scan{Table: "S"}}, nil, env)
	if err != nil {
		t.Fatal(err)
	}
	if col != IntV(1) {
		t.Fatalf("COUNT(d) should skip NULLs, got %v", col)
	}
}

func TestBagSemanticsPreserved(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}, Rows: []Row{{IntV(1)}, {IntV(1)}, {IntV(1)}}},
	}}
	_, rows, err := evalPlan(Filter{Input: Scan{Table: "R"}, Pred: Cmp{Op: "eq", L: Col{Name: "R.a"}, R: Lit{V: IntV(1)}}}, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("bag semantics: duplicates must be preserved, got %d rows", len(rows))
	}
}

func TestInnerJoinMultipliesDuplicates(t *testing.T) {
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}, Rows: []Row{{IntV(1)}, {IntV(1)}}},
		"S": {Cols: []string{"c"}, Rows: []Row{{IntV(1)}, {IntV(1)}, {IntV(1)}}},
	}}
	plan := Join{Kind: InnerJoin, Left: Scan{Table: "R"}, Right: Scan{Table: "S"},
		On: Cmp{Op: "eq", L: Col{Name: "R.a"}, R: Col{Name: "S.c"}}}
	_, rows, err := evalPlan(plan, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 {
		t.Fatalf("2 x 3 duplicate keys should produce 6 rows, got %d", len(rows))
	}
}

func bagOfStrings(rows []Row) []string {
	ks := make([]string, len(rows))
	for i, r := range rows {
		parts := make([]string, len(r))
		for j, v := range r {
			parts[j] = v.String()
		}
		ks[i] = strings.Join(parts, ",")
	}
	sort.Strings(ks)
	return ks
}

func assertBag(t *testing.T, rows []Row, want ...string) {
	t.Helper()
	got := bagOfStrings(rows)
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("bag mismatch:\n got: %v\nwant: %v", got, want)
	}
}
