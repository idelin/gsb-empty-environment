package netsec

import "fmt"

// Effect is the decision attached to a rule.
type Effect string

const (
	Allow Effect = "allow"
	Deny  Effect = "deny"
)

// Rule pairs an effect with a boolean condition over the finite domains.
type Rule struct {
	ID        string
	Effect    Effect
	Condition Condition
}

// Policy is an ordered collection of rules. Evaluation scans every matching
// rule; any Deny wins. A point matched by no rule is denied by default.
type Policy struct {
	Name  string
	Rules []Rule
}

type compiledRule struct {
	rule Rule
	node node
}

type compiledPolicy struct {
	policy Policy
	rules  []compiledRule
}

// Hit records one rule that fired on a point.
type Hit struct {
	RuleID string
	Effect Effect
}

// Decision is the effective outcome of evaluating a policy on a point.
type Decision struct {
	// Allowed is true only when at least one allow rule fires and no deny
	// rule fires.
	Allowed bool
	Hits    []Hit
}

func compilePolicy(u *Universe, p Policy) (compiledPolicy, error) {
	compiled := compiledPolicy{policy: p, rules: make([]compiledRule, 0, len(p.Rules))}
	seenIDs := make(map[string]bool)
	for i, rule := range p.Rules {
		id := rule.ID
		if id == "" {
			id = fmt.Sprintf("rule-%d", i)
		}
		if seenIDs[id] {
			return compiledPolicy{}, fmt.Errorf("netsec: duplicate rule id %q in policy %q", id, p.Name)
		}
		seenIDs[id] = true
		if rule.Effect != Allow && rule.Effect != Deny {
			return compiledPolicy{}, fmt.Errorf("netsec: rule %q has invalid effect %q", id, rule.Effect)
		}
		compiledNode, err := compileCondition(u, rule.Condition)
		if err != nil {
			return compiledPolicy{}, fmt.Errorf("netsec: rule %q: %w", id, err)
		}
		compiled.rules = append(compiled.rules, compiledRule{
			rule: Rule{ID: id, Effect: rule.Effect, Condition: rule.Condition},
			node: compiledNode,
		})
	}
	return compiled, nil
}

// decide evaluates the compiled policy on an index tuple. Deny takes
// precedence over allow; no match means deny.
func (p compiledPolicy) decide(point []int) Decision {
	var hits []Hit
	allowed := false
	for _, compiled := range p.rules {
		if !compiled.node.holds(point) {
			continue
		}
		hits = append(hits, Hit{RuleID: compiled.rule.ID, Effect: compiled.rule.Effect})
		if compiled.rule.Effect == Allow {
			allowed = true
		} else {
			allowed = false
		}
	}
	return Decision{Allowed: allowed, Hits: hits}
}
