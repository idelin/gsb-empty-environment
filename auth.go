package changeaccept

import (
	"fmt"
	"sort"
)

// EndToEndRule authorizes a pair of endpoint labels. Rules are ordered and the
// first matching source/destination pair decides. The default is deny.
type EndToEndRule struct {
	Source      Predicate
	Destination Predicate
	Action      Action
}

func (r EndToEndRule) matches(source, destination Label) bool {
	return r.Source.Matches(source) && r.Destination.Matches(destination)
}

// PairPolicy is an ordered end-to-end authorization policy.
type PairPolicy []EndToEndRule

// Decide returns the first matching end-to-end decision.
func (p PairPolicy) Decide(source, destination Label) Decision {
	for index, rule := range p {
		if rule.matches(source, destination) {
			if !rule.Action.valid() {
				return Decision{Action: ActionDeny, Rule: -1}
			}
			return Decision{Action: rule.Action, Rule: index}
		}
	}
	return Decision{Action: ActionDeny, Rule: -1}
}

func (p PairPolicy) validate(field string) error {
	for index, rule := range p {
		if !rule.Action.valid() {
			return fmt.Errorf("%s[%d]: invalid action %d", field, index, rule.Action)
		}
	}
	return nil
}

// Authorizer is the closed-world model of managed tenant pairs.
type Authorizer struct {
	managed map[TenantID]struct{}
	pairs   map[TenantPair]PairPolicy
}

// NewAuthorizer builds an authorization model. Every tenant appearing in a
// pair must be managed. Pairs absent from rules are denied.
func NewAuthorizer(managed []TenantID, pairs map[TenantPair]PairPolicy) (*Authorizer, error) {
	if len(managed) == 0 {
		return nil, fmt.Errorf("authorizer requires at least one managed tenant")
	}
	result := &Authorizer{
		managed: make(map[TenantID]struct{}, len(managed)),
		pairs:   make(map[TenantPair]PairPolicy, len(pairs)),
	}
	for _, tenant := range managed {
		if tenant == "" {
			return nil, fmt.Errorf("managed tenant ID must not be empty")
		}
		result.managed[tenant] = struct{}{}
	}
	for pair, policy := range pairs {
		if _, ok := result.managed[pair.Source]; !ok {
			return nil, fmt.Errorf("pair %q -> %q uses unmanaged source tenant", pair.Source, pair.Destination)
		}
		if _, ok := result.managed[pair.Destination]; !ok {
			return nil, fmt.Errorf("pair %q -> %q uses unmanaged destination tenant", pair.Source, pair.Destination)
		}
		if err := policy.validate(fmt.Sprintf("pair %q -> %q", pair.Source, pair.Destination)); err != nil {
			return nil, err
		}
		result.pairs[pair] = append(PairPolicy(nil), policy...)
	}
	return result, nil
}

// IsManaged reports whether tenant belongs to the closed authorization model.
func (a *Authorizer) IsManaged(tenant TenantID) bool {
	if a == nil {
		return false
	}
	_, ok := a.managed[tenant]
	return ok
}

// Decide evaluates the end-to-end relation for a concrete tenant/label pair.
func (a *Authorizer) Decide(pair TenantPair, source, destination Label) Decision {
	if a == nil || !a.IsManaged(pair.Source) || !a.IsManaged(pair.Destination) {
		return Decision{Action: ActionDeny, Rule: -1}
	}
	policy, ok := a.pairs[pair]
	if !ok {
		return Decision{Action: ActionDeny, Rule: -1}
	}
	return policy.Decide(source, destination)
}

// PairPolicy returns a copy of the ordered policy for pair.
func (a *Authorizer) PairPolicy(pair TenantPair) PairPolicy {
	if a == nil {
		return nil
	}
	return append(PairPolicy(nil), a.pairs[pair]...)
}

// Pairs returns all explicitly authorized tenant pairs in deterministic order.
func (a *Authorizer) Pairs() []TenantPair {
	if a == nil {
		return nil
	}
	result := make([]TenantPair, 0, len(a.pairs))
	for pair := range a.pairs {
		result = append(result, pair)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].Destination < result[j].Destination
	})
	return result
}

// ManagedTenants returns the closed set in deterministic order.
func (a *Authorizer) ManagedTenants() []TenantID {
	return a.managedTenants()
}

// AllManagedPairs returns every ordered pair of distinct managed tenants.
// Default-deny verification uses this set so a reachable inter-tenant pair with
// no explicit rule cannot escape inspection.
func (a *Authorizer) AllManagedPairs() []TenantPair {
	tenants := a.managedTenants()
	result := make([]TenantPair, 0, len(tenants)*len(tenants))
	for _, source := range tenants {
		for _, destination := range tenants {
			if source == destination {
				continue
			}
			result = append(result, TenantPair{Source: source, Destination: destination})
		}
	}
	return result
}

func (a *Authorizer) managedTenants() []TenantID {
	if a == nil {
		return nil
	}
	result := make([]TenantID, 0, len(a.managed))
	for tenant := range a.managed {
		result = append(result, tenant)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
