package fsm

import (
	"errors"
	"testing"
)

func TestCompileValid(t *testing.T) {
	m, err := Compile("A",
		[]State{"A", "B"},
		[]Row{{"A", "go", "B", nil}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if m.Initial() != "A" {
		t.Fatalf("Initial = %q", m.Initial())
	}
}

func TestCompileRejectsBadTables(t *testing.T) {
	cases := []struct {
		name    string
		initial State
		states  []State
		rows    []Row
	}{
		{"empty initial", "", []State{"A"}, nil},
		{"undeclared initial", "X", []State{"A"}, nil},
		{"duplicate state", "A", []State{"A", "A"}, nil},
		{"empty state", "A", []State{"A", ""}, nil},
		{"unknown from", "A", []State{"A"}, []Row{{"B", "e", "A", nil}}},
		{"unknown to", "A", []State{"A"}, []Row{{"A", "e", "B", nil}}},
		{"duplicate row", "A", []State{"A"}, []Row{
			{"A", "e", "A", nil},
			{"A", "e", "A", nil},
		}},
	}
	for _, c := range cases {
		if _, err := Compile(c.initial, c.states, c.rows); err == nil {
			t.Errorf("%s: expected compile error, got nil", c.name)
		}
	}
}

func TestStepCommitsOutputAndStateAtomically(t *testing.T) {
	m, err := Compile("A", []State{"A", "B"}, []Row{
		{"A", "go", "B", func(c *Context) (interface{}, error) { return "ok", nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	next, res := m.Step("A", "go", nil)
	if res.Err != nil || !res.Applied || res.Output != "ok" || next != "B" {
		t.Fatalf("got next=%q res=%+v", next, res)
	}
}

func TestStepFailureKeepsOldState(t *testing.T) {
	boom := errors.New("boom")
	m, err := Compile("A", []State{"A", "B"}, []Row{
		{"A", "go", "B", func(c *Context) (interface{}, error) { return nil, boom }},
	})
	if err != nil {
		t.Fatal(err)
	}
	next, res := m.Step("A", "go", nil)
	if res.Err != boom {
		t.Fatalf("Err = %v, want boom", res.Err)
	}
	if res.Applied || next != "A" {
		t.Fatalf("state changed on failure: next=%q applied=%v", next, res.Applied)
	}
}

func TestStepNoTransition(t *testing.T) {
	m, err := Compile("A", []State{"A"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	next, res := m.Step("A", "nope", nil)
	if !IsNoTransition(res.Err) {
		t.Fatalf("Err = %v, want NoTransitionError", res.Err)
	}
	if next != "A" {
		t.Fatalf("state changed: %q", next)
	}
}
