package netsec

import "sync"

// Rule is a single authorization permission. It follows the Kubernetes RBAC
// shape: a subject may perform Verb on Resource[/Subresource], optionally
// restricted to specific resource names. An empty ResourceNames slice means
// the rule applies to any name; "*" matches every name.
type Rule struct {
	Subject       string
	Verb          string
	Resource      string
	Subresource   string
	ResourceNames []string
}

// RuleAuthorizer is a concurrency-safe, RBAC-like Authorizer backed by an
// explicit rule list. Absence of a matching rule is DecisionNoOpinion rather
// than an allow, so callers cannot gain privileges by omission.
type RuleAuthorizer struct {
	mu    sync.RWMutex
	rules []Rule
}

// NewRuleAuthorizer builds an authorizer containing the given rules.
func NewRuleAuthorizer(rules ...Rule) *RuleAuthorizer {
	copied := append([]Rule(nil), rules...)
	return &RuleAuthorizer{rules: copied}
}

// Add appends a rule and returns the authorizer for chaining.
func (a *RuleAuthorizer) Add(rule Rule) *RuleAuthorizer {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rules = append(a.rules, rule)
	return a
}

// Rules returns a copy of the configured rules.
func (a *RuleAuthorizer) Rules() []Rule {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]Rule(nil), a.rules...)
}

// Authorize implements Authorizer.
func (a *RuleAuthorizer) Authorize(attrs Attributes) (Decision, string, error) {
	if attrs.User == nil {
		return DecisionDeny, "no authenticated user", nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, rule := range a.rules {
		if !a.matches(rule, attrs) {
			continue
		}
		return DecisionAllow, "", nil
	}
	return DecisionNoOpinion, "no matching authorization rule", nil
}

func (a *RuleAuthorizer) matches(rule Rule, attrs Attributes) bool {
	if rule.Subject != attrs.User.GetName() {
		return false
	}
	if rule.Verb != attrs.Verb {
		return false
	}
	if rule.Resource != attrs.Resource || rule.Subresource != attrs.Subresource {
		return false
	}
	if len(rule.ResourceNames) == 0 {
		return true
	}
	if attrs.Name == "" {
		// A name-scoped rule cannot satisfy a name-less check.
		return false
	}
	for _, allowedName := range rule.ResourceNames {
		if allowedName == "*" || allowedName == attrs.Name {
			return true
		}
	}
	return false
}
