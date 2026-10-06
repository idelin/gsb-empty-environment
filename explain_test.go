package explain

import (
	"reflect"
	"testing"
)

func boolDom() []int64 { return []int64{0, 1} }

func mustExplain(t *testing.T, p Problem, cur Assignment) Explanation {
	t.Helper()
	expl, err := Explain(p, cur)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	suff, minimal, err := Verify(p, cur, expl)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !suff {
		t.Fatalf("explanation %v not sufficient", expl.Vars)
	}
	if !minimal {
		t.Fatalf("explanation %v not minimal", expl.Vars)
	}
	return expl
}

func TestAndFalseShortCircuit(t *testing.T) {
	p := Problem{
		Rule:    And{Cmp("a", Gt, 0), Cmp("b", Gt, 0)},
		Domains: map[string][]int64{"a": boolDom(), "b": boolDom()},
		Version: "v1",
	}
	expl := mustExplain(t, p, Assignment{"a": 0, "b": 1})
	if !reflect.DeepEqual(expl.Vars, []string{"a"}) {
		t.Fatalf("got %v, want [a]", expl.Vars)
	}
	if expl.Outcome.Value || expl.Outcome.Err != nil {
		t.Fatalf("unexpected outcome %+v", expl.Outcome)
	}
}

func TestTieBreakByName(t *testing.T) {
	// a=0 and b=0 each alone force false; the lexicographically smaller
	// variable name must win.
	p := Problem{
		Rule:    And{Cmp("b", Gt, 0), Cmp("a", Gt, 0)},
		Domains: map[string][]int64{"a": boolDom(), "b": boolDom()},
		Version: "v1",
	}
	expl := mustExplain(t, p, Assignment{"a": 0, "b": 0})
	if !reflect.DeepEqual(expl.Vars, []string{"a"}) {
		t.Fatalf("got %v, want [a]", expl.Vars)
	}
}

func TestXorNeedsBoth(t *testing.T) {
	rule := Or{
		And{Cmp("a", Gt, 0), Not{Cmp("b", Gt, 0)}},
		And{Not{Cmp("a", Gt, 0)}, Cmp("b", Gt, 0)},
	}
	p := Problem{
		Rule:    rule,
		Domains: map[string][]int64{"a": boolDom(), "b": boolDom()},
		Version: "v1",
	}
	expl := mustExplain(t, p, Assignment{"a": 1, "b": 0})
	if !reflect.DeepEqual(expl.Vars, []string{"a", "b"}) {
		t.Fatalf("got %v, want [a b]", expl.Vars)
	}
}

// TestSinglePathIsNotEnough: evaluating (a&&b)||(a&&!b) with a=1,b=1 only
// touches a and b on one path, and a naive path-based explanation would keep
// both; enumeration proves {a} alone guarantees the conclusion.
func TestSinglePathIsNotEnough(t *testing.T) {
	rule := Or{
		And{Cmp("a", Gt, 0), Cmp("b", Gt, 0)},
		And{Cmp("a", Gt, 0), Not{Cmp("b", Gt, 0)}},
	}
	p := Problem{
		Rule:    rule,
		Domains: map[string][]int64{"a": boolDom(), "b": boolDom()},
		Version: "v1",
	}
	expl := mustExplain(t, p, Assignment{"a": 1, "b": 1})
	if !reflect.DeepEqual(expl.Vars, []string{"a"}) {
		t.Fatalf("got %v, want [a]", expl.Vars)
	}
	if !expl.Outcome.Value {
		t.Fatal("expected true outcome")
	}
}

func TestErrorShortCircuitedAway(t *testing.T) {
	// a=1 short-circuits the division; fixing a alone guarantees no error.
	p := Problem{
		Rule: Or{Cmp("a", Gt, 0), DivGt(10, "x", 1)},
		Domains: map[string][]int64{
			"a": boolDom(),
			"x": {0, 1, 2},
		},
		Version: "v1",
	}
	expl := mustExplain(t, p, Assignment{"a": 1, "x": 0})
	if !reflect.DeepEqual(expl.Vars, []string{"a"}) {
		t.Fatalf("got %v, want [a]", expl.Vars)
	}
	if expl.Outcome.Err != nil || !expl.Outcome.Value {
		t.Fatalf("unexpected outcome %+v", expl.Outcome)
	}
}

func TestErrorBehaviorMustBePreserved(t *testing.T) {
	// Baseline errors (division by zero). Neither {a} nor {x} alone
	// preserves the error; both are needed.
	p := Problem{
		Rule: Or{Cmp("a", Gt, 0), DivGt(10, "x", 1)},
		Domains: map[string][]int64{
			"a": boolDom(),
			"x": {0, 1, 2},
		},
		Version: "v1",
	}
	expl := mustExplain(t, p, Assignment{"a": 0, "x": 0})
	if !reflect.DeepEqual(expl.Vars, []string{"a", "x"}) {
		t.Fatalf("got %v, want [a x]", expl.Vars)
	}
	if expl.Outcome.Err == nil {
		t.Fatal("expected error outcome")
	}
}

func TestErrorFreeBranchViaOtherVar(t *testing.T) {
	// Baseline true with no error via x=2; fixing x alone keeps the
	// conclusion true and error-free regardless of a.
	p := Problem{
		Rule: Or{Cmp("a", Gt, 0), DivGt(10, "x", 1)},
		Domains: map[string][]int64{
			"a": boolDom(),
			"x": {0, 1, 2},
		},
		Version: "v1",
	}
	expl := mustExplain(t, p, Assignment{"a": 0, "x": 2})
	if !reflect.DeepEqual(expl.Vars, []string{"x"}) {
		t.Fatalf("got %v, want [x]", expl.Vars)
	}
}

func TestUnknownInput(t *testing.T) {
	// The rule references b, which has no domain: an unknown input.
	p := Problem{
		Rule:    And{Cmp("a", Gt, 0), Cmp("b", Gt, 0)},
		Domains: map[string][]int64{"a": boolDom()},
		Version: "v1",
	}
	// a=1 reaches b and errors; fixing a preserves the error.
	expl := mustExplain(t, p, Assignment{"a": 1})
	if !reflect.DeepEqual(expl.Vars, []string{"a"}) {
		t.Fatalf("got %v, want [a]", expl.Vars)
	}
	if expl.Outcome.Err == nil {
		t.Fatal("expected unknown-input error")
	}
	// a=0 short-circuits; conclusion false, no error.
	expl = mustExplain(t, p, Assignment{"a": 0})
	if expl.Outcome.Err != nil || expl.Outcome.Value {
		t.Fatalf("unexpected outcome %+v", expl.Outcome)
	}
}

func TestConstRuleNeedsNothing(t *testing.T) {
	p := Problem{
		Rule:    Const{V: true},
		Domains: map[string][]int64{"a": boolDom(), "b": boolDom()},
		Version: "v1",
	}
	expl := mustExplain(t, p, Assignment{"a": 1, "b": 0})
	if len(expl.Vars) != 0 {
		t.Fatalf("got %v, want empty", expl.Vars)
	}
}

func TestCacheAndReshrinkAfterDomainContraction(t *testing.T) {
	rule := And{Cmp("a", Gt, 0), And{Cmp("b", Gt, 0), Cmp("c", Gt, 0)}}
	cur := Assignment{"a": 1, "b": 1, "c": 1}
	full := Problem{
		Rule:    rule,
		Domains: map[string][]int64{"a": boolDom(), "b": boolDom(), "c": boolDom()},
		Version: "v1",
	}
	e := NewExplainer()
	expl, err := e.Explain(full, cur)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expl.Vars, []string{"a", "b", "c"}) {
		t.Fatalf("got %v, want [a b c]", expl.Vars)
	}

	// c's domain contracts to {1}: the cached explanation is still
	// sufficient but no longer minimal; it must be re-shortened.
	shrunk := full
	shrunk.Domains = map[string][]int64{"a": boolDom(), "b": boolDom(), "c": {1}}
	expl, err = e.Explain(shrunk, cur)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expl.Vars, []string{"a", "b"}) {
		t.Fatalf("got %v, want [a b]", expl.Vars)
	}
	if _, minimal, err := Verify(shrunk, cur, expl); err != nil || !minimal {
		t.Fatalf("re-shrunk explanation not minimal: %v", err)
	}
}

func TestCacheBoundToVersion(t *testing.T) {
	cur := Assignment{"a": 1, "b": 1}
	domains := map[string][]int64{"a": boolDom(), "b": boolDom()}
	e := NewExplainer()

	v1 := Problem{Rule: And{Cmp("a", Gt, 0), Cmp("b", Gt, 0)}, Domains: domains, Version: "v1"}
	expl, err := e.Explain(v1, cur)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expl.Vars, []string{"a", "b"}) {
		t.Fatalf("got %v, want [a b]", expl.Vars)
	}

	// Same inputs, new rule version: the cached v1 explanation must not
	// leak into v2, whose rule ignores b entirely.
	v2 := Problem{Rule: Cmp("a", Gt, 0), Domains: domains, Version: "v2"}
	expl, err = e.Explain(v2, cur)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expl.Vars, []string{"a"}) {
		t.Fatalf("got %v, want [a]", expl.Vars)
	}
}

func TestVerifyRejectsBadExplanations(t *testing.T) {
	p := Problem{
		Rule:    And{Cmp("a", Gt, 0), Cmp("b", Gt, 0)},
		Domains: map[string][]int64{"a": boolDom(), "b": boolDom()},
		Version: "v1",
	}
	cur := Assignment{"a": 0, "b": 0}
	base := Eval(p.Rule, cur)

	// Not sufficient: fixing only b still lets a=1,b=1... no wait, b=0
	// fixed forces false; use a genuinely insufficient set: empty.
	suff, _, err := Verify(p, cur, Explanation{Version: "v1", Vars: nil, Outcome: base})
	if err != nil {
		t.Fatal(err)
	}
	if suff {
		t.Fatal("empty fix set must be insufficient here")
	}

	// Sufficient but not minimal.
	suff, minimal, err := Verify(p, cur, Explanation{
		Version: "v1",
		Vars:    []string{"a", "b"},
		Outcome: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !suff || minimal {
		t.Fatalf("want sufficient but not minimal, got suff=%v minimal=%v", suff, minimal)
	}
}

func TestTooManyVariables(t *testing.T) {
	domains := make(map[string][]int64)
	cur := make(Assignment)
	for i := 0; i < MaxVars+1; i++ {
		name := string(rune('a' + i))
		domains[name] = boolDom()
		cur[name] = 0
	}
	p := Problem{Rule: Const{V: true}, Domains: domains, Version: "v1"}
	if _, err := Explain(p, cur); err == nil {
		t.Fatal("expected error for more than 12 variables")
	}
}

func TestTwelveVariablesExhaustive(t *testing.T) {
	// 12 boolean variables: 4096 assignments fully enumerated. The rule
	// only depends on v00 and v11, so the explanation must drop the rest.
	domains := make(map[string][]int64)
	cur := make(Assignment)
	names := make([]string, 0, MaxVars)
	for i := 0; i < MaxVars; i++ {
		name := "v" + string(rune('0'+i/10)) + string(rune('0'+i%10))
		names = append(names, name)
		domains[name] = boolDom()
		cur[name] = 1
	}
	rule := And{Cmp("v00", Gt, 0), Cmp("v11", Gt, 0)}
	p := Problem{Rule: rule, Domains: domains, Version: "v1"}
	expl := mustExplain(t, p, cur)
	if !reflect.DeepEqual(expl.Vars, []string{"v00", "v11"}) {
		t.Fatalf("got %v, want [v00 v11]", expl.Vars)
	}
}

func TestMissingCurrentValue(t *testing.T) {
	p := Problem{
		Rule:    Cmp("a", Gt, 0),
		Domains: map[string][]int64{"a": boolDom()},
		Version: "v1",
	}
	if _, err := Explain(p, Assignment{}); err == nil {
		t.Fatal("expected error for missing current value")
	}
}
