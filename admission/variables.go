package admission

import (
	"fmt"
	"sort"
)

func (a *Analyzer) buildVariables() error {
	m := a.model
	participant := map[Identity]*Participant{}
	for i := range m.Participants {
		participant[m.Participants[i].Subject] = &m.Participants[i]
	}

	// First pass: one variable per (subject, public observation). Nodes that
	// differ only in hidden state land in the same key and must agree.
	byKey := map[decisionKey]*conditionVar{}
	var keyOrder []decisionKey
	for _, id := range a.order {
		n := a.nodes[id]
		if n.Kind != KindDecision {
			continue
		}
		d := n.Decision
		key := decisionKey{subject: d.Subject, obs: observationKey(d.ObservedMessages)}
		v := byKey[key]
		if v == nil {
			v = &conditionVar{
				domain:   participant[d.Subject].FailureDomain,
				subjects: []Identity{d.Subject},
				obs:      append([]MessageType(nil), d.ObservedMessages...),
				actions:  append([]string(nil), d.Actions...),
				abstain:  d.AbstainAction,
				key:      key,
			}
			byKey[key] = v
			keyOrder = append(keyOrder, key)
		} else if !sameStrings(v.actions, d.Actions) || v.abstain != d.AbstainAction {
			if !sameStrings(v.actions, d.Actions) || v.abstain != d.AbstainAction {
				return fmt.Errorf(
					"admission: subject %q sees the same public prefix at node %q but declares different actions; hidden-state branching is forbidden",
					d.Subject, n.ID)
			}
		}
		a.nodeVar[n.ID] = -1 // resolved after merging
	}

	// Second pass: failure domains couple variables with the same observation.
	domainGroups := map[string][]*conditionVar{}
	var independent []*conditionVar
	for _, key := range keyOrder {
		v := byKey[key]
		if v.domain == "" {
			independent = append(independent, v)
			continue
		}
		domainGroups[v.domain] = append(domainGroups[v.domain], v)
	}

	merged := make([]*conditionVar, 0, len(byKey))
	merged = append(merged, independent...)
	domains := make([]string, 0, len(domainGroups))
	for domain := range domainGroups {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	for _, domain := range domains {
		group := domainGroups[domain]
		// Keyed by public observation: all subjects of the domain facing the
		// same prefix share one variable, even when they entered via distinct
		// (subject, observation) keys.
		byObs := map[string]*conditionVar{}
		for _, v := range group {
			g := byObs[v.key.obs]
			if g == nil {
				byObs[v.key.obs] = v
				merged = append(merged, v)
				continue
			}
			if !sameStrings(g.actions, v.actions) || g.abstain != v.abstain {
				return fmt.Errorf("admission: failure domain %q couples incompatible action sets at one observation", domain)
			}
			g.subjects = append(g.subjects, v.subjects...)
		}
	}

	// Canonical order: independent subjects first by identity, then domains.
	sort.Slice(merged, func(i, j int) bool {
		x, y := merged[i], merged[j]
		if x.domain != y.domain {
			return x.domain < y.domain
		}
		if x.key.subject != y.key.subject {
			return x.key.subject < y.key.subject
		}
		return x.key.obs < y.key.obs
	})
	if len(merged) > MaxDecisionPoints {
		return fmt.Errorf("admission: %d public decision points exceed cap %d", len(merged), MaxDecisionPoints)
	}

	if err := a.applyHostConditions(merged, participant); err != nil {
		return err
	}

	for i, v := range merged {
		v.id = fmt.Sprintf("c%02d", i)
		a.vars = append(a.vars, v)
	}
	// Resolve every decision node to its final variable index.
	for _, id := range a.order {
		n := a.nodes[id]
		if n.Kind != KindDecision {
			continue
		}
		d := n.Decision
		domain := participant[d.Subject].FailureDomain
		obs := observationKey(d.ObservedMessages)
		for i, v := range a.vars {
			if v.domain == domain && v.key.obs == obs && containsIdentity(v.subjects, d.Subject) {
				a.nodeVar[n.ID] = i
				break
			}
		}
		if a.nodeVar[id] == -1 {
			return fmt.Errorf("admission: internal error, no variable for node %q", id)
		}
	}
	return nil
}

func (a *Analyzer) applyHostConditions(vars []*conditionVar, participant map[Identity]*Participant) error {
	m := a.model
	used := make([]bool, len(m.HostConditions))
	for _, v := range vars {
		if v.domain == "" {
			continue
		}
		obsKey := observationKey(v.obs)
		matched := -1
		for i, hc := range m.HostConditions {
			if hc.Domain == v.domain && observationKey(hc.ObservedMessages) == obsKey {
				matched = i
				break
			}
		}
		if matched == -1 {
			return fmt.Errorf("admission: failure domain %q at observation %v has no host condition", v.domain, v.obs)
		}
		used[matched] = true
		hc := m.HostConditions[matched]
		if !containsString(v.actions, hc.Action) {
			return fmt.Errorf("admission: host condition action %q is not admissible for domain %q", hc.Action, v.domain)
		}
		v.hostFixed = true
		v.hostRule = hc.Rule
		v.hostAct = hc.Action
	}
	for i, u := range used {
		if !u {
			hc := m.HostConditions[i]
			// A host condition for an unknown subject is always invalid.
			domainKnown := false
			for _, p := range m.Participants {
				if p.FailureDomain == hc.Domain {
					domainKnown = true
				}
			}
			if !domainKnown {
				return fmt.Errorf("admission: host condition references unknown failure domain %q", hc.Domain)
			}
			return fmt.Errorf("admission: host condition for domain %q at observation %v matches no decision point", hc.Domain, hc.ObservedMessages)
		}
	}
	return nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func containsIdentity(list []Identity, s Identity) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
