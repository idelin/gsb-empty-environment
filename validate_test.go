package validag

import (
	"strings"
	"testing"
)

func traceFor(t *testing.T, r *Result, ruleID string) Trace {
	t.Helper()
	for _, tr := range r.Traces {
		if tr.RuleID == ruleID {
			return tr
		}
	}
	t.Fatalf("no trace for rule %q", ruleID)
	return Trace{}
}

func TestBaseTypeFailureSkipsDependents(t *testing.T) {
	rs, err := NewRuleSet(
		IsInt("age", "age.type"),
		IntRange("age", "age.range", 0, 150, "age.type"),
	)
	if err != nil {
		t.Fatal(err)
	}
	res := rs.Validate(map[string]interface{}{"age": "not-a-number"})
	if len(res.Errors) != 1 {
		t.Fatalf("want exactly 1 error (base type), got %d: %v", len(res.Errors), res.Errors)
	}
	if res.Errors[0].RuleID != "age.type" {
		t.Fatalf("want error from age.type, got %q", res.Errors[0].RuleID)
	}
	tr := traceFor(t, res, "age.range")
	if tr.Status != Skipped {
		t.Fatalf("want age.range skipped, got %s", tr.Status)
	}
	if !strings.Contains(tr.Detail, `"age.type"`) {
		t.Fatalf("skip reason should name the failed dependency, got %q", tr.Detail)
	}
}

func TestIndependentErrorsStillCollected(t *testing.T) {
	rs, err := NewRuleSet(
		IsInt("age", "age.type"),
		IntRange("age", "age.range", 0, 150, "age.type"),
		IsString("name", "name.type"),
		MinLen("name", "name.min", 2, "name.type"),
	)
	if err != nil {
		t.Fatal(err)
	}
	res := rs.Validate(map[string]interface{}{"age": "x", "name": 42})
	if len(res.Errors) != 2 {
		t.Fatalf("want 2 independent errors, got %v", res.Errors)
	}
	if traceFor(t, res, "age.range").Status != Skipped {
		t.Fatal("age.range should be skipped")
	}
	if traceFor(t, res, "name.min").Status != Skipped {
		t.Fatal("name.min should be skipped")
	}
}

func TestCycleRejected(t *testing.T) {
	mk := func(id string, deps ...string) Rule {
		return Rule{ID: id, Field: "f", Check: func(Value) error { return nil }, Depends: deps}
	}
	if _, err := NewRuleSet(mk("a", "b"), mk("b", "a")); err == nil ||
		!strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
	if _, err := NewRuleSet(mk("a", "a")); err == nil {
		t.Fatal("want self-dependency rejected")
	}
	if _, err := NewRuleSet(mk("a", "ghost")); err == nil ||
		!strings.Contains(err.Error(), "unknown") {
		t.Fatalf("want unknown-dependency error, got %v", err)
	}
	if _, err := NewRuleSet(mk("a"), mk("a")); err == nil {
		t.Fatal("want duplicate ID rejected")
	}
}

func TestErrorsSortedByFieldThenRuleID(t *testing.T) {
	// Registration order is intentionally scrambled.
	rs, err := NewRuleSet(
		IsInt("b.count", "b.count.type"),
		Required("a.name", "a.name.zz-required"),
		Required("a.name", "a.name.aa-required"),
		IsString("b.count", "b.count.alt"),
	)
	if err != nil {
		t.Fatal(err)
	}
	res := rs.Validate(map[string]interface{}{"b": map[string]interface{}{"count": true}})
	want := []struct{ field, id string }{
		{"a.name", "a.name.aa-required"},
		{"a.name", "a.name.zz-required"},
		{"b.count", "b.count.alt"},
		{"b.count", "b.count.type"},
	}
	if len(res.Errors) != len(want) {
		t.Fatalf("want %d errors, got %v", len(want), res.Errors)
	}
	for i, w := range want {
		if res.Errors[i].Field != w.field || res.Errors[i].RuleID != w.id {
			t.Fatalf("error %d: want %s/%s, got %s/%s",
				i, w.field, w.id, res.Errors[i].Field, res.Errors[i].RuleID)
		}
	}
}

func TestMissingFieldSemantics(t *testing.T) {
	rs, err := NewRuleSet(
		Required("user.email", "email.required"),
		IsString("user.email", "email.type", "email.required"),
		MinLen("user.email", "email.min", 3, "email.type"),
		IsString("nickname", "nick.type"), // optional field
	)
	if err != nil {
		t.Fatal(err)
	}
	res := rs.Validate(map[string]interface{}{})
	if len(res.Errors) != 1 || res.Errors[0].RuleID != "email.required" {
		t.Fatalf("want only email.required error, got %v", res.Errors)
	}
	if tr := traceFor(t, res, "email.type"); tr.Status != Skipped ||
		!strings.Contains(tr.Detail, "did not pass") {
		t.Fatalf("email.type should be skipped via failed dependency, got %+v", tr)
	}
	if tr := traceFor(t, res, "nick.type"); tr.Status != Skipped ||
		!strings.Contains(tr.Detail, "missing") {
		t.Fatalf("optional missing field should skip, got %+v", tr)
	}
}

func TestConditionalValidation(t *testing.T) {
	isBusiness := func(in map[string]interface{}) bool {
		v, _ := lookup(in, "kind")
		return v == "business"
	}
	vatRule := IsString("vat", "vat.type")
	vatRule.When = isBusiness
	rs, err := NewRuleSet(vatRule)
	if err != nil {
		t.Fatal(err)
	}

	personal := rs.Validate(map[string]interface{}{"kind": "personal", "vat": 123})
	if !personal.OK() {
		t.Fatalf("condition unmet: want no errors, got %v", personal.Errors)
	}
	if tr := traceFor(t, personal, "vat.type"); tr.Status != Skipped ||
		!strings.Contains(tr.Detail, "condition") {
		t.Fatalf("want condition skip, got %+v", tr)
	}

	business := rs.Validate(map[string]interface{}{"kind": "business", "vat": 123})
	if len(business.Errors) != 1 {
		t.Fatalf("condition met: want 1 error, got %v", business.Errors)
	}
}

func TestBatchIsolation(t *testing.T) {
	rs, err := NewRuleSet(
		IsInt("age", "age.type"),
		IntRange("age", "age.range", 0, 150, "age.type"),
	)
	if err != nil {
		t.Fatal(err)
	}
	batch := []map[string]interface{}{
		{"age": 30},
		{"age": "bad"},
		{"age": 200},
	}
	results := rs.ValidateBatch(batch)
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	if !results[0].OK() {
		t.Fatalf("element 0 should be clean, got %v", results[0].Errors)
	}
	if len(results[1].Errors) != 1 || results[1].Errors[0].RuleID != "age.type" {
		t.Fatalf("element 1 should fail type check only, got %v", results[1].Errors)
	}
	if len(results[2].Errors) != 1 || results[2].Errors[0].RuleID != "age.range" {
		t.Fatalf("element 2 should fail range check only, got %v", results[2].Errors)
	}
	// State must not leak: re-validating the clean element stays clean.
	if again := rs.Validate(batch[0]); !again.OK() {
		t.Fatalf("state leaked between validations: %v", again.Errors)
	}
}

func TestExplainRendersPerRuleModel(t *testing.T) {
	rs, err := NewRuleSet(
		IsInt("age", "age.type"),
		IntRange("age", "age.range", 0, 150, "age.type"),
	)
	if err != nil {
		t.Fatal(err)
	}
	out := rs.Validate(map[string]interface{}{"age": "bad"}).Explain()
	if !strings.Contains(out, "age/age.type: FAIL") {
		t.Fatalf("explain missing FAIL line:\n%s", out)
	}
	if !strings.Contains(out, "age/age.range: SKIP") {
		t.Fatalf("explain missing SKIP line:\n%s", out)
	}
}

func TestNestedFieldPaths(t *testing.T) {
	rs, err := NewRuleSet(IsString("user.email", "email.type"))
	if err != nil {
		t.Fatal(err)
	}
	bad := rs.Validate(map[string]interface{}{"user": map[string]interface{}{"email": 5}})
	if len(bad.Errors) != 1 || bad.Errors[0].Field != "user.email" {
		t.Fatalf("want nested path error, got %v", bad.Errors)
	}
	good := rs.Validate(map[string]interface{}{"user": map[string]interface{}{"email": "a@b.c"}})
	if !good.OK() {
		t.Fatalf("want clean, got %v", good.Errors)
	}
}
