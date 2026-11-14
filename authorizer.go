package netsec

import (
	"fmt"
	"strings"
)

// Wildcard is the only wildcard token understood by this package. It must be
// written explicitly in a rule; an omitted or empty field is never widened.
const Wildcard = "*"

const (
	// APIGroupCore is the API group used by core Kubernetes resources.
	APIGroupCore = ""

	// ResourcePods is the core pods resource.
	ResourcePods = "pods"

	// Subresources are kept as distinct values. A rule for pods itself must
	// never be treated as a rule for pods/exec or one of its sibling
	// subresources.
	SubresourceExec        = "exec"
	SubresourceAttach      = "attach"
	SubresourcePortForward = "portforward"
	SubresourceLog         = "log"

	// Common verbs.
	VerbGet              = "get"
	VerbList             = "list"
	VerbWatch            = "watch"
	VerbCreate           = "create"
	VerbUpdate           = "update"
	VerbPatch            = "patch"
	VerbDelete           = "delete"
	VerbDeleteCollection = "deletecollection"
)

// Compatibility aliases for callers that prefer the noun first.
const (
	ExecSubresource        = SubresourceExec
	AttachSubresource      = SubresourceAttach
	PortForwardSubresource = SubresourcePortForward
	LogSubresource         = SubresourceLog
	ResourcePod            = ResourcePods
)

// Rule is one authorization rule. Every dimension is matched exactly unless
// that dimension contains an explicit Wildcard.
//
// ResourceName is the name of the concrete resource. An empty ResourceName
// only matches a request whose ResourceName is also empty (for example a
// collection-level request). Use Wildcard explicitly to cover all names.
//
// Resource may contain a single "/" to express a subresource, such as
// "pods/exec". The combined form is equivalent to Resource "pods" and
// Subresource "exec". If both Resource contains "/" and Subresource is set,
// the rule is invalid rather than being interpreted ambiguously.
type Rule struct {
	APIGroup     string
	Namespace    string
	Resource     string
	Subresource  string
	Verb         string
	ResourceName string
}

// Request describes the access being authorized. It uses the same dimensions
// as Rule so that callers can compare policy rules and requests directly.
type Request struct {
	APIGroup     string
	Namespace    string
	Resource     string
	Subresource  string
	Verb         string
	ResourceName string
}

// Attributes is a compatibility alias for Request.
type Attributes = Request

// AccessRequest is a compatibility alias for Request.
type AccessRequest = Request

// Decision is the result of a detailed authorization check.
type Decision struct {
	Allowed     bool
	MatchedRule int // index of the first matching rule, or -1
}

// Authorizer evaluates a fixed set of rules. It is immutable after
// construction and is safe for concurrent use.
type Authorizer struct {
	rules []Rule
}

// Policy and RuleSet are compatibility aliases for Authorizer.
type Policy = Authorizer
type RuleSet = Authorizer

// New creates an authorizer from rules. The rules are copied so later changes
// to the caller's slice cannot change the policy. Invalid rules are retained
// for inspection by Rules but never match a request.
func New(rules []Rule) *Authorizer {
	copied := append([]Rule(nil), rules...)
	return &Authorizer{rules: copied}
}

// NewAuthorizer is an alias for New.
func NewAuthorizer(rules []Rule) *Authorizer {
	return New(rules)
}

// NewPolicy is an alias for New.
func NewPolicy(rules []Rule) *Authorizer {
	return New(rules)
}

// NewRuleSet accepts individual rules and is useful in small tests.
func NewRuleSet(rules ...Rule) *Authorizer {
	return New(rules)
}

// Compile validates rules and returns an authorizer. It is useful when policy
// is loaded from configuration and invalid rules should be reported rather
// than silently ignored.
func Compile(rules []Rule) (*Authorizer, error) {
	if err := ValidateRules(rules); err != nil {
		return nil, err
	}
	return New(rules), nil
}

// Allow evaluates a request. A nil authorizer denies.
func (a *Authorizer) Allow(req Request) bool {
	return a.Authorize(req)
}

// Allowed evaluates a request. A nil authorizer denies.
func (a *Authorizer) Allowed(req Request) bool {
	return a.Authorize(req)
}

// IsAllowed evaluates a request. A nil authorizer denies.
func (a *Authorizer) IsAllowed(req Request) bool {
	return a.Authorize(req)
}

// Authorize evaluates a request against all rules. Matching is exact in every
// dimension except for an explicit Wildcard in a rule. The first matching rule
// allows the request; if no rule matches, the request is denied.
func (a *Authorizer) Authorize(req Request) bool {
	return a.Check(req).Allowed
}

// Check evaluates a request and reports the matching rule index.
func (a *Authorizer) Check(req Request) Decision {
	if a == nil {
		return Decision{Allowed: false, MatchedRule: -1}
	}
	for i, rule := range a.rules {
		if rule.Matches(req) {
			return Decision{Allowed: true, MatchedRule: i}
		}
	}
	return Decision{Allowed: false, MatchedRule: -1}
}

// Rules returns a copy of the configured rules.
func (a *Authorizer) Rules() []Rule {
	if a == nil {
		return nil
	}
	return append([]Rule(nil), a.rules...)
}

// Authorize is a package-level convenience function for one-off checks.
func Authorize(rules []Rule, req Request) bool {
	return New(rules).Authorize(req)
}

// Allowed is a package-level convenience function for one-off checks.
func Allowed(rules []Rule, req Request) bool {
	return Authorize(rules, req)
}

// Matches reports whether a request is covered by one rule. Invalid rules do
// not match anything.
func (r Rule) Matches(req Request) bool {
	if err := r.Validate(); err != nil {
		return false
	}
	if err := req.Validate(); err != nil {
		return false
	}

	ruleResource, ruleSubresource := splitResource(r.Resource, r.Subresource)
	reqResource, reqSubresource := splitResource(req.Resource, req.Subresource)

	if !matchExactOrWildcard(r.APIGroup, req.APIGroup) {
		return false
	}
	if !matchExactOrWildcard(r.Namespace, req.Namespace) {
		return false
	}
	if !matchExactOrWildcard(ruleResource, reqResource) {
		return false
	}
	if !matchSubresource(ruleSubresource, reqSubresource) {
		return false
	}
	if !matchExactOrWildcard(r.Verb, req.Verb) {
		return false
	}
	if !matchExactOrWildcard(r.ResourceName, req.ResourceName) {
		return false
	}
	return true
}

// Matches is a package-level rule/request matcher.
func Matches(rule Rule, req Request) bool {
	return rule.Matches(req)
}

// Validate checks a rule for ambiguity. A rule with an empty Resource or Verb
// is invalid; empty Namespace, APIGroup and ResourceName remain valid because
// they have exact, non-wildcard meanings.
func (r Rule) Validate() error {
	if r.Resource == "" {
		return fmt.Errorf("netsec: rule resource must not be empty")
	}
	if r.Verb == "" {
		return fmt.Errorf("netsec: rule verb must not be empty")
	}
	if strings.Contains(r.Resource, "/") && r.Subresource != "" {
		return fmt.Errorf("netsec: rule must not combine a resource path with an explicit subresource")
	}
	if strings.Count(r.Resource, "/") > 1 {
		return fmt.Errorf("netsec: rule resource may contain at most one subresource separator")
	}
	if strings.Contains(r.Subresource, "/") {
		return fmt.Errorf("netsec: rule subresource must not contain '/'")
	}
	return nil
}

// Validate checks a request for malformed resource/subresource combinations.
func (r Request) Validate() error {
	if r.Resource == "" {
		return fmt.Errorf("netsec: request resource must not be empty")
	}
	if r.Verb == "" {
		return fmt.Errorf("netsec: request verb must not be empty")
	}
	if strings.Contains(r.Resource, "/") && r.Subresource != "" {
		return fmt.Errorf("netsec: request must not combine a resource path with an explicit subresource")
	}
	if strings.Count(r.Resource, "/") > 1 {
		return fmt.Errorf("netsec: request resource may contain at most one subresource separator")
	}
	if strings.Contains(r.Subresource, "/") {
		return fmt.Errorf("netsec: request subresource must not contain '/'")
	}
	return nil
}

// ValidateRules validates every rule and identifies the offending index.
func ValidateRules(rules []Rule) error {
	for i, rule := range rules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("netsec: rule %d: %w", i, err)
		}
	}
	return nil
}

func splitResource(resource, subresource string) (string, string) {
	if subresource != "" {
		return resource, subresource
	}
	if i := strings.IndexByte(resource, '/'); i >= 0 {
		return resource[:i], resource[i+1:]
	}
	return resource, ""
}

func matchExactOrWildcard(ruleValue, requestValue string) bool {
	return ruleValue == Wildcard || ruleValue == requestValue
}

func matchSubresource(ruleSubresource, requestSubresource string) bool {
	// A wildcard for subresources covers concrete subresources only. It does
	// not silently grant the base resource; the base resource has its own
	// empty-subresource rule dimension.
	if ruleSubresource == Wildcard {
		return requestSubresource != ""
	}
	return ruleSubresource == requestSubresource
}
