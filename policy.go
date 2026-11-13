package netsec

import "strings"

// PolicyRule is the subset of a Kubernetes RBAC policy rule needed to decide
// whether a requested permission is already held by a user.
//
// An empty ResourceNames slice means that the rule applies to every resource
// name. The empty string in APIGroups denotes the Kubernetes core API group.
type PolicyRule struct {
	APIGroups       []string `json:"apiGroups,omitempty"`
	Resources       []string `json:"resources,omitempty"`
	Verbs           []string `json:"verbs"`
	ResourceNames   []string `json:"resourceNames,omitempty"`
	NonResourceURLs []string `json:"nonResourceURLs,omitempty"`
}

// RuleCovers reports whether one granted rule covers every permission in a
// requested rule.
//
// Coverage is directional. A wildcard in the granted rule covers concrete
// values in the requested rule, but a concrete value never covers a requested
// wildcard. Resource names use subset semantics: a granted rule without
// resourceNames covers a requested rule with or without names, while a granted
// named rule cannot cover a requested rule that applies to all names.
//
// Resources are matched verbatim apart from the resource wildcard. In
// particular, "pods" does not cover "pods/exec"; a rule must grant either
// "pods/exec" explicitly or use "*".
func RuleCovers(granted, requested PolicyRule) bool {
	for _, verb := range requested.Verbs {
		if !containsMatch(granted.Verbs, verb) {
			return false
		}
	}

	if len(requested.NonResourceURLs) > 0 {
		for _, url := range requested.NonResourceURLs {
			if !containsMatch(granted.NonResourceURLs, url) {
				return false
			}
		}
	}

	for _, group := range requested.APIGroups {
		if !containsMatch(granted.APIGroups, group) {
			return false
		}
	}
	for _, resource := range requested.Resources {
		if !containsMatch(granted.Resources, resource) {
			return false
		}
	}

	return namesCovered(granted.ResourceNames, requested.ResourceNames)
}

// RulesCover reports whether the union of granted rules covers every
// permission represented by the requested rules.
//
// A requested rule can be covered by multiple granted rules. For example, one
// granted rule may provide "get" and another may provide "list" for the same
// resource. This is equivalent to authorizing every concrete
// (apiGroup, resource, verb, resourceName) tuple requested by the role.
func RulesCover(granted, requested []PolicyRule) bool {
	for _, rule := range requested {
		for _, attrs := range ruleAttributes(rule) {
			if !rulesAllow(granted, attrs) {
				return false
			}
		}
	}
	return true
}

// rulesAllow checks one concrete authorization request against a set of rules.
func rulesAllow(rules []PolicyRule, attrs Attributes) bool {
	for _, rule := range rules {
		if ruleAllows(rule, attrs) {
			return true
		}
	}
	return false
}

func ruleAllows(rule PolicyRule, attrs Attributes) bool {
	if !containsMatch(rule.Verbs, attrs.Verb) {
		return false
	}

	// Role rules are resource rules. Non-resource URL rules are valid only on
	// ClusterRoles, but accepting them here keeps the matching helpers useful
	// for callers that validate ClusterRole policy as well.
	if attrs.NonResourceURL != "" {
		return matchWildcard(rule.NonResourceURLs, attrs.NonResourceURL)
	}

	if !containsMatch(rule.APIGroups, attrs.APIGroup) {
		return false
	}
	if !containsMatch(rule.Resources, attrs.Resource) {
		return false
	}
	return resourceNameAllowed(rule.ResourceNames, attrs.Name)
}

// ruleAttributes expands a rule into the concrete authorization requests it
// represents. An empty name is the authorization attribute for "all names",
// which is how an empty resourceNames list is represented.
func ruleAttributes(rule PolicyRule) []Attributes {
	var attrs []Attributes

	for _, verb := range rule.Verbs {
		for _, url := range rule.NonResourceURLs {
			attrs = append(attrs, Attributes{
				Verb:           verb,
				NonResourceURL: url,
			})
		}

		names := rule.ResourceNames
		if len(names) == 0 {
			names = []string{""}
		}
		for _, group := range rule.APIGroups {
			for _, resource := range rule.Resources {
				for _, name := range names {
					attrs = append(attrs, Attributes{
						APIGroup: group,
						Resource: resource,
						Verb:     verb,
						Name:     name,
					})
				}
			}
		}
	}

	return attrs
}

func containsMatch(granted []string, requested string) bool {
	return matchWildcard(granted, requested)
}

func matchWildcard(granted []string, requested string) bool {
	for _, value := range granted {
		if value == "*" || value == requested {
			return true
		}
	}
	return false
}

func namesCovered(granted, requested []string) bool {
	if len(requested) == 0 {
		return len(granted) == 0
	}
	if len(granted) == 0 {
		return true
	}
	for _, name := range requested {
		if !containsExact(granted, name) {
			return false
		}
	}
	return true
}

func resourceNameAllowed(granted []string, requested string) bool {
	if len(granted) == 0 {
		return true
	}
	if requested == "" {
		// A request for all names is not covered by a name-restricted grant.
		return false
	}
	return containsExact(granted, requested)
}

func containsExact(values []string, requested string) bool {
	for _, value := range values {
		if value == requested {
			return true
		}
	}
	return false
}

func normalizeKind(kind string) string {
	if kind == "" {
		return KindRole
	}
	switch {
	case strings.EqualFold(kind, KindRole):
		return KindRole
	case strings.EqualFold(kind, KindClusterRole):
		return KindClusterRole
	default:
		return kind
	}
}
