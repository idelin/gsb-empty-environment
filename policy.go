package netsec

// NetworkPolicy is the selector-based subset of a Kubernetes NetworkPolicy
// needed to evaluate ingress peers. Ports, IPBlock peers, egress, and policy
// types are intentionally outside this library's scope.
type NetworkPolicy struct {
	// Namespace is the namespace in which the policy is defined.
	Namespace string `json:"namespace"`
	// PodSelector selects the destination pods to which this policy applies.
	// The same zero value as a Kubernetes empty selector matches every pod in
	// the policy's namespace.
	PodSelector LabelSelector `json:"podSelector"`
	// Ingress is the union of ingress rules. A nil or empty slice denies all
	// ingress, matching Kubernetes' behavior for an ingress-isolating policy
	// with no ingress rules.
	Ingress []IngressRule `json:"ingress,omitempty"`
}

// IngressRule is one ingress rule. If From is nil or empty, every source pod
// is allowed. Otherwise, From elements are evaluated as a union.
type IngressRule struct {
	From []PeerSelector `json:"from,omitempty"`
}

// PeerSelector combines namespaceSelector and podSelector semantics from a
// NetworkPolicyPeer.
//
//   - Both selectors present: they are intersected.
//   - Only NamespaceSelector present: every pod in a selected namespace.
//   - Only PodSelector present: every selected pod in the policy namespace.
//   - Both nil: every pod in every namespace.
type PeerSelector struct {
	NamespaceSelector *LabelSelector `json:"namespaceSelector,omitempty"`
	PodSelector       *LabelSelector `json:"podSelector,omitempty"`
}

// MutableNamespaceLabel identifies a namespace-label predicate that
// contributed to an allow result. Every namespace label except
// MetadataNameLabel is considered tenant-modifiable.
type MutableNamespaceLabel struct {
	Namespace string                `json:"namespace"`
	Key       string                `json:"key"`
	Operator  LabelSelectorOperator `json:"operator"`
	Values    []string              `json:"values,omitempty"`
}

// RuleMatch identifies an ingress path that allowed the source. Peer is -1
// when the rule had no From elements and therefore matched all sources.
type RuleMatch struct {
	Rule                   int                     `json:"rule"`
	Peer                   int                     `json:"peer"`
	MutableNamespaceLabels []MutableNamespaceLabel `json:"mutableNamespaceLabels,omitempty"`
}

// Decision is the result of an ingress evaluation.
type Decision struct {
	Allowed bool `json:"allowed"`

	// Matches lists every accepting ingress path. Multiple entries can match
	// because ingress rules and from elements are both unioned.
	Matches []RuleMatch `json:"matches,omitempty"`

	// MutableNamespaceLabels is the aggregate of namespace labels used by
	// accepting paths. ReliesOnMutableNamespaceLabels is true exactly when
	// this slice is non-empty.
	MutableNamespaceLabels         []MutableNamespaceLabel `json:"mutableNamespaceLabels,omitempty"`
	ReliesOnMutableNamespaceLabels bool                    `json:"reliesOnMutableNamespaceLabels"`
}

// EvaluateIngress decides whether source may send inbound traffic to
// destination under policy. The destination must be in the policy's namespace
// and match the policy-level podSelector; otherwise the result is a deny even
// when an ingress peer would match.
//
// An allow is reported when at least one ingress rule accepts the source. A
// rule with no From entries accepts every source. For rules with From entries,
// each element follows PeerSelector semantics and elements are unioned.
func EvaluateIngress(policy NetworkPolicy, source, destination Pod) Decision {
	decision := Decision{}

	if NamespaceName(destination.Namespace) != policy.Namespace {
		return decision
	}
	if !policy.PodSelector.matches(destination.Labels) {
		return decision
	}

	sourceNamespaceName := NamespaceName(source.Namespace)
	sourceNamespaceLabels := NamespaceLabels(source.Namespace)

	for ruleIndex, rule := range policy.Ingress {
		if len(rule.From) == 0 {
			decision.Matches = append(decision.Matches, RuleMatch{
				Rule: ruleIndex,
				Peer: -1,
			})
			continue
		}

		for peerIndex, peer := range rule.From {
			dependencies, matched := peer.matches(
				policy.Namespace,
				sourceNamespaceName,
				sourceNamespaceLabels,
				source.Labels,
			)
			if !matched {
				continue
			}
			decision.Matches = append(decision.Matches, RuleMatch{
				Rule:                   ruleIndex,
				Peer:                   peerIndex,
				MutableNamespaceLabels: dependencies,
			})
		}
	}

	if len(decision.Matches) == 0 {
		return decision
	}

	decision.Allowed = true
	for _, match := range decision.Matches {
		decision.MutableNamespaceLabels = append(
			decision.MutableNamespaceLabels,
			match.MutableNamespaceLabels...,
		)
	}
	decision.ReliesOnMutableNamespaceLabels = len(decision.MutableNamespaceLabels) > 0

	return decision
}

func (p PeerSelector) matches(
	policyNamespace string,
	sourceNamespaceName string,
	sourceNamespaceLabels map[string]string,
	sourcePodLabels map[string]string,
) ([]MutableNamespaceLabel, bool) {
	var dependencies []MutableNamespaceLabel

	switch {
	case p.NamespaceSelector != nil:
		if !p.NamespaceSelector.matches(sourceNamespaceLabels) {
			return nil, false
		}
		dependencies = p.NamespaceSelector.mutableNamespaceLabels(sourceNamespaceName)
	case p.PodSelector != nil:
		// A podSelector without a namespaceSelector is scoped to the policy's
		// own namespace. This comparison uses the immutable namespace name.
		if sourceNamespaceName != policyNamespace {
			return nil, false
		}
	}

	// A nil podSelector means every pod in the selected namespaces. An empty
	// non-nil podSelector also matches every pod, but retains the namespace
	// scoping rule above.
	if p.PodSelector != nil && !p.PodSelector.matches(sourcePodLabels) {
		return nil, false
	}

	return dependencies, true
}
