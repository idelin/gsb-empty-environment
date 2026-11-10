package main

import (
	"testing"
)

func mustModel(t *testing.T, spec *Spec) *model {
	t.Helper()
	m, err := buildModel(spec)
	if err != nil {
		t.Fatalf("buildModel: %v", err)
	}
	return m
}

func mustSolve(t *testing.T, spec *Spec) *solution {
	t.Helper()
	sol, err := solve(mustModel(t, spec))
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if sol == nil {
		t.Fatalf("solve: expected a dangerous graph, got none")
	}
	return sol
}

// diamondSpec: both root fields have the same static type; a trigger-bearing
// subtype exists. Minimal dangerous graph: root + one shared object.
func diamondSpec() *Spec {
	return &Spec{
		RootTypes:     []string{"Session"},
		DangerFields:  []string{"primary", "backup"},
		MaxObjects:    8,
		TriggeredOnly: true,
		Types: []TypeSpec{
			{Name: "Session", Fields: []FieldSpec{
				{Name: "primary", Type: "Handler"},
				{Name: "backup", Type: "Handler"},
			}},
			{Name: "Handler", Fields: []FieldSpec{{Name: "next", Type: "Handler"}}},
			{Name: "EvilHandler", Triggers: []string{"readObject"},
				Fields: []FieldSpec{{Name: "next", Type: "Handler"}}},
		},
		Subtypes: map[string][]string{"Handler": {"Handler", "EvilHandler"}},
	}
}

func TestSolveDiamond(t *testing.T) {
	sol := mustSolve(t, diamondSpec())
	if len(sol.nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(sol.nodes))
	}
	if len(sol.shared) != 1 || sol.shared[0] != 1 {
		t.Fatalf("want shared=[1], got %v", sol.shared)
	}
	// Both danger fields must reference the shared object.
	if sol.nodes[0].refs[0] != 1 || sol.nodes[0].refs[1] != 1 {
		t.Fatalf("root refs = %v, want [1 1]", sol.nodes[0].refs)
	}
}

// chainSpec: the two danger fields have disjoint static types, so sharing is
// only possible one level down. Minimal: Root, IA, IB, shared Leaf = 4 nodes.
func chainSpec() *Spec {
	return &Spec{
		RootTypes:     []string{"Root"},
		DangerFields:  []string{"a", "b"},
		MaxObjects:    8,
		TriggeredOnly: true,
		Types: []TypeSpec{
			{Name: "Root", Fields: []FieldSpec{{Name: "a", Type: "IA"}, {Name: "b", Type: "IB"}}},
			{Name: "IA", Fields: []FieldSpec{{Name: "x", Type: "Leaf"}}},
			{Name: "IB", Fields: []FieldSpec{{Name: "y", Type: "Leaf"}}},
			{Name: "Leaf", Triggers: []string{"finalize"}},
		},
	}
}

func TestSolveChain(t *testing.T) {
	sol := mustSolve(t, chainSpec())
	if len(sol.nodes) != 4 {
		t.Fatalf("want 4 nodes, got %d", len(sol.nodes))
	}
	if len(sol.shared) != 1 {
		t.Fatalf("want exactly one shared object, got %v", sol.shared)
	}
}

func TestSolveChainRespectsBound(t *testing.T) {
	spec := chainSpec()
	spec.MaxObjects = 3
	sol, err := solve(mustModel(t, spec))
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if sol != nil {
		t.Fatalf("want no solution with maxObjects=3, got %d nodes", len(sol.nodes))
	}
}

// cycleSpec: the root type has a trigger and its fields may point back to it.
// Minimal dangerous graph: a single object referencing itself twice.
func cycleSpec() *Spec {
	return &Spec{
		RootTypes:     []string{"Node"},
		DangerFields:  []string{"p", "q"},
		MaxObjects:    8,
		TriggeredOnly: true,
		Types: []TypeSpec{
			{Name: "Node", Triggers: []string{"readObject"}, Fields: []FieldSpec{
				{Name: "p", Type: "Node"},
				{Name: "q", Type: "Node"},
			}},
		},
	}
}

func TestSolveCycle(t *testing.T) {
	sol := mustSolve(t, cycleSpec())
	if len(sol.nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(sol.nodes))
	}
	if sol.nodes[0].refs[0] != 0 || sol.nodes[0].refs[1] != 0 {
		t.Fatalf("root refs = %v, want [0 0] (self-cycle)", sol.nodes[0].refs)
	}
}

// safeSpec: the two subtrees have disjoint type sets, so no object can be
// reachable from both danger fields; the whitelist holds.
func safeSpec() *Spec {
	return &Spec{
		RootTypes:    []string{"Root"},
		DangerFields: []string{"a", "b"},
		MaxObjects:   8,
		Types: []TypeSpec{
			{Name: "Root", Fields: []FieldSpec{{Name: "a", Type: "IA"}, {Name: "b", Type: "IB"}}},
			{Name: "IA", Fields: []FieldSpec{{Name: "x", Type: "LeafA"}}},
			{Name: "IB", Fields: []FieldSpec{{Name: "y", Type: "LeafB"}}},
			{Name: "LeafA", Triggers: []string{"readObject"}},
			{Name: "LeafB"},
		},
	}
}

func TestSolveSafe(t *testing.T) {
	sol, err := solve(mustModel(t, safeSpec()))
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if sol != nil {
		t.Fatalf("want no dangerous graph, got %d nodes", len(sol.nodes))
	}
}

func failures(checks []check) []string {
	var out []string
	for _, c := range checks {
		if c.err != nil {
			out = append(out, c.name+": "+c.err.Error())
		}
	}
	return out
}

func TestVerifyRoundTrip(t *testing.T) {
	for name, spec := range map[string]*Spec{
		"diamond": diamondSpec(),
		"chain":   chainSpec(),
		"cycle":   cycleSpec(),
	} {
		t.Run(name, func(t *testing.T) {
			m := mustModel(t, spec)
			sol, err := solve(m)
			if err != nil || sol == nil {
				t.Fatalf("solve: sol=%v err=%v", sol, err)
			}
			res := buildResult(m, sol)
			if !res.Dangerous {
				t.Fatalf("result not marked dangerous")
			}
			if bad := failures(verifyGraph(m, res)); len(bad) > 0 {
				t.Fatalf("verify failed: %v", bad)
			}
		})
	}
}

func TestVerifyRejectsTampered(t *testing.T) {
	spec := diamondSpec()
	m := mustModel(t, spec)
	sol, err := solve(m)
	if err != nil || sol == nil {
		t.Fatalf("solve: sol=%v err=%v", sol, err)
	}
	res := buildResult(m, sol)
	// Break the aliasing: point backup at null.
	res.Objects[0].Fields["backup"] = nil
	if bad := failures(verifyGraph(m, res)); len(bad) == 0 {
		t.Fatalf("expected verification to fail for tampered graph")
	}
}

func TestVerifyRejectsNonMinimal(t *testing.T) {
	spec := diamondSpec()
	m := mustModel(t, spec)
	// Hand-build a 3-node dangerous graph (2 is minimal).
	one, two := 1, 2
	res := &Result{Objects: []ObjJSON{
		{ID: 0, Type: "Session", Fields: map[string]*int{"primary": &one, "backup": &one}},
		{ID: 1, Type: "EvilHandler", Fields: map[string]*int{"next": &two}},
		{ID: 2, Type: "Handler", Fields: map[string]*int{"next": nil}},
	}}
	bad := failures(verifyGraph(m, res))
	found := false
	for _, b := range bad {
		if len(b) >= 10 && b[:10] == "minimality" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a minimality failure, got %v", bad)
	}
}

func TestBuildModelValidation(t *testing.T) {
	spec := diamondSpec()
	spec.DangerFields = []string{"primary"}
	if _, err := buildModel(spec); err == nil {
		t.Fatalf("expected error for a single danger field")
	}
	spec = diamondSpec()
	spec.MaxObjects = 9
	if _, err := buildModel(spec); err == nil {
		t.Fatalf("expected error for maxObjects > 8")
	}
}
