package main

import (
	"reflect"
	"testing"
)

func TestMutuallyExclusiveEdgesNoFakeCycle(t *testing.T) {
	// Union of edges contains A->B->A, but no single configuration does.
	g := Graph{
		Switches: []string{"x"},
		Edges: []Edge{
			{From: "A", To: "B", When: "x"},
			{From: "B", To: "A", When: "!x"},
		},
	}
	res, err := Check(g)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cyclic {
		t.Fatalf("expected acyclic, got cycle %v under %v", res.Cycle, res.BestConfig)
	}
	if res.TotalConfigs != 2 {
		t.Fatalf("expected 2 configurations, got %d", res.TotalConfigs)
	}
}

func TestMutuallyExclusiveThreeNodeChain(t *testing.T) {
	// Union has A->B->C->A, but per configuration the chain is broken.
	g := Graph{
		Switches: []string{"x"},
		Edges: []Edge{
			{From: "A", To: "B", When: "x"},
			{From: "B", To: "C", When: "!x"},
			{From: "C", To: "A", When: ""},
		},
	}
	res, err := Check(g)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cyclic {
		t.Fatalf("expected acyclic, got cycle %v under %v", res.Cycle, res.BestConfig)
	}
	if res.TotalConfigs != 2 {
		t.Fatalf("expected 2 configurations, got %d", res.TotalConfigs)
	}
}

func TestUnconditionalCycle(t *testing.T) {
	g := Graph{
		Switches: []string{"x", "y"},
		Edges: []Edge{
			{From: "A", To: "B"},
			{From: "B", To: "A"},
		},
	}
	res, err := Check(g)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cyclic {
		t.Fatal("expected cyclic")
	}
	if got := BitString(res.BestConfig); got != "00" {
		t.Fatalf("expected minimal config 00, got %s", got)
	}
	want := []string{"A", "B", "A"}
	if !reflect.DeepEqual(res.Cycle, want) {
		t.Fatalf("expected cycle %v, got %v", want, res.Cycle)
	}
}

func TestMinimalConfigSelection(t *testing.T) {
	// Cycle requires x=1; y is free. Minimal bit string is 10, not 11.
	g := Graph{
		Switches: []string{"x", "y"},
		Edges: []Edge{
			{From: "A", To: "B", When: "x"},
			{From: "B", To: "A", When: "x && (y || !y)"},
		},
	}
	res, err := Check(g)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cyclic {
		t.Fatal("expected cyclic")
	}
	if got := BitString(res.BestConfig); got != "10" {
		t.Fatalf("expected minimal config 10, got %s", got)
	}
}

func TestConditionalCycleNeedsAllConditions(t *testing.T) {
	// Cycle only when x && y; minimal config is 11.
	g := Graph{
		Switches: []string{"x", "y"},
		Edges: []Edge{
			{From: "A", To: "B", When: "x"},
			{From: "B", To: "C"},
			{From: "C", To: "A", When: "y"},
		},
	}
	res, err := Check(g)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cyclic {
		t.Fatal("expected cyclic")
	}
	if got := BitString(res.BestConfig); got != "11" {
		t.Fatalf("expected minimal config 11, got %s", got)
	}
	// Verify the reported cycle is real: consecutive edges must be active
	// under the reported configuration.
	cyc := res.Cycle
	if len(cyc) < 2 || cyc[0] != cyc[len(cyc)-1] {
		t.Fatalf("cycle not closed: %v", cyc)
	}
	env := map[string]bool{"x": true, "y": true}
	active := map[[2]string]bool{}
	for _, e := range g.Edges {
		ex, err := ParseExpr(e.When)
		if err != nil {
			t.Fatal(err)
		}
		if ex.Eval(env) {
			active[[2]string{e.From, e.To}] = true
		}
	}
	for i := 0; i+1 < len(cyc); i++ {
		if !active[[2]string{cyc[i], cyc[i+1]}] {
			t.Fatalf("edge %s -> %s of reported cycle is inactive under config", cyc[i], cyc[i+1])
		}
	}
}

func TestSelfLoop(t *testing.T) {
	g := Graph{
		Switches: []string{"x"},
		Edges:    []Edge{{From: "A", To: "A", When: "x"}},
	}
	res, err := Check(g)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cyclic {
		t.Fatal("expected cyclic")
	}
	if got := BitString(res.BestConfig); got != "1" {
		t.Fatalf("expected config 1, got %s", got)
	}
	if !reflect.DeepEqual(res.Cycle, []string{"A", "A"}) {
		t.Fatalf("unexpected cycle %v", res.Cycle)
	}
}

func TestFullEnumerationCount(t *testing.T) {
	g := Graph{
		Switches: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
		Edges: []Edge{
			{From: "A", To: "B", When: "a && b"},
			{From: "B", To: "C", When: "c"},
		},
	}
	res, err := Check(g)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cyclic {
		t.Fatal("expected acyclic")
	}
	if res.TotalConfigs != 1024 {
		t.Fatalf("expected 1024 configurations, got %d", res.TotalConfigs)
	}
}

func TestTooManySwitches(t *testing.T) {
	g := Graph{Switches: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}}
	if _, err := Check(g); err == nil {
		t.Fatal("expected error for 11 switches")
	}
}

func TestParseExpr(t *testing.T) {
	cases := []struct {
		expr string
		env  map[string]bool
		want bool
	}{
		{"", nil, true},
		{"true", nil, true},
		{"!false", nil, true},
		{"x", map[string]bool{"x": true}, true},
		{"!x", map[string]bool{"x": true}, false},
		{"x && y", map[string]bool{"x": true, "y": false}, false},
		{"x || y", map[string]bool{"x": true, "y": false}, true},
		{"(x || y) && !z", map[string]bool{"x": false, "y": true, "z": false}, true},
		{"x && y || z", map[string]bool{"x": false, "y": true, "z": true}, true},  // || lower precedence
		{"x || y && z", map[string]bool{"x": true, "y": false, "z": false}, true}, // && binds tighter
		{"!!x", map[string]bool{"x": true}, true},
	}
	for _, c := range cases {
		ex, err := ParseExpr(c.expr)
		if err != nil {
			t.Fatalf("ParseExpr(%q): %v", c.expr, err)
		}
		if got := ex.Eval(c.env); got != c.want {
			t.Fatalf("ParseExpr(%q).Eval(%v) = %v, want %v", c.expr, c.env, got, c.want)
		}
	}
}

func TestParseExprErrors(t *testing.T) {
	for _, s := range []string{"x &&", "&& x", "(x", "x)", "x y", "@"} {
		if _, err := ParseExpr(s); err == nil {
			t.Fatalf("expected error for %q", s)
		}
	}
}
