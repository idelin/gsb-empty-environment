package validate

import (
	"reflect"
	"strings"
	"testing"
)

func TestBaseTypeFailureSkipsDependents(t *testing.T) {
	rs, err := NewRuleSet(
		Rule{ID: "age-type", Field: "age", Check: IsInt()},
		Rule{ID: "age-min", Field: "age", DependsOn: []string{"age-type"}, Check: Min(0)},
		Rule{ID: "age-max", Field: "age", DependsOn: []string{"age-min"}, Check: Max(150)},
		Rule{ID: "name-req", Field: "name", Check: Required()},
	)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	res := rs.Validate(map[string]interface{}{"age": "not-a-number"})

	wantErrs := []Error{
		{Field: "age", RuleID: "age-type", Msg: "expected int, got string"},
		{Field: "name", RuleID: "name-req", Msg: "field is required"},
	}
	if !reflect.DeepEqual(res.Errors, wantErrs) {
		t.Errorf("Errors = %+v, want %+v", res.Errors, wantErrs)
	}

	wantSkips := []Skip{
		{Field: "age", RuleID: "age-max", Reason: `dependency "age-min" did not pass`},
		{Field: "age", RuleID: "age-min", Reason: `dependency "age-type" did not pass`},
	}
	if !reflect.DeepEqual(res.Skipped, wantSkips) {
		t.Errorf("Skipped = %+v, want %+v", res.Skipped, wantSkips)
	}
}

func TestIndependentErrorsCollected(t *testing.T) {
	rs, err := NewRuleSet(
		Rule{ID: "a-type", Field: "a", Check: IsInt()},
		Rule{ID: "a-min", Field: "a", DependsOn: []string{"a-type"}, Check: Min(10)},
		Rule{ID: "b-min", Field: "b", Check: Min(10)},
		Rule{ID: "c-req", Field: "c", Check: Required()},
	)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	res := rs.Validate(map[string]interface{}{"a": "bad", "b": 1})

	var got []string
	for _, e := range res.Errors {
		got = append(got, e.RuleID)
	}
	want := []string{"a-type", "b-min", "c-req"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("error rule IDs = %v, want %v", got, want)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].RuleID != "a-min" {
		t.Errorf("Skipped = %+v, want only a-min", res.Skipped)
	}
}

func TestCycleRejected(t *testing.T) {
	ok := Required()
	cases := []struct {
		name  string
		rules []Rule
		want  string
	}{
		{"self", []Rule{{ID: "a", Field: "f", DependsOn: []string{"a"}, Check: ok}}, "a -> a"},
		{"two", []Rule{
			{ID: "a", Field: "f", DependsOn: []string{"b"}, Check: ok},
			{ID: "b", Field: "f", DependsOn: []string{"a"}, Check: ok},
		}, "a -> b -> a"},
		{"three", []Rule{
			{ID: "a", Field: "f", DependsOn: []string{"c"}, Check: ok},
			{ID: "b", Field: "f", DependsOn: []string{"a"}, Check: ok},
			{ID: "c", Field: "f", DependsOn: []string{"b"}, Check: ok},
		}, "a -> c -> b -> a"},
		{"cycle among acyclic", []Rule{
			{ID: "root", Field: "f", Check: ok},
			{ID: "x", Field: "f", DependsOn: []string{"root", "z"}, Check: ok},
			{ID: "y", Field: "f", DependsOn: []string{"x"}, Check: ok},
			{ID: "z", Field: "f", DependsOn: []string{"y"}, Check: ok},
		}, "cycle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs, err := NewRuleSet(tc.rules...)
			if err == nil {
				t.Fatalf("expected cycle error, got rule set %+v", rs)
			}
			if !strings.Contains(err.Error(), "cycle") {
				t.Errorf("error %q does not mention cycle", err)
			}
			if tc.want != "cycle" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain cycle path %q", err, tc.want)
			}
		})
	}
}

func TestInvalidRuleSetsRejected(t *testing.T) {
	ok := Required()
	cases := []struct {
		name  string
		rules []Rule
		want  string
	}{
		{"empty ID", []Rule{{ID: "", Field: "f", Check: ok}}, "empty ID"},
		{"nil check", []Rule{{ID: "a", Field: "f"}}, "nil Check"},
		{"duplicate ID", []Rule{
			{ID: "a", Field: "f", Check: ok},
			{ID: "a", Field: "g", Check: ok},
		}, "duplicate"},
		{"unknown dependency", []Rule{
			{ID: "a", Field: "f", DependsOn: []string{"nope"}, Check: ok},
		}, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewRuleSet(tc.rules...); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestErrorsSortedByFieldThenRuleID(t *testing.T) {
	// Rules deliberately declared in non-sorted order.
	rs, err := NewRuleSet(
		Rule{ID: "z2", Field: "z", Check: Min(100)},
		Rule{ID: "a2", Field: "a", Check: Min(100)},
		Rule{ID: "z1", Field: "z", Check: IsInt()},
		Rule{ID: "a1", Field: "a", Check: IsInt()},
		Rule{ID: "m1", Field: "m", Check: Required()},
	)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}
	res := rs.Validate(map[string]interface{}{"a": 5, "z": 5})

	var got []string
	for _, e := range res.Errors {
		got = append(got, e.Field+"/"+e.RuleID)
	}
	want := []string{"a/a2", "m/m1", "z/z2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}

	// Deterministic across repeated runs.
	again := rs.Validate(map[string]interface{}{"a": 5, "z": 5})
	if !reflect.DeepEqual(res, again) {
		t.Errorf("repeated validation differs: %+v vs %+v", res, again)
	}
}

func TestMissingAndConditional(t *testing.T) {
	rs, err := NewRuleSet(
		Rule{ID: "req", Field: "must", Check: Required()},
		Rule{ID: "opt", Field: "maybe", Check: Optional(Min(0))},
		Rule{ID: "type", Field: "cond", Check: IsString()},
		Rule{ID: "len", Field: "cond", DependsOn: []string{"type"}, Check: LenMin(3)},
	)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	// Missing optional field passes; missing required fails.
	res := rs.Validate(map[string]interface{}{"cond": "ab"})
	if len(res.Errors) != 2 || res.Errors[0].RuleID != "len" || res.Errors[1].RuleID != "req" {
		t.Errorf("Errors = %+v, want [cond/len, must/req]", res.Errors)
	}

	// Present optional field is validated.
	res = rs.Validate(map[string]interface{}{"must": 1, "maybe": -5, "cond": "abc"})
	if len(res.Errors) != 1 || res.Errors[0].RuleID != "opt" {
		t.Errorf("Errors = %+v, want [maybe/opt]", res.Errors)
	}

	// Wrong base type skips the conditional length check.
	res = rs.Validate(map[string]interface{}{"must": 1, "cond": 42})
	if len(res.Errors) != 1 || res.Errors[0].RuleID != "type" {
		t.Errorf("Errors = %+v, want [cond/type]", res.Errors)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].RuleID != "len" {
		t.Errorf("Skipped = %+v, want [cond/len]", res.Skipped)
	}
}

func TestBatchIsolation(t *testing.T) {
	rs, err := NewRuleSet(
		Rule{ID: "t", Field: "n", Check: IsInt()},
		Rule{ID: "m", Field: "n", DependsOn: []string{"t"}, Check: Min(0)},
	)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}
	batch := []map[string]interface{}{
		{"n": "bad"},
		{"n": 5},
		{},
		{"n": -1},
	}
	results := rs.ValidateBatch(batch)
	if len(results) != len(batch) {
		t.Fatalf("got %d results, want %d", len(results), len(batch))
	}
	for i, data := range batch {
		solo := rs.Validate(data)
		if !reflect.DeepEqual(results[i], solo) {
			t.Errorf("item %d: batch result %+v != solo %+v", i, results[i], solo)
		}
	}
	if results[0].OK() || !results[1].OK() || results[2].OK() || results[3].OK() {
		t.Errorf("OK flags = %v %v %v %v, want false true false false",
			results[0].OK(), results[1].OK(), results[2].OK(), results[3].OK())
	}
}
