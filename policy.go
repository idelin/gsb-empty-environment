package netsec

import (
	"errors"
	"fmt"
)

// ErrUnauthorized is wrapped by AuthorizationError when a valid expression is
// denied by the policy.
var ErrUnauthorized = errors.New("netsec: unauthorized")

// Effect identifies whether a rule grants or removes access.
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// Rule is one allow or deny key expression.
type Rule struct {
	Effect     Effect
	Expression string
}

// AuthorizationError explains why a key expression was denied.
type AuthorizationError struct {
	Action    string
	Reason    string
	RuleIndex int
	Rule      string
}

func (e *AuthorizationError) Error() string {
	if e.RuleIndex >= 0 {
		return fmt.Sprintf("%s: %s: rule %q at index %d", ErrUnauthorized, e.Action, e.Rule, e.RuleIndex)
	}
	return fmt.Sprintf("%s: %s: %s", ErrUnauthorized, e.Action, e.Reason)
}

// Unwrap lets callers use errors.Is(err, ErrUnauthorized).
func (e *AuthorizationError) Unwrap() error {
	return ErrUnauthorized
}

// Authorizer evaluates subscriptions and publishes against allow and deny key
// expressions. Access is denied by default.
type Authorizer struct {
	allow      []compiledRule
	deny       []compiledRule
	denySource []string
}

// Policy is an alias for Authorizer.
type Policy = Authorizer

type compiledRule struct {
	expression *expression
}

// NewAuthorizer compiles allow and deny rules. Invalid expressions fail
// construction rather than being ignored.
func NewAuthorizer(allow, deny []string) (*Authorizer, error) {
	authorizer := &Authorizer{
		denySource: append([]string(nil), deny...),
	}

	for i, raw := range allow {
		ex, err := parseExpression(raw)
		if err != nil {
			return nil, fmt.Errorf("allow rule %d: %w", i, err)
		}
		authorizer.allow = append(authorizer.allow, compiledRule{expression: ex})
	}
	for i, raw := range deny {
		ex, err := parseExpression(raw)
		if err != nil {
			return nil, fmt.Errorf("deny rule %d: %w", i, err)
		}
		authorizer.deny = append(authorizer.deny, compiledRule{expression: ex})
	}

	return authorizer, nil
}

// New is a shorthand for NewAuthorizer.
func New(allow, deny []string) (*Authorizer, error) {
	return NewAuthorizer(allow, deny)
}

// NewPolicy is a shorthand for NewAuthorizer.
func NewPolicy(allow, deny []string) (*Policy, error) {
	return NewAuthorizer(allow, deny)
}

// MustNewAuthorizer is like NewAuthorizer but panics on invalid rules.
func MustNewAuthorizer(allow, deny []string) *Authorizer {
	authorizer, err := NewAuthorizer(allow, deny)
	if err != nil {
		panic(err)
	}
	return authorizer
}

// NewFromRules compiles a mixed list of allow and deny rules.
func NewFromRules(rules []Rule) (*Authorizer, error) {
	var allow []string
	var deny []string
	for i, rule := range rules {
		switch rule.Effect {
		case EffectAllow:
			allow = append(allow, rule.Expression)
		case EffectDeny:
			deny = append(deny, rule.Expression)
		default:
			return nil, fmt.Errorf("rule %d has invalid effect %q", i, rule.Effect)
		}
	}
	return NewAuthorizer(allow, deny)
}

// AuthorizeSubscription reports whether expr is fully contained by at least
// one allow rule and disjoint from every deny rule.
//
// Invalid expressions are denied.
func (a *Authorizer) AuthorizeSubscription(expr string) bool {
	return a.CheckSubscription(expr) == nil
}

// AuthorizePublish reports whether key is a concrete key permitted by the
// same allow/deny policy. Invalid or wildcard keys are denied.
func (a *Authorizer) AuthorizePublish(key string) bool {
	return a.CheckPublish(key) == nil
}

// AllowsSubscription is an alias for AuthorizeSubscription.
func (a *Authorizer) AllowsSubscription(expr string) bool {
	return a.AuthorizeSubscription(expr)
}

// AllowsPublish is an alias for AuthorizePublish.
func (a *Authorizer) AllowsPublish(key string) bool {
	return a.AuthorizePublish(key)
}

// CheckSubscription returns nil only when the subscription is authorized.
func (a *Authorizer) CheckSubscription(expr string) error {
	candidate, err := parseExpression(expr)
	if err != nil {
		return err
	}
	return a.check("subscription", candidate, expr)
}

// CheckPublish returns nil only when the concrete key is authorized.
func (a *Authorizer) CheckPublish(key string) error {
	candidate, err := parseConcreteKey(key)
	if err != nil {
		return err
	}
	return a.check("publish", candidate, key)
}

func (a *Authorizer) check(action string, candidate *expression, raw string) error {
	covered := false
	for _, rule := range a.allow {
		if expressionContains(rule.expression, candidate) {
			covered = true
			break
		}
	}
	if !covered {
		return &AuthorizationError{
			Action:    action,
			Reason:    fmt.Sprintf("%q is not contained by any allow rule", raw),
			RuleIndex: -1,
		}
	}

	for i, rule := range a.deny {
		if expressionsIntersect(candidate, rule.expression) {
			return &AuthorizationError{
				Action:    action,
				Reason:    fmt.Sprintf("%q intersects a deny rule", raw),
				RuleIndex: i,
				Rule:      a.denySource[i],
			}
		}
	}
	return nil
}
