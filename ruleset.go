// Package validag validates map-shaped input against a set of field rules
// whose dependencies form a DAG. A failed base rule (e.g. a type check)
// causes its dependents to be skipped instead of producing misleading
// follow-up errors, while independent rules still run and report errors.
package validag

import (
	"fmt"
	"sort"
	"strings"
)

// Value is the field value handed to a rule's Check function.
type Value struct {
	Present bool
	Raw     interface{}
}

// Rule is a single validation rule bound to a field path.
type Rule struct {
	// ID uniquely identifies the rule inside a RuleSet.
	ID string
	// Field is a dot-separated path into the input, e.g. "user.email".
	Field string
	// Depends lists IDs of rules that must pass before this rule runs.
	Depends []string
	// SkipWhenMissing skips the rule (instead of running Check) when the
	// field is absent from the input. Presence rules set this to false.
	SkipWhenMissing bool
	// When, if non-nil, gates the rule on the whole input. A false result
	// skips the rule. Useful for conditional validation.
	When func(input map[string]interface{}) bool
	// Check returns nil when the value is valid.
	Check func(v Value) error
}

// RuleSet is an immutable, cycle-free collection of rules in a fixed
// topological evaluation order.
type RuleSet struct {
	rules []Rule
	order []int // indices into rules, topologically sorted
}

// NewRuleSet validates the rule set and returns it ready for use. It
// rejects empty or duplicate IDs, unknown dependencies, and dependency
// cycles.
func NewRuleSet(rules ...Rule) (*RuleSet, error) {
	if len(rules) == 0 {
		return nil, fmt.Errorf("validag: rule set is empty")
	}
	byID := make(map[string]int, len(rules))
	for i, r := range rules {
		if r.ID == "" {
			return nil, fmt.Errorf("validag: rule at index %d has empty ID", i)
		}
		if r.Field == "" {
			return nil, fmt.Errorf("validag: rule %q has empty field", r.ID)
		}
		if r.Check == nil {
			return nil, fmt.Errorf("validag: rule %q has nil Check", r.ID)
		}
		if _, dup := byID[r.ID]; dup {
			return nil, fmt.Errorf("validag: duplicate rule ID %q", r.ID)
		}
		byID[r.ID] = i
	}
	indegree := make([]int, len(rules))
	dependents := make([][]int, len(rules))
	for i, r := range rules {
		for _, dep := range r.Depends {
			j, ok := byID[dep]
			if !ok {
				return nil, fmt.Errorf("validag: rule %q depends on unknown rule %q", r.ID, dep)
			}
			if j == i {
				return nil, fmt.Errorf("validag: rule %q depends on itself", r.ID)
			}
			indegree[i]++
			dependents[j] = append(dependents[j], i)
		}
	}
	// Kahn's algorithm; the ready set is kept sorted by (Field, ID) so the
	// evaluation order is deterministic regardless of registration order.
	var ready []int
	for i := range rules {
		if indegree[i] == 0 {
			ready = append(ready, i)
		}
	}
	less := func(a, b int) bool { return ruleLess(rules[a], rules[b]) }
	sort.Slice(ready, func(x, y int) bool { return less(ready[x], ready[y]) })
	order := make([]int, 0, len(rules))
	for len(ready) > 0 {
		next := ready[0]
		ready = ready[1:]
		order = append(order, next)
		for _, d := range dependents[next] {
			indegree[d]--
			if indegree[d] == 0 {
				k := sort.Search(len(ready), func(k int) bool { return !less(ready[k], d) })
				ready = append(ready, 0)
				copy(ready[k+1:], ready[k:])
				ready[k] = d
			}
		}
	}
	if len(order) != len(rules) {
		var cyclic []string
		for i := range rules {
			if indegree[i] > 0 {
				cyclic = append(cyclic, rules[i].ID)
			}
		}
		sort.Strings(cyclic)
		return nil, fmt.Errorf("validag: dependency cycle detected among rules: %s", strings.Join(cyclic, ", "))
	}
	return &RuleSet{rules: rules, order: order}, nil
}

func ruleLess(a, b Rule) bool {
	if a.Field != b.Field {
		return a.Field < b.Field
	}
	return a.ID < b.ID
}
