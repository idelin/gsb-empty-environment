package netsec

// Effect is the decision a rule contributes.
type Effect int

const (
	// Allow grants the matching points, unless a Deny also matches.
	Allow Effect = iota
	// Deny rejects the matching points and takes precedence over
	// every Allow.
	Deny
)

// Rule is one policy statement: a boolean condition (a rectangle
// over the finite domain) plus an effect.
type Rule struct {
	Name   string `json:"name"`
	Effect Effect `json:"effect"`
	When   Rect   `json:"when"`
}

// Policy is an unordered rule list evaluated with deny precedence.
type Policy struct {
	Rules []Rule `json:"rules"`
}

// Allows reports whether the policy grants pt: at least one Allow
// rule matches and no Deny rule does. Points matched by no Allow
// rule are rejected (default deny).
func (p *Policy) Allows(pt Point) bool {
	allowed := false
	for _, r := range p.Rules {
		if !r.When.Contains(pt) {
			continue
		}
		if r.Effect == Deny {
			return false
		}
		allowed = true
	}
	return allowed
}

func (p *Policy) rulesByEffect(e Effect) []Rule {
	var out []Rule
	for _, r := range p.Rules {
		if r.Effect == e {
			out = append(out, r)
		}
	}
	return out
}

// Clip intersects every rule condition with r and drops rules that no
// longer match anything. Use it to restrict a policy to a remainder
// rectangle before re-running Diff on that shard.
func Clip(p *Policy, r Rect) *Policy {
	out := &Policy{}
	for _, rule := range p.Rules {
		when, ok := rule.When.Intersect(r)
		if !ok {
			continue
		}
		rule.When = when
		out.Rules = append(out.Rules, rule)
	}
	return out
}
