package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestJSONRoundTrip(t *testing.T) {
	plan := corrCount("S.d", eq(col("S.d"), lit(1)))
	b, err := json.Marshal(planJSON(plan))
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParsePlan(b)
	if err != nil {
		t.Fatal(err)
	}
	env := &Env{Tables: map[string]Table{
		"R": {Cols: []string{"a"}, Rows: []Row{{IntV(1)}, {NullV()}}},
		"S": {Cols: []string{"c", "d"}, Rows: []Row{{IntV(1), IntV(1)}, {IntV(1), NullV()}}},
	}}
	_, r1, err := evalPlan(plan, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, r2, err := evalPlan(back, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bagOf(r1) != bagOf(r2) {
		t.Fatalf("JSON round trip changed plan semantics: %v vs %v", r1, r2)
	}
}

func TestExampleFile(t *testing.T) {
	data, err := os.ReadFile("testdata/example.json")
	if err != nil {
		t.Fatal(err)
	}
	env, plan, err := ParseInput(data)
	if err != nil {
		t.Fatal(err)
	}
	rows := checkEquivalent(t, env, plan)
	assertBag(t, rows, `1,"x",1`, `1,"y",1`, `NULL,"z",0`, `2,"w",0`)
}

func TestRejectInvalidJSON(t *testing.T) {
	if _, _, err := ParseInput([]byte(`{"tables":{}}`)); err == nil {
		t.Fatal("missing plan should be rejected")
	}
	if _, _, err := ParseInput([]byte(`{"tables":{"R":{"cols":["a"],"rows":[[1,2]]}},"plan":{"op":"scan","table":"R"}}`)); err == nil {
		t.Fatal("row width mismatch should be rejected")
	}
}
