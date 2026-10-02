package attr

import (
	"errors"
	"strings"
	"testing"
)

// TestTopologicalEvaluation builds a small attribute graph over fake nodes
// and checks that every rule sees fully evaluated dependencies.
func TestTopologicalEvaluation(t *testing.T) {
	ev := NewEvaluator()
	// Seed: inherited attribute of the root.
	ev.Set(Key{"root", "in"}, 10)

	mk := func(target Key, deps []Key, f func(Getter) Value) {
		if err := ev.AddRule(Rule{Target: target, Deps: deps, Eval: f}); err != nil {
			t.Fatal(err)
		}
	}
	// Declared in scrambled order on purpose: evaluation order must come
	// from the dependency graph, not registration order.
	mk(Key{"c", "sum"}, []Key{{"a", "v"}, {"b", "v"}}, func(g Getter) Value {
		return g(Key{"a", "v"}).(int) + g(Key{"b", "v"}).(int)
	})
	mk(Key{"a", "v"}, []Key{{"root", "in"}}, func(g Getter) Value {
		return g(Key{"root", "in"}).(int) + 1
	})
	mk(Key{"d", "v"}, []Key{{"c", "sum"}}, func(g Getter) Value {
		return g(Key{"c", "sum"}).(int) * 2
	})
	mk(Key{"b", "v"}, []Key{{"root", "in"}}, func(g Getter) Value {
		return g(Key{"root", "in"}).(int) * 3
	})

	if err := ev.Evaluate(); err != nil {
		t.Fatal(err)
	}
	want := map[Key]int{
		{"a", "v"}:   11,
		{"b", "v"}:   30,
		{"c", "sum"}: 41,
		{"d", "v"}:   82,
	}
	for k, w := range want {
		got, ok := ev.Get(k)
		if !ok {
			t.Fatalf("attribute %s not evaluated", k)
		}
		if got.(int) != w {
			t.Errorf("attribute %s = %v, want %d", k, got, w)
		}
	}
}

// TestInheritedAndSynthesizedChain threads an inherited attribute down a
// chain of nodes and a synthesized attribute back up.
func TestInheritedAndSynthesizedChain(t *testing.T) {
	ev := NewEvaluator()
	ev.Set(Key{"n0", "env"}, "top")
	names := []string{"n0", "n1", "n2", "n3"}
	for i := 1; i < len(names); i++ {
		parent := Key{names[i-1], "env"}
		self := Key{names[i], "env"}
		if err := ev.AddRule(Rule{Target: self, Deps: []Key{parent},
			Eval: func(g Getter) Value { return g(parent).(string) + ">" + names[i][1:] }}); err != nil {
			t.Fatal(err)
		}
	}
	// Synthesized: deepest node's env flows back up as "result".
	for i := len(names) - 1; i >= 0; i-- {
		dep := Key{names[i], "env"}
		if i < len(names)-1 {
			dep = Key{names[i+1], "result"}
		}
		target := Key{names[i], "result"}
		if err := ev.AddRule(Rule{Target: target, Deps: []Key{dep},
			Eval: func(g Getter) Value { return g(dep) }}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ev.Evaluate(); err != nil {
		t.Fatal(err)
	}
	got, _ := ev.Get(Key{"n0", "result"})
	if want := "top>1>2>3"; got != want {
		t.Errorf("synthesized result = %v, want %q", got, want)
	}
}

func TestCycleDetection(t *testing.T) {
	ev := NewEvaluator()
	// x.a depends on y.b, y.b depends on x.a: a classic
	// inherited/synthesized attribute cycle.
	add := func(target, dep Key) {
		if err := ev.AddRule(Rule{Target: target, Deps: []Key{dep},
			Eval: func(g Getter) Value { return g(dep) }}); err != nil {
			t.Fatal(err)
		}
	}
	add(Key{"x", "a"}, Key{"y", "b"})
	add(Key{"y", "b"}, Key{"x", "a"})

	err := ev.Evaluate()
	var cyc *CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("expected *CycleError, got %v", err)
	}
	if len(cyc.Keys) != 2 {
		t.Fatalf("cycle should contain 2 attributes, got %v", cyc.Keys)
	}
	msg := err.Error()
	if !strings.Contains(msg, "x.a") || !strings.Contains(msg, "y.b") {
		t.Errorf("cycle message should name the attributes, got: %s", msg)
	}
}

func TestSelfCycle(t *testing.T) {
	ev := NewEvaluator()
	k := Key{"n", "a"}
	if err := ev.AddRule(Rule{Target: k, Deps: []Key{k},
		Eval: func(g Getter) Value { return g(k) }}); err != nil {
		t.Fatal(err)
	}
	var cyc *CycleError
	if err := ev.Evaluate(); !errors.As(err, &cyc) {
		t.Fatalf("expected *CycleError, got %v", err)
	}
}

func TestUndefinedDependency(t *testing.T) {
	ev := NewEvaluator()
	err := ev.AddRule(Rule{Target: Key{"n", "a"}, Deps: []Key{{"ghost", "b"}},
		Eval: func(g Getter) Value { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := ev.Evaluate(); err == nil || !strings.Contains(err.Error(), "undefined attribute") {
		t.Fatalf("expected undefined-dependency error, got %v", err)
	}
}

func TestDuplicateRule(t *testing.T) {
	ev := NewEvaluator()
	r := Rule{Target: Key{"n", "a"}, Eval: func(Getter) Value { return 1 }}
	if err := ev.AddRule(r); err != nil {
		t.Fatal(err)
	}
	if err := ev.AddRule(r); err == nil {
		t.Fatal("expected duplicate-rule error")
	}
}
