package validate

import (
	"fmt"
	"sort"
)

// Error describes a single failed rule.
type Error struct {
	Field  string
	RuleID string
	Msg    string
}

func (e Error) Error() string {
	return fmt.Sprintf("%s: rule %s: %s", e.Field, e.RuleID, e.Msg)
}

// Skip describes a rule that was not evaluated because a dependency did not pass.
type Skip struct {
	Field  string
	RuleID string
	Reason string
}

// Result is the outcome of validating one input.
type Result struct {
	// Errors contains every failed rule, sorted by (Field, RuleID).
	Errors []Error
	// Skipped contains every rule skipped due to a failed or skipped
	// dependency, sorted by (Field, RuleID).
	Skipped []Skip
}

// OK reports whether the input passed all evaluated rules.
func (r *Result) OK() bool { return len(r.Errors) == 0 }

type ruleStatus int

const (
	statusPassed ruleStatus = iota
	statusFailed
	statusSkipped
)

// Validate evaluates the rule set against data. Rules whose dependencies
// failed or were skipped are themselves skipped rather than evaluated, so a
// single base-type failure does not cascade into misleading errors; rules
// independent of the failure are still evaluated and their errors collected.
func (s *RuleSet) Validate(data map[string]interface{}) *Result {
	status := make([]ruleStatus, len(s.rules))
	skipReason := make([]string, len(s.rules))
	var errs []Error

	for i, r := range s.rules {
		badDep := ""
		for _, depID := range r.DependsOn {
			if status[s.byID[depID]] != statusPassed && (badDep == "" || depID < badDep) {
				badDep = depID
			}
		}
		if badDep != "" {
			status[i] = statusSkipped
			skipReason[i] = fmt.Sprintf("dependency %q did not pass", badDep)
			continue
		}
		value, present := data[r.Field]
		if err := r.Check(value, present); err != nil {
			status[i] = statusFailed
			errs = append(errs, Error{Field: r.Field, RuleID: r.ID, Msg: err.Error()})
		}
	}

	var skips []Skip
	for i, r := range s.rules {
		if status[i] == statusSkipped {
			skips = append(skips, Skip{Field: r.Field, RuleID: r.ID, Reason: skipReason[i]})
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
	return &Result{Errors: errs, Skipped: skips}
}

// ValidateBatch validates each item independently and returns one Result per
// item. Every item is evaluated with fresh state, so results are identical to
// validating each item on its own.
func (s *RuleSet) ValidateBatch(batch []map[string]interface{}) []*Result {
	results := make([]*Result, len(batch))
	for i, data := range batch {
		results[i] = s.Validate(data)
	}
	return results
}
