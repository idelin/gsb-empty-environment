package impersonate

import (
	"context"
	"strings"
)

// Rule permits one impersonation check. An empty Name matches any
// resourceName; otherwise the resourceName must match exactly. Extra-key
// rules match Resource=userextras with Subresource set to the key.
type Rule struct {
	APIGroup    string
	Namespace   string
	Resource    string
	Subresource string
	Name        string
}

// RuleAuthorizer is a small in-memory Authorizer: a check is allowed only
// when an exact matching rule exists, everything else returns no opinion
// (which the middleware denies).
type RuleAuthorizer struct {
	Rules []Rule
}

// Authorize implements Authorizer.
func (a *RuleAuthorizer) Authorize(_ context.Context, attrs Attributes) (Decision, string, error) {
	for _, rule := range a.Rules {
		if rule.APIGroup != attrs.APIGroup ||
			rule.Namespace != attrs.Namespace ||
			rule.Resource != attrs.Resource ||
			rule.Subresource != attrs.Subresource {
			continue
		}
		if rule.Name != "" && rule.Name != attrs.Name {
			continue
		}
		return DecisionAllow, "", nil
	}
	return DecisionNoOpinion, "no matching impersonation rule for " + describe(attrs), nil
}

func describe(attrs Attributes) string {
	var b strings.Builder
	b.WriteString(attrs.Verb)
	b.WriteString(" ")
	b.WriteString(attrs.ResourcePath())
	if attrs.Name != "" {
		b.WriteString("/")
		b.WriteString(attrs.Name)
	}
	return b.String()
}
