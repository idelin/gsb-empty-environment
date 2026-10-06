// Package validate provides rule-set based validation for map-shaped data.
//
// Rules attached to field paths form a dependency DAG. When a base rule
// (e.g. a type check) fails, rules that depend on it are skipped instead of
// reporting misleading follow-up errors, while independent rules are still
// evaluated and their errors collected. Rule sets containing dependency
// cycles are rejected at construction time. Errors are reported in a stable
// order sorted by field path and rule ID.
package validate

import (
	"fmt"
	"sort"
	"strings"
)

// CheckFunc validates a single field value. present is false when the field
// is absent from the input data. It returns nil on success and an error
// describing the violation on failure.
type CheckFunc func(value interface{}, present bool) error

// Rule is a single validation rule bound to a field path.
type Rule struct {
	// ID uniquely identifies the rule within a rule set.
	ID string
	// Field is the field path this rule validates.
	Field string
	// DependsOn lists IDs of rules that must pass before this rule runs.
	// If any dependency fails or is skipped, this rule is skipped.
	DependsOn []string
	// Check performs the actual validation.
	Check CheckFunc
}

// RuleSet is an immutable, validated collection of rules evaluated as a DAG.
type RuleSet struct {
	rules []Rule // topological evaluation order
	byID  map[string]int
}

// NewRuleSet validates rules and builds a RuleSet. It rejects empty IDs,
// nil checks, duplicate IDs, unknown dependencies and dependency cycles.
func NewRuleSet(rules ...Rule) (*RuleSet, error) {
	byID := make(map[string]int, len(rules))
	for i, r := range rules {
		if r.ID == "" {
			return nil, fmt.Errorf("validate: rule at index %d has empty ID", i)
		}
		if r.Check == nil {
			return nil, fmt.Errorf("validate: rule %q has nil Check", r.ID)
		}
		if _, dup := byID[r.ID]; dup {
			return nil, fmt.Errorf("validate: duplicate rule ID %q", r.ID)
		}
		byID[r.ID] = i
	}

	indeg := make(map[string]int, len(rules))
	dependents := make(map[string][]string, len(rules))
	for _, r := range rules {
		indeg[r.ID] += 0
		for _, dep := range r.DependsOn {
			if _, ok := byID[dep]; !ok {
				return nil, fmt.Errorf("validate: rule %q depends on unknown rule %q", r.ID, dep)
			}
			dependents[dep] = append(dependents[dep], r.ID)
			indeg[r.ID]++
		}
	}

	// Kahn's algorithm with deterministic tie-breaking by rule ID.
	var ready []string
	for id, d := range indeg {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	order := make([]Rule, 0, len(rules))
	index := make(map[string]int, len(rules))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		index[id] = len(order)
		order = append(order, rules[byID[id]])
		next := dependents[id]
		sort.Strings(next)
		for _, m := range next {
			indeg[m]--
			if indeg[m] == 0 {
				ready = insertSorted(ready, m)
			}
		}
	}

	if len(order) != len(rules) {
		return nil, fmt.Errorf("validate: dependency cycle detected: %s", strings.Join(findCycle(rules, byID), " -> "))
	}
	return &RuleSet{rules: order, byID: index}, nil
}

func insertSorted(s []string, v string) []string {
	i := sort.SearchStrings(s, v)
	s = append(s, "")
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

// findCycle returns one dependency cycle among the given rules, for diagnostics.
func findCycle(rules []Rule, byID map[string]int) []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(rules))
	var stack []string
	var cycle []string

	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = gray
		stack = append(stack, id)
		for _, dep := range rules[byID[id]].DependsOn {
			switch color[dep] {
			case gray:
				// Found a back edge; extract the cycle from the stack.
				for i, s := range stack {
					if s == dep {
						cycle = append(append([]string{}, stack[i:]...), dep)
						return true
					}
				}
			case white:
				if visit(dep) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return false
	}

	ids := make([]string, 0, len(rules))
	for _, r := range rules {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if color[id] == white {
			if visit(id) {
				return cycle
			}
		}
	}
	return []string{"<unknown>"}
}
