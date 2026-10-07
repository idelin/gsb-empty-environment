package validag

import (
	"fmt"
	"sort"
	"strings"
)

// Status is the outcome of evaluating a single rule.
type Status int

const (
	Passed Status = iota
	Failed
	Skipped
)

func (s Status) String() string {
	switch s {
	case Passed:
		return "PASS"
	case Failed:
		return "FAIL"
	case Skipped:
		return "SKIP"
	}
	return "UNKNOWN"
}

// Trace explains what happened to one rule during validation: whether it
// passed, failed (Detail holds the error), or was skipped (Detail holds
// the reason, e.g. a failed dependency, a missing field, or an unmet
// condition).
type Trace struct {
	Field  string
	RuleID string
	Status Status
	Detail string
}

// Error is a single validation failure.
type Error struct {
	Field  string
	RuleID string
	Msg    string
}

func (e Error) String() string {
	return fmt.Sprintf("%s: rule %q: %s", e.Field, e.RuleID, e.Msg)
}

// Result is the outcome of validating one input. Errors are sorted stably
// by field path, then rule ID. Traces follow the deterministic topological
// evaluation order so causal chains (fail -> skip) read naturally.
type Result struct {
	Errors []Error
	Traces []Trace
}

// OK reports whether the input produced no errors.
func (r *Result) OK() bool { return len(r.Errors) == 0 }

// Explain renders the per-rule explanation model, one line per rule in
// evaluation order.
func (r *Result) Explain() string {
	var b strings.Builder
	for _, t := range r.Traces {
		fmt.Fprintf(&b, "%s/%s: %s", t.Field, t.RuleID, t.Status)
		if t.Detail != "" {
			fmt.Fprintf(&b, ": %s", t.Detail)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Validate runs every applicable rule against input in topological order.
// A rule is skipped when a dependency did not pass, its condition is
// unmet, or its field is missing and it declared SkipWhenMissing. Skipped
// rules never produce errors; independent rules still run and their
// errors are collected.
func (rs *RuleSet) Validate(input map[string]interface{}) *Result {
	if input == nil {
		input = map[string]interface{}{}
	}
	state := make(map[string]Status, len(rs.rules))
	res := &Result{}
	for _, idx := range rs.order {
		rule := rs.rules[idx]
		trace := Trace{Field: rule.Field, RuleID: rule.ID}

		if blocker := blockingDependency(rule, state); blocker != "" {
			trace.Status = Skipped
			trace.Detail = fmt.Sprintf("dependency %q did not pass", blocker)
			res.Traces = append(res.Traces, trace)
			state[rule.ID] = Skipped
			continue
		}
		if rule.When != nil && !rule.When(input) {
			trace.Status = Skipped
			trace.Detail = "condition not met"
			res.Traces = append(res.Traces, trace)
			state[rule.ID] = Skipped
			continue
		}
		raw, present := lookup(input, rule.Field)
		if !present && rule.SkipWhenMissing {
			trace.Status = Skipped
			trace.Detail = fmt.Sprintf("field %q missing", rule.Field)
			res.Traces = append(res.Traces, trace)
			state[rule.ID] = Skipped
			continue
		}
		if err := rule.Check(Value{Present: present, Raw: raw}); err != nil {
			trace.Status = Failed
			trace.Detail = err.Error()
			res.Errors = append(res.Errors, Error{Field: rule.Field, RuleID: rule.ID, Msg: err.Error()})
			state[rule.ID] = Failed
		} else {
			trace.Status = Passed
			state[rule.ID] = Passed
		}
		res.Traces = append(res.Traces, trace)
	}
	sort.SliceStable(res.Errors, func(i, j int) bool {
		if res.Errors[i].Field != res.Errors[j].Field {
			return res.Errors[i].Field < res.Errors[j].Field
		}
		return res.Errors[i].RuleID < res.Errors[j].RuleID
	})
	return res
}

// ValidateBatch validates each input independently and returns one Result
// per input, aligned by index. No state is shared between inputs, so a
// failing element never affects its neighbours.
func (rs *RuleSet) ValidateBatch(inputs []map[string]interface{}) []*Result {
	results := make([]*Result, len(inputs))
	for i, input := range inputs {
		results[i] = rs.Validate(input)
	}
	return results
}

func blockingDependency(rule Rule, state map[string]Status) string {
	for _, dep := range rule.Depends {
		if state[dep] != Passed {
			return dep
		}
	}
	return ""
}

// lookup walks a dot-separated path through nested maps.
func lookup(input map[string]interface{}, path string) (interface{}, bool) {
	var cur interface{} = input
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
