package netsec

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	// ErrDenied is the common ancestor for all binding-authorization failures.
	ErrDenied = errors.New("binding denied")

	// ErrTenant means that no ACL entry exists for the subscription tenant.
	ErrTenant = errors.New("tenant not authorized")

	// ErrExchange means that the tenant has no ACL entry for the exchange.
	ErrExchange = errors.New("exchange not authorized")

	// ErrPattern means that the requested binding is broader than every
	// allowed pattern for the tenant and exchange.
	ErrPattern = errors.New("binding pattern not authorized")
)

// Rule grants a tenant access to binding patterns on one exchange. A
// subscription is allowed only when its binding pattern is a subset of at
// least one allowed pattern.
//
// Patterns is the canonical field. Bindings is accepted as an alias; when both
// are set their entries are combined.
type Rule struct {
	Tenant   string
	Exchange string
	Patterns []string
	Bindings []string
}

// BindingRule is an alias for Rule.
type BindingRule = Rule

type compiledRule struct {
	tenant   string
	exchange string
	patterns []Pattern
}

type aclSnapshot struct {
	generation uint64
	rules      []Rule
	compiled   []compiledRule
}

// Authorizer is a concurrency-safe binding authorizer. ACL changes publish a
// new immutable snapshot with a monotonically increasing generation. Existing
// subscriptions are revalidated against every new snapshot and again whenever
// an event is routed or received.
type Authorizer struct {
	mu    sync.Mutex
	state atomic.Pointer[aclSnapshot]
	subs  map[*Subscription]struct{}
}

// NewAuthorizer returns an authorizer with an empty, deny-all ACL.
func NewAuthorizer() *Authorizer {
	authorizer := &Authorizer{subs: make(map[*Subscription]struct{})}
	authorizer.state.Store(&aclSnapshot{})
	return authorizer
}

// NewAuthorizerWithRules returns an authorizer initialized with rules.
func NewAuthorizerWithRules(rules ...Rule) (*Authorizer, error) {
	authorizer := NewAuthorizer()
	if _, err := authorizer.SetRules(rules); err != nil {
		return nil, err
	}
	return authorizer, nil
}

// Generation returns the current ACL generation.
func (a *Authorizer) Generation() uint64 {
	return a.state.Load().generation
}

// SetRules atomically replaces the ACL and installs its next generation. If
// the rules are invalid, the previous ACL and generation are left untouched.
func (a *Authorizer) SetRules(rules []Rule) (uint64, error) {
	compiled, err := compileRules(rules)
	if err != nil {
		return a.Generation(), err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	return a.installLocked(rules, compiled), nil
}

// ReplaceRules is an alias for SetRules.
func (a *Authorizer) ReplaceRules(rules []Rule) (uint64, error) {
	return a.SetRules(rules)
}

// Update is an alias for SetRules.
func (a *Authorizer) Update(rules []Rule) (uint64, error) {
	return a.SetRules(rules)
}

// Grant adds binding patterns to a tenant's ACL on an exchange. Existing
// patterns are retained.
func (a *Authorizer) Grant(tenant, exchange string, patterns ...string) (uint64, error) {
	if tenant == "" {
		return a.Generation(), deny(ErrTenant, "empty tenant")
	}
	if exchange == "" {
		return a.Generation(), deny(ErrExchange, "empty exchange")
	}
	if len(patterns) == 0 {
		return a.Generation(), fmt.Errorf("%w: no patterns supplied", ErrInvalidPattern)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	current := a.state.Load()
	rules := cloneRules(current.rules)
	found := false
	for i := range rules {
		if rules[i].Tenant != tenant || rules[i].Exchange != exchange {
			continue
		}
		found = true
		rules[i].Patterns = appendUnique(rules[i].Patterns, patterns...)
	}
	if !found {
		rules = append(rules, Rule{
			Tenant:   tenant,
			Exchange: exchange,
			Patterns: append([]string(nil), patterns...),
		})
	}

	compiled, err := compileRules(rules)
	if err != nil {
		return current.generation, err
	}
	return a.installLocked(rules, compiled), nil
}

// Revoke removes the ACL entry for a tenant and exchange. The returned boolean
// reports whether anything changed.
func (a *Authorizer) Revoke(tenant, exchange string) (uint64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	current := a.state.Load()
	rules := make([]Rule, 0, len(current.rules))
	changed := false
	for _, rule := range current.rules {
		if rule.Tenant == tenant && rule.Exchange == exchange {
			changed = true
			continue
		}
		rules = append(rules, cloneRule(rule))
	}
	if !changed {
		return current.generation, false
	}

	compiled, err := compileRules(rules)
	if err != nil {
		return current.generation, false
	}
	return a.installLocked(rules, compiled), true
}

// RevokePattern removes one exact pattern from a tenant and exchange entry.
// The returned boolean reports whether anything changed.
func (a *Authorizer) RevokePattern(tenant, exchange, pattern string) (uint64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	current := a.state.Load()
	rules := cloneRules(current.rules)
	changed := false
	keptRules := rules[:0]
	for i := range rules {
		if rules[i].Tenant != tenant || rules[i].Exchange != exchange {
			keptRules = append(keptRules, rules[i])
			continue
		}

		keptPatterns := rules[i].Patterns[:0]
		for _, existing := range rules[i].Patterns {
			if existing == pattern {
				changed = true
				continue
			}
			keptPatterns = append(keptPatterns, existing)
		}
		rules[i].Patterns = keptPatterns

		keptBindings := rules[i].Bindings[:0]
		for _, existing := range rules[i].Bindings {
			if existing == pattern {
				changed = true
				continue
			}
			keptBindings = append(keptBindings, existing)
		}
		rules[i].Bindings = keptBindings

		if len(rules[i].Patterns) != 0 || len(rules[i].Bindings) != 0 {
			keptRules = append(keptRules, rules[i])
		}
	}
	if !changed {
		return current.generation, false
	}

	compiled, err := compileRules(keptRules)
	if err != nil {
		return current.generation, false
	}
	return a.installLocked(keptRules, compiled), true
}

// AuthorizeBinding checks a tenant, exchange, and requested binding pattern
// against the current ACL. It returns the generation that authorized the
// binding. On failure it returns a typed error that matches ErrDenied.
func (a *Authorizer) AuthorizeBinding(tenant, exchange, binding string) (uint64, error) {
	pattern, err := ParsePattern(binding)
	if err != nil {
		return a.Generation(), deny(ErrInvalidPattern, "%s", err.Error())
	}

	snapshot := a.state.Load()
	if err := snapshot.authorize(tenant, exchange, pattern); err != nil {
		return snapshot.generation, err
	}
	return snapshot.generation, nil
}

// Authorize is an alias for AuthorizeBinding.
func (a *Authorizer) Authorize(tenant, exchange, binding string) (uint64, error) {
	return a.AuthorizeBinding(tenant, exchange, binding)
}

// CheckBinding checks a binding and returns only the authorization error.
func (a *Authorizer) CheckBinding(tenant, exchange, binding string) error {
	_, err := a.AuthorizeBinding(tenant, exchange, binding)
	return err
}

// BindingAllowed reports whether a binding is currently authorized.
func (a *Authorizer) BindingAllowed(tenant, exchange, binding string) bool {
	return a.CheckBinding(tenant, exchange, binding) == nil
}

// Revalidate rechecks an existing subscription against the current ACL and
// updates its observed generation when it remains authorized.
func (a *Authorizer) Revalidate(subscription *Subscription) (uint64, error) {
	if subscription == nil {
		return a.Generation(), deny(ErrDenied, "nil subscription")
	}

	snapshot := a.state.Load()
	if err := snapshot.authorize(subscription.tenant, subscription.exchange, subscription.pattern); err != nil {
		subscription.revoke()
		a.removeSubscription(subscription)
		return snapshot.generation, err
	}
	subscription.setGeneration(snapshot.generation)
	return snapshot.generation, nil
}

func (a *Authorizer) installLocked(rules []Rule, compiled []compiledRule) uint64 {
	current := a.state.Load()
	next := &aclSnapshot{
		generation: current.generation + 1,
		rules:      cloneRules(rules),
		compiled:   compiled,
	}
	a.state.Store(next)
	a.revalidateLocked(next)
	return next.generation
}

func (a *Authorizer) revalidateLocked(snapshot *aclSnapshot) {
	for subscription := range a.subs {
		if err := snapshot.authorize(subscription.tenant, subscription.exchange, subscription.pattern); err != nil {
			subscription.revoke()
			delete(a.subs, subscription)
			continue
		}
		subscription.setGeneration(snapshot.generation)
	}
}

func (a *Authorizer) removeSubscription(subscription *Subscription) {
	if subscription == nil {
		return
	}
	a.mu.Lock()
	delete(a.subs, subscription)
	a.mu.Unlock()
}

func (s *aclSnapshot) authorize(tenant, exchange string, requested Pattern) error {
	if tenant == "" {
		return deny(ErrTenant, "empty tenant")
	}
	if exchange == "" {
		return deny(ErrExchange, "empty exchange")
	}

	tenantKnown := false
	exchangeKnown := false
	for _, rule := range s.compiled {
		if rule.tenant != tenant {
			continue
		}
		tenantKnown = true
		if rule.exchange != exchange {
			continue
		}
		exchangeKnown = true
		for _, allowed := range rule.patterns {
			if allowed.Covers(requested) {
				return nil
			}
		}
	}

	switch {
	case !tenantKnown:
		return deny(ErrTenant, "tenant %q has no ACL entry", tenant)
	case !exchangeKnown:
		return deny(ErrExchange, "exchange %q is not allowed for tenant %q", exchange, tenant)
	default:
		return deny(ErrPattern, "binding %q is broader than the allowed patterns", requested.String())
	}
}

func compileRules(rules []Rule) ([]compiledRule, error) {
	if len(rules) == 0 {
		return nil, nil
	}

	compiled := make([]compiledRule, 0, len(rules))
	index := make(map[string]int)
	for _, rule := range rules {
		if rule.Tenant == "" {
			return nil, fmt.Errorf("%w: rule has an empty tenant", ErrTenant)
		}
		if rule.Exchange == "" {
			return nil, fmt.Errorf("%w: rule for tenant %q has an empty exchange", ErrExchange, rule.Tenant)
		}

		key := rule.Tenant + "\x00" + rule.Exchange
		position, ok := index[key]
		if !ok {
			position = len(compiled)
			index[key] = position
			compiled = append(compiled, compiledRule{tenant: rule.Tenant, exchange: rule.Exchange})
		}

		for _, binding := range rulePatterns(rule) {
			pattern, err := ParsePattern(binding)
			if err != nil {
				return nil, fmt.Errorf("tenant %q exchange %q: %w", rule.Tenant, rule.Exchange, err)
			}
			if !hasPattern(compiled[position].patterns, pattern) {
				compiled[position].patterns = append(compiled[position].patterns, pattern)
			}
		}
	}
	return compiled, nil
}

func rulePatterns(rule Rule) []string {
	patterns := make([]string, 0, len(rule.Patterns)+len(rule.Bindings))
	patterns = append(patterns, rule.Patterns...)
	patterns = append(patterns, rule.Bindings...)
	return patterns
}

func hasPattern(patterns []Pattern, candidate Pattern) bool {
	for _, pattern := range patterns {
		if pattern.String() == candidate.String() {
			return true
		}
	}
	return false
}

func appendUnique(existing []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(existing)+len(additions))
	for _, value := range existing {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		existing = append(existing, value)
	}
	return existing
}

func cloneRules(rules []Rule) []Rule {
	cloned := make([]Rule, len(rules))
	for i, rule := range rules {
		cloned[i] = cloneRule(rule)
	}
	return cloned
}

func cloneRule(rule Rule) Rule {
	return Rule{
		Tenant:   rule.Tenant,
		Exchange: rule.Exchange,
		Patterns: append([]string(nil), rule.Patterns...),
		Bindings: append([]string(nil), rule.Bindings...),
	}
}

func deny(scope error, format string, args ...any) error {
	detail := fmt.Sprintf(format, args...)
	return fmt.Errorf("%w: %w: %s", ErrDenied, scope, detail)
}
