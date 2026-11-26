package delegation

import (
	"crypto/ed25519"
	"fmt"
	"slices"
)

func validateUniverse(u *Universe) error {
	if u == nil {
		return fmt.Errorf("universe is nil")
	}
	if u.Tenant == "" || u.Action == "" || u.RootDelegationID == "" {
		return fmt.Errorf("tenant, action and root delegation id are required")
	}
	if len(u.PathAlphabet) == 0 || len(u.Labels) == 0 {
		return fmt.Errorf("path and label alphabets are required")
	}
	if len(u.Labels) > 16 {
		return fmt.Errorf("label alphabet has %d labels; maximum is 16", len(u.Labels))
	}
	if len(u.PathAlphabet) > 64 {
		return fmt.Errorf("path alphabet has %d symbols; maximum is 64", len(u.PathAlphabet))
	}
	if err := validateAlphabet("path alphabet", u.PathAlphabet); err != nil {
		return err
	}
	if err := validateAlphabet("label alphabet", u.Labels); err != nil {
		return err
	}
	labelSet := map[string]struct{}{}
	for _, label := range u.Labels {
		labelSet[label] = struct{}{}
	}
	if err := validateLabelRules(u.Labels, u.LabelRules, labelSet); err != nil {
		return err
	}
	if len(validTagSets(u.Labels, u.LabelRules)) == 0 {
		return fmt.Errorf("label constraints admit no tag set")
	}
	if len(u.Delegations) == 0 {
		return fmt.Errorf("no delegations were supplied")
	}
	seen := map[string]struct{}{}
	for _, signed := range u.Delegations {
		d := signed.Delegation
		if d.ID == "" {
			return fmt.Errorf("delegation id is empty")
		}
		if _, ok := seen[d.ID]; ok {
			return fmt.Errorf("duplicate delegation id %q", d.ID)
		}
		seen[d.ID] = struct{}{}
		if d.Tenant != u.Tenant || d.Action != u.Action {
			return fmt.Errorf("delegation %q is not bound to tenant %q and action %q", d.ID, u.Tenant, u.Action)
		}
		if err := validateDelegationRules(d.Rules, u.PathAlphabet, labelSet); err != nil {
			return fmt.Errorf("delegation %q: %w", d.ID, err)
		}
		key, ok := u.KeyRing[d.SignerKey]
		if !ok {
			return fmt.Errorf("delegation %q: signer %q is absent from the key ring", d.ID, d.SignerKey)
		}
		encoded, err := CanonicalDelegationJSON(d)
		if err != nil {
			return fmt.Errorf("delegation %q: canonicalize signature payload: %w", d.ID, err)
		}
		if len(key) == 0 || !ed25519.Verify(key, encoded, signed.Signature) {
			return fmt.Errorf("delegation %q: invalid signature", d.ID)
		}
	}
	if _, ok := seen[u.RootDelegationID]; !ok {
		return fmt.Errorf("root delegation %q is absent", u.RootDelegationID)
	}
	for _, signed := range u.Delegations {
		d := signed.Delegation
		for _, parent := range d.Parents {
			if _, ok := seen[parent.DelegationID]; !ok {
				return fmt.Errorf("delegation %q references missing parent %q", d.ID, parent.DelegationID)
			}
		}
	}
	return nil
}

func validateAlphabet(name string, values []string) error {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value == "" {
			return fmt.Errorf("%s contains an empty symbol", name)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s contains duplicate symbol %q", name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateLabelRules(labels []string, rules LabelConstraints, labelSet map[string]struct{}) error {
	if !slices.IsSorted(labels) {
		return fmt.Errorf("label alphabet must be sorted")
	}
	for _, pair := range rules.MutexPairs {
		if pair[0] == pair[1] {
			return fmt.Errorf("mutex pair cannot contain the same label %q", pair[0])
		}
		if _, ok := labelSet[pair[0]]; !ok {
			return fmt.Errorf("mutex label %q is outside the label alphabet", pair[0])
		}
		if _, ok := labelSet[pair[1]]; !ok {
			return fmt.Errorf("mutex label %q is outside the label alphabet", pair[1])
		}
	}
	for _, label := range rules.Required {
		if _, ok := labelSet[label]; !ok {
			return fmt.Errorf("required label %q is outside the label alphabet", label)
		}
	}
	for _, implication := range rules.Implications {
		if _, ok := labelSet[implication.IfLabel]; !ok {
			return fmt.Errorf("implication premise %q is outside the label alphabet", implication.IfLabel)
		}
		if _, ok := labelSet[implication.ThenLabel]; !ok {
			return fmt.Errorf("implication conclusion %q is outside the label alphabet", implication.ThenLabel)
		}
	}
	return nil
}

func validateDelegationRules(rules []Rule, alphabet []string, labelSet map[string]struct{}) error {
	if len(rules) == 0 {
		return fmt.Errorf("at least one rule is required")
	}
	alphabetSet := map[string]struct{}{}
	for _, symbol := range alphabet {
		alphabetSet[symbol] = struct{}{}
	}
	for i, rule := range rules {
		if rule.Effect != EffectAllow && rule.Effect != EffectDeny {
			return fmt.Errorf("rule %d has invalid effect %q", i, rule.Effect)
		}
		if rule.Start >= rule.End {
			return fmt.Errorf("rule %d has an empty or inverted interval", i)
		}
		tokens, err := patternTokens(rule.Pattern)
		if err != nil {
			return fmt.Errorf("rule %d: %w", i, err)
		}
		for _, token := range tokens {
			if token == "*" || token == "**" {
				continue
			}
			if _, ok := alphabetSet[token]; !ok {
				return fmt.Errorf("rule %d pattern token %q is outside the path alphabet", i, token)
			}
		}
		if err := validatePredicate(rule.Predicate, labelSet); err != nil {
			return fmt.Errorf("rule %d: %w", i, err)
		}
	}
	return nil
}

func validatePredicate(predicate TagPredicate, labelSet map[string]struct{}) error {
	if !slices.IsSorted(predicate.RequireAll) || !slices.IsSorted(predicate.ForbidAll) {
		return fmt.Errorf("tag predicate labels must be sorted")
	}
	for _, label := range append(append([]string(nil), predicate.RequireAll...), predicate.ForbidAll...) {
		if _, ok := labelSet[label]; !ok {
			return fmt.Errorf("predicate label %q is outside the label alphabet", label)
		}
	}
	if intersection(predicate.RequireAll, predicate.ForbidAll) {
		return fmt.Errorf("predicate requires and forbids the same label")
	}
	return nil
}

func intersection(a, b []string) bool {
	for _, value := range a {
		if slices.Contains(b, value) {
			return true
		}
	}
	return false
}
