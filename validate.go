package netsec

import (
	"fmt"
	"sort"
)

// Validate checks the whole policy set for structural errors: empty
// names, invalid conditions or tags, dangling sub-policy references,
// recursive call graphs and unreachable rules.
func (ps *PolicySet) Validate() error {
	if ps == nil || len(ps.Policies) == 0 {
		return fmt.Errorf("policy set is empty")
	}
	names := make([]string, 0, len(ps.Policies))
	for name, p := range ps.Policies {
		if name == "" {
			return fmt.Errorf("policy with empty name")
		}
		if p == nil {
			return fmt.Errorf("policy %q is nil", name)
		}
		if p.Name != name {
			return fmt.Errorf("policy map key %q does not match name %q", name, p.Name)
		}
		if len(p.Rules) == 0 {
			return fmt.Errorf("policy %q has no rules", name)
		}
		for i := range p.Rules {
			rule := &p.Rules[i]
			if rule.Name == "" {
				return fmt.Errorf("policy %q rule %d has empty name", name, i)
			}
			if rule.Do == nil {
				return fmt.Errorf("policy %q rule %q has no action", name, rule.Name)
			}
			if rule.When != nil {
				if err := rule.When.validate(); err != nil {
					return fmt.Errorf("policy %q rule %q condition: %w", name, rule.Name, err)
				}
			}
			if err := rule.Do.validate(ps); err != nil {
				return fmt.Errorf("policy %q rule %q action: %w", name, rule.Name, err)
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(name, via string) error
	visit = func(name, via string) error {
		switch color[name] {
		case gray:
			return fmt.Errorf("recursive sub-policy call involving %q (via %q)", name, via)
		case black:
			return nil
		}
		color[name] = gray
		for _, callee := range ps.calls(name) {
			if err := visit(callee, name); err != nil {
				return err
			}
		}
		color[name] = black
		return nil
	}
	for _, name := range names {
		if err := visit(name, ""); err != nil {
			return err
		}
	}

	for _, name := range names {
		if dead := ps.firstUnreachableRule(name); dead != "" {
			return fmt.Errorf("policy %q rule %q is unreachable", name, dead)
		}
	}
	return nil
}

func (ps *PolicySet) calls(name string) []string {
	out := []string{}
	for _, r := range ps.Policies[name].Rules {
		if c, ok := r.Do.(CallPolicy); ok {
			out = append(out, c.Name)
		}
	}
	return out
}

// firstUnreachableRule returns the name of the first rule that can
// never execute, or "" when every rule is reachable. A rule is dead
// when an earlier unconditional terminal rule, or an unconditional call
// into an always-terminal policy, must have already returned. The call
// graph is already known to be acyclic when this runs.
func (ps *PolicySet) firstUnreachableRule(name string) string {
	p := ps.Policies[name]
	for i, rule := range p.Rules {
		if i == 0 || rule.When != nil {
			continue
		}
		for j := 0; j < i; j++ {
			prev := &p.Rules[j]
			if prev.When != nil {
				continue
			}
			switch a := prev.Do.(type) {
			case Accept, Reject:
				return rule.Name
			case CallPolicy:
				if ps.policyAlwaysTerminates(a.Name) {
					return rule.Name
				}
			}
		}
	}
	return ""
}

// policyAlwaysTerminates reports whether every possible execution of the
// policy reaches an accept or reject verdict.
func (ps *PolicySet) policyAlwaysTerminates(name string) bool {
	p := ps.Policies[name]
	for _, rule := range p.Rules {
		if rule.When != nil {
			return false
		}
		switch a := rule.Do.(type) {
		case Accept, Reject:
			return true
		case CallPolicy:
			if ps.policyAlwaysTerminates(a.Name) {
				return true
			}
		}
	}
	return false
}
