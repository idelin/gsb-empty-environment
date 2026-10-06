package validate

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// interpret is an independent reference model of the validation semantics.
// Unlike the library's Kahn-order evaluation, it resolves rules by repeated
// fixpoint scans in rule-ID order: on every pass it evaluates each rule whose
// dependencies are all resolved, until no rule remains. The library output
// must match this model exactly.
func interpret(rules []Rule, data map[string]interface{}) (errs []Error, skips []Skip) {
	status := map[string]string{} // "passed" | "failed" | "skipped"
	reason := map[string]string{}
	remaining := map[string]Rule{}
	for _, r := range rules {
		remaining[r.ID] = r
	}

	for len(remaining) > 0 {
		ids := make([]string, 0, len(remaining))
		for id := range remaining {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		progressed := false
		for _, id := range ids {
			r := remaining[id]
			badDep := ""
			resolved := true
			for _, dep := range r.DependsOn {
				st, ok := status[dep]
				if !ok {
					resolved = false
					break
				}
				if st != "passed" && (badDep == "" || dep < badDep) {
					badDep = dep
				}
			}
			if !resolved {
				continue
			}
			progressed = true
			delete(remaining, id)
			if badDep != "" {
				status[id] = "skipped"
				reason[id] = fmt.Sprintf("dependency %q did not pass", badDep)
				continue
			}
			value, present := data[r.Field]
			if err := r.Check(value, present); err != nil {
				status[id] = "failed"
				errs = append(errs, Error{Field: r.Field, RuleID: id, Msg: err.Error()})
			} else {
				status[id] = "passed"
			}
		}
		if !progressed {
			panic("model: unresolvable rules (cycle slipped through)")
		}
	}

	for _, r := range rules {
		if status[r.ID] == "skipped" {
			skips = append(skips, Skip{Field: r.Field, RuleID: r.ID, Reason: reason[r.ID]})
		}
	}
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Field != errs[j].Field {
			return errs[i].Field < errs[j].Field
		}
		return errs[i].RuleID < errs[j].RuleID
	})
	sort.SliceStable(skips, func(i, j int) bool {
		if skips[i].Field != skips[j].Field {
			return skips[i].Field < skips[j].Field
		}
		return skips[i].RuleID < skips[j].RuleID
	})
	return errs, skips
}

var modelFields = []string{"a", "b", "c", "user.name", "user.age"}

func randomCheck(r *rand.Rand, depth int) CheckFunc {
	choice := r.Intn(9)
	if depth > 1 {
		choice = r.Intn(8) // avoid nesting Optional too deep
	}
	switch choice {
	case 0:
		return Required()
	case 1:
		return IsString()
	case 2:
		return IsInt()
	case 3:
		return IsBool()
	case 4:
		return Min(float64(r.Intn(50) - 25))
	case 5:
		return Max(float64(r.Intn(50)))
	case 6:
		return LenMin(r.Intn(4))
	case 7:
		return OneOf("x", "y", 1, 2)
	default:
		return Optional(randomCheck(r, depth+1))
	}
}

// randomDAG builds n rules whose dependencies only point to earlier rules,
// guaranteeing acyclicity while exercising arbitrary DAG shapes.
func randomDAG(r *rand.Rand, n int) []Rule {
	rules := make([]Rule, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("r%02d", i)
		var deps []string
		for j := 0; j < i; j++ {
			if r.Intn(3) == 0 {
				deps = append(deps, fmt.Sprintf("r%02d", j))
			}
		}
		rules[i] = Rule{
			ID:        id,
			Field:     modelFields[r.Intn(len(modelFields))],
			DependsOn: deps,
			Check:     randomCheck(r, 0),
		}
	}
	// Shuffle declaration order; evaluation must not depend on it.
	r.Shuffle(len(rules), func(i, j int) { rules[i], rules[j] = rules[j], rules[i] })
	return rules
}

func randomValue(r *rand.Rand) interface{} {
	switch r.Intn(6) {
	case 0:
		return fmt.Sprintf("s%d", r.Intn(100))
	case 1:
		return r.Intn(200) - 100
	case 2:
		return r.Float64()*200 - 100
	case 3:
		return r.Intn(2) == 0
	case 4:
		return nil
	default:
		return []string{"x", "y", "z"}[r.Intn(3)]
	}
}

func randomData(r *rand.Rand) map[string]interface{} {
	data := map[string]interface{}{}
	for _, f := range modelFields {
		if r.Intn(2) == 0 { // fields are randomly missing
			data[f] = randomValue(r)
		}
	}
	return data
}

func normalize(res *Result) ([]Error, []Skip) {
	var errs []Error
	if res.Errors != nil {
		errs = res.Errors
	}
	var skips []Skip
	if res.Skipped != nil {
		skips = res.Skipped
	}
	return errs, skips
}

func TestModelCrossCheck(t *testing.T) {
	for seed := int64(0); seed < 500; seed++ {
		r := rand.New(rand.NewSource(seed))
		rules := randomDAG(r, 1+r.Intn(10))
		rs, err := NewRuleSet(rules...)
		if err != nil {
			t.Fatalf("seed %d: NewRuleSet: %v", seed, err)
		}
		data := randomData(r)

		wantErrs, wantSkips := interpret(rules, data)
		gotErrs, gotSkips := normalize(rs.Validate(data))

		if !reflect.DeepEqual(gotErrs, wantErrs) {
			t.Errorf("seed %d: errors mismatch\ngot:  %+v\nwant: %+v", seed, gotErrs, wantErrs)
		}
		if !reflect.DeepEqual(gotSkips, wantSkips) {
			t.Errorf("seed %d: skips mismatch\ngot:  %+v\nwant: %+v", seed, gotSkips, wantSkips)
		}
	}
}

func TestModelBatchIsolation(t *testing.T) {
	for seed := int64(1000); seed < 1050; seed++ {
		r := rand.New(rand.NewSource(seed))
		rules := randomDAG(r, 2+r.Intn(8))
		rs, err := NewRuleSet(rules...)
		if err != nil {
			t.Fatalf("seed %d: NewRuleSet: %v", seed, err)
		}

		batch := make([]map[string]interface{}, 1+r.Intn(6))
		for i := range batch {
			batch[i] = randomData(r)
		}

		results := rs.ValidateBatch(batch)
		for i, data := range batch {
			wantErrs, wantSkips := interpret(rules, data)
			gotErrs, gotSkips := normalize(results[i])
			if !reflect.DeepEqual(gotErrs, wantErrs) || !reflect.DeepEqual(gotSkips, wantSkips) {
				t.Errorf("seed %d item %d: batch result diverges from model\ngot:  %+v %+v\nwant: %+v %+v",
					seed, i, gotErrs, gotSkips, wantErrs, wantSkips)
			}
			// Batch evaluation must not leak state between items.
			soloErrs, soloSkips := normalize(rs.Validate(data))
			if !reflect.DeepEqual(gotErrs, soloErrs) || !reflect.DeepEqual(gotSkips, soloSkips) {
				t.Errorf("seed %d item %d: batch result differs from solo validation", seed, i)
			}
		}
	}
}
