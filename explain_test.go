package rulexplain

import (
	"errors"
	"reflect"
	"testing"
)

func boolDomains(names ...string) map[string][]int {
	d := make(map[string][]int, len(names))
	for _, n := range names {
		d[n] = []int{0, 1}
	}
	return d
}

func mustExplainer(t *testing.T, r *Rule, d map[string][]int) *Explainer {
	t.Helper()
	e, err := NewExplainer(r, d)
	if err != nil {
		t.Fatalf("NewExplainer: %v", err)
	}
	return e
}

func mustExplain(t *testing.T, e *Explainer, env Env) Explanation {
	t.Helper()
	ex, err := e.Explain(env)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	return ex
}

func TestShortCircuitMasksUnknownInput(t *testing.T) {
	// x==1 || ghost==1: with x=1 the unknown input is never evaluated.
	rule := NewRule(Or{Condition{Var: "x", Op: Eq, Const: 1}, Condition{Var: "ghost", Op: Eq, Const: 1}})
	e := mustExplainer(t, rule, boolDomains("x"))

	ex := mustExplain(t, e, Env{"x": 1})
	if !reflect.DeepEqual(ex.Vars, []string{"x"}) {
		t.Fatalf("vars = %v, want [x]", ex.Vars)
	}
	if ex.Outcome != (Outcome{Value: true, Errored: false}) {
		t.Fatalf("outcome = %+v", ex.Outcome)
	}

	// With x=0 the unknown input surfaces as an error; the explanation
	// must pin x so the error (not just the value) is guaranteed.
	ex = mustExplain(t, e, Env{"x": 0})
	if !reflect.DeepEqual(ex.Vars, []string{"x"}) || !ex.Outcome.Errored {
		t.Fatalf("vars = %v outcome = %+v, want [x] errored", ex.Vars, ex.Outcome)
	}
	ok, err := e.Verify(Env{"x": 0}, nil)
	if err != nil || ok {
		t.Fatalf("Verify(empty) = %v, %v; want false, nil", ok, err)
	}
}

func TestShortCircuitAndMasksError(t *testing.T) {
	// x==0 && ghost==1: with x=1, And stops before the unknown input.
	rule := NewRule(And{Condition{Var: "x", Op: Eq, Const: 0}, Condition{Var: "ghost", Op: Eq, Const: 1}})
	e := mustExplainer(t, rule, boolDomains("x"))

	ex := mustExplain(t, e, Env{"x": 1})
	if !reflect.DeepEqual(ex.Vars, []string{"x"}) {
		t.Fatalf("vars = %v, want [x]", ex.Vars)
	}
	if ex.Outcome != (Outcome{Value: false, Errored: false}) {
		t.Fatalf("outcome = %+v, want {false false}", ex.Outcome)
	}
}

func TestSinglePathIsNotMinimal(t *testing.T) {
	// (a && b) || (a && !b): evaluating with a=1,b=1 reads both a and b,
	// so a path-trace "explanation" would say {a,b}; enumeration proves
	// {a} alone already pins the outcome.
	rule := NewRule(Or{
		And{Condition{Var: "a", Op: Eq, Const: 1}, Condition{Var: "b", Op: Eq, Const: 1}},
		And{Condition{Var: "a", Op: Eq, Const: 1}, Not{Condition{Var: "b", Op: Eq, Const: 1}}},
	})
	e := mustExplainer(t, rule, boolDomains("a", "b"))

	ex := mustExplain(t, e, Env{"a": 1, "b": 1})
	if !reflect.DeepEqual(ex.Vars, []string{"a"}) {
		t.Fatalf("vars = %v, want [a]", ex.Vars)
	}
}

func TestTieBreakByVariableName(t *testing.T) {
	// x||y with both true: {x} and {y} are both minimal; the
	// lexicographically smaller variable name wins.
	rule := NewRule(Or{Condition{Var: "x", Op: Eq, Const: 1}, Condition{Var: "y", Op: Eq, Const: 1}})
	e := mustExplainer(t, rule, boolDomains("x", "y"))

	ex := mustExplain(t, e, Env{"x": 1, "y": 1})
	if !reflect.DeepEqual(ex.Vars, []string{"x"}) {
		t.Fatalf("vars = %v, want [x]", ex.Vars)
	}
	min, err := e.IsMinimal(Env{"x": 1, "y": 1}, []string{"x"})
	if err != nil || !min {
		t.Fatalf("IsMinimal([x]) = %v, %v; want true, nil", min, err)
	}
	min, err = e.IsMinimal(Env{"x": 1, "y": 1}, []string{"x", "y"})
	if err != nil || min {
		t.Fatalf("IsMinimal([x y]) = %v, %v; want false, nil", min, err)
	}
}

func TestRuntimeErrorSemanticsPreserved(t *testing.T) {
	// x==1 || (10 % y == 0): the modulo errors when y == 0.
	mod := Pred{Fn: func(env Env) (bool, error) {
		y := env["y"]
		if y == 0 {
			return false, errors.New("division by zero")
		}
		return 10%y == 0, nil
	}}
	rule := NewRule(Or{Condition{Var: "x", Op: Eq, Const: 1}, mod})
	e := mustExplainer(t, rule, map[string][]int{"x": {0, 1}, "y": {0, 1, 2}})

	// y=2: whether x short-circuits or the modulo runs, the outcome is
	// true without error, so {y} alone is a sufficient explanation.
	ex := mustExplain(t, e, Env{"x": 0, "y": 2})
	if !reflect.DeepEqual(ex.Vars, []string{"y"}) {
		t.Fatalf("vars = %v, want [y]", ex.Vars)
	}

	// y=0: the error only surfaces when x=0, so both variables are
	// needed to pin the errored outcome.
	env := Env{"x": 0, "y": 0}
	ex = mustExplain(t, e, env)
	if !reflect.DeepEqual(ex.Vars, []string{"x", "y"}) || !ex.Outcome.Errored {
		t.Fatalf("vars = %v outcome = %+v, want [x y] errored", ex.Vars, ex.Outcome)
	}
	for _, sub := range [][]string{{"x"}, {"y"}, {}} {
		ok, err := e.Verify(env, sub)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatalf("Verify(%v) = true, want false", sub)
		}
	}
}

func TestCacheBoundToRuleVersion(t *testing.T) {
	rule := NewRule(Or{Condition{Var: "x", Op: Eq, Const: 1}, Condition{Var: "y", Op: Eq, Const: 1}})
	e := mustExplainer(t, rule, boolDomains("x", "y"))
	env := Env{"x": 1, "y": 0}

	ex := mustExplain(t, e, env)
	if !reflect.DeepEqual(ex.Vars, []string{"x"}) {
		t.Fatalf("vars = %v, want [x]", ex.Vars)
	}
	// Cached result for the identical query.
	ex2 := mustExplain(t, e, env)
	if !reflect.DeepEqual(ex, ex2) {
		t.Fatalf("cached = %+v, want %+v", ex2, ex)
	}

	// Mutating the rule bumps the version and must invalidate the cache.
	rule.SetRoot(And{Condition{Var: "x", Op: Eq, Const: 1}, Condition{Var: "y", Op: Eq, Const: 1}})
	ex = mustExplain(t, e, env)
	if !reflect.DeepEqual(ex.Vars, []string{"y"}) || ex.Outcome.Value {
		t.Fatalf("vars = %v outcome = %+v, want [y] false", ex.Vars, ex.Outcome)
	}
}

func TestDomainShrinkReshortensExplanation(t *testing.T) {
	// x==1 && y!=2: with y ranging over {0,1,2} both variables are
	// needed; once y's domain shrinks to {0,1}, {x} suffices.
	rule := NewRule(And{Condition{Var: "x", Op: Eq, Const: 1}, Condition{Var: "y", Op: Ne, Const: 2}})
	e := mustExplainer(t, rule, map[string][]int{"x": {0, 1}, "y": {0, 1, 2}})
	env := Env{"x": 1, "y": 1}

	ex := mustExplain(t, e, env)
	if !reflect.DeepEqual(ex.Vars, []string{"x", "y"}) {
		t.Fatalf("vars = %v, want [x y]", ex.Vars)
	}

	if err := e.SetDomains(map[string][]int{"x": {0, 1}, "y": {0, 1}}); err != nil {
		t.Fatal(err)
	}
	ex = mustExplain(t, e, env)
	if !reflect.DeepEqual(ex.Vars, []string{"x"}) {
		t.Fatalf("vars = %v after shrink, want [x]", ex.Vars)
	}
}

func TestTooManyVars(t *testing.T) {
	d := make(map[string][]int, 13)
	for i := 0; i < 13; i++ {
		d[string(rune('a'+i))] = []int{0, 1}
	}
	if _, err := NewExplainer(NewRule(And{}), d); err != ErrTooManyVars {
		t.Fatalf("err = %v, want ErrTooManyVars", err)
	}
}

func TestEnvValidation(t *testing.T) {
	rule := NewRule(Condition{Var: "x", Op: Eq, Const: 1})
	e := mustExplainer(t, rule, map[string][]int{"x": {0, 1}})
	if _, err := e.Explain(Env{}); err == nil {
		t.Fatal("Explain with missing variable: want error")
	}
	if _, err := e.Explain(Env{"x": 5}); err == nil {
		t.Fatal("Explain with out-of-domain value: want error")
	}
}

// bruteForce independently recomputes the minimal explanation by
// enumerating every subset (small to large, lexicographic) and every
// assignment, evaluating the rule from scratch each time.
func bruteForce(t *testing.T, rule *Rule, domains map[string][]int, env Env) []string {
	t.Helper()
	names := make([]string, 0, len(domains))
	for n := range domains {
		names = append(names, n)
	}
	sortStrings(names)
	base := Evaluate(rule.Root(), env)

	sufficient := func(sub []string) bool {
		pinned := make(map[string]int, len(sub))
		for _, n := range sub {
			pinned[n] = env[n]
		}
		free := make([]string, 0, len(names)-len(sub))
		for _, n := range names {
			if _, ok := pinned[n]; !ok {
				free = append(free, n)
			}
		}
		cur := make(Env, len(names))
		for n, v := range pinned {
			cur[n] = v
		}
		var walk func(i int) bool
		walk = func(i int) bool {
			if i == len(free) {
				return Evaluate(rule.Root(), cur) == base
			}
			for _, v := range domains[free[i]] {
				cur[free[i]] = v
				if !walk(i + 1) {
					return false
				}
			}
			return true
		}
		return walk(0)
	}

	for size := 0; size <= len(names); size++ {
		var found []string
		firstCombination(len(names), size, func(comb []int) bool {
			sub := make([]string, len(comb))
			for i, v := range comb {
				sub[i] = names[v]
			}
			if sufficient(sub) {
				found = sub
				return true
			}
			return false
		})
		if found != nil {
			return found
		}
	}
	t.Fatal("no sufficient subset found")
	return nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestExplainMatchesBruteForce(t *testing.T) {
	cond := func(v string, c int) Condition { return Condition{Var: v, Op: Eq, Const: c} }
	rules := []*Rule{
		NewRule(Or{And{cond("a", 1), cond("b", 1)}, And{cond("a", 1), Not{cond("b", 1)}}}),
		NewRule(And{Or{cond("a", 1), cond("b", 1)}, Or{cond("c", 1), Not{cond("a", 1)}}}),
		NewRule(Or{And{cond("a", 0), Or{cond("b", 1), cond("c", 0)}}, And{Not{cond("a", 0)}, cond("c", 1)}}),
		NewRule(Not{Or{cond("a", 1), And{cond("b", 0), cond("c", 1)}}}),
	}
	domains := boolDomains("a", "b", "c")
	envs := []Env{
		{"a": 0, "b": 0, "c": 0},
		{"a": 1, "b": 0, "c": 1},
		{"a": 1, "b": 1, "c": 0},
		{"a": 0, "b": 1, "c": 1},
	}
	for ri, rule := range rules {
		e := mustExplainer(t, rule, domains)
		for ei, env := range envs {
			got := mustExplain(t, e, env)
			want := bruteForce(t, rule, domains, env)
			if !reflect.DeepEqual(got.Vars, want) {
				t.Fatalf("rule %d env %d: vars = %v, brute force = %v", ri, ei, got.Vars, want)
			}
			ok, err := e.Verify(env, got.Vars)
			if err != nil || !ok {
				t.Fatalf("rule %d env %d: Verify(%v) = %v, %v", ri, ei, got.Vars, ok, err)
			}
			min, err := e.IsMinimal(env, got.Vars)
			if err != nil || !min {
				t.Fatalf("rule %d env %d: IsMinimal(%v) = %v, %v", ri, ei, got.Vars, min, err)
			}
		}
	}
}

func TestTwelveVarsBoundary(t *testing.T) {
	names := make([]string, 12)
	for i := 0; i < 12; i++ {
		names[i] = string(rune('a' + i))
	}
	// Outcome depends only on "k" and "l"; the other ten variables are
	// noise that full enumeration must prove irrelevant.
	rule := NewRule(And{
		Or{Condition{Var: "k", Op: Eq, Const: 1}, Condition{Var: "l", Op: Eq, Const: 1}},
		Condition{Var: "k", Op: Ge, Const: 0},
	})
	e := mustExplainer(t, rule, boolDomains(names...))
	env := Env{}
	for _, n := range names {
		env[n] = 0
	}
	env["k"] = 1
	ex := mustExplain(t, e, env)
	if !reflect.DeepEqual(ex.Vars, []string{"k"}) {
		t.Fatalf("vars = %v, want [k]", ex.Vars)
	}
}
