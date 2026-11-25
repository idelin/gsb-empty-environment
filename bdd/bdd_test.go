package bdd

import (
	"testing"
)

func enumerate(t *testing.T, width int, fn func(values []bool)) {
	t.Helper()
	values := make([]bool, width)
	var walk func(level int)
	walk = func(level int) {
		if level == width {
			fn(append([]bool(nil), values...))
			return
		}
		values[level] = false
		walk(level + 1)
		values[level] = true
		walk(level + 1)
		values[level] = false
	}
	walk(0)
}

func TestBooleanOperationsMatchTruthTables(t *testing.T) {
	const width = 5
	manager, err := NewManager(width)
	if err != nil {
		t.Fatal(err)
	}
	vars := make([]BDD, width)
	for index := range vars {
		vars[index], _ = manager.Variable(index)
	}
	root := manager.Ite(
		manager.And(vars[0], manager.Or(vars[1], manager.Not(vars[2]))),
		manager.Xor(vars[3], vars[4]),
		manager.And(vars[2], vars[4]),
	)
	enumerate(t, width, func(values []bool) {
		got, err := manager.Eval(root, values)
		if err != nil {
			t.Fatal(err)
		}
		want := (values[0] && (values[1] || !values[2])) &&
			(values[3] != values[4]) ||
			(!(values[0] && (values[1] || !values[2])) && values[2] && values[4])
		if got != want {
			t.Fatalf("Eval(%v) = %v, want %v", values, got, want)
		}
	})
}

func TestFromFuncAndMinSolution(t *testing.T) {
	const width = 6
	manager, _ := NewManager(width)
	vars := make([]BDD, width)
	for index := range vars {
		vars[index], _ = manager.Variable(index)
	}
	root := manager.Or(
		manager.And(vars[0], vars[5]),
		manager.And(manager.Not(vars[0]), vars[1], vars[2]),
	)
	minimal := manager.MinSolution(root)
	want := []bool{false, true, true, false, false, false}
	for index := range want {
		if minimal[index] != want[index] {
			t.Fatalf("MinSolution = %v, want %v", minimal, want)
		}
	}

	table, err := manager.FromFunc(width, func(values []bool) bool {
		return (values[0] && values[5]) || (!values[0] && values[1] && values[2])
	})
	if err != nil {
		t.Fatal(err)
	}
	if !manager.Equivalent(table, root) {
		t.Fatal("FromFunc produced a different function")
	}
	if manager.MinSolution(Zero) != nil {
		t.Fatal("unsatisfiable BDD must return nil")
	}
}

func TestRestrictAndParse(t *testing.T) {
	const width = 4
	manager, _ := NewManager(width)
	variables := make([]BDD, width)
	for index := range variables {
		variables[index], _ = manager.Variable(index)
	}
	parsed, err := manager.Parse("a & (b | !N2)", map[string]int{"a": 0, "b": 1})
	if err != nil {
		t.Fatal(err)
	}
	manual := manager.And(variables[0], manager.Or(variables[1], manager.Not(variables[2])))
	if !manager.Equivalent(parsed, manual) {
		t.Fatal("parser result differs from manual construction")
	}
	restricted, err := manager.Restrict(parsed, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	expected := manager.Or(variables[1], manager.Not(variables[2]))
	if !manager.Equivalent(restricted, expected) {
		t.Fatal("restriction mismatch")
	}
	if tautology, _ := manager.Parse("true", nil); !manager.IsOne(tautology) {
		t.Fatal("empty/true parser support failed")
	}
}
