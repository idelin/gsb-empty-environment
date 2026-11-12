package xref

import "maps"

// Machine-readable reasons for listener attachment decisions.
const (
	// ReasonSameNamespaceMode: Same mode and the route shares the gateway's
	// namespace.
	ReasonSameNamespaceMode = "SameNamespaceAllowed"
	// ReasonAllNamespacesMode: All mode allows the route.
	ReasonAllNamespacesMode = "AllNamespacesAllowed"
	// ReasonSelectorMatched: Selector mode and the route namespace labels
	// match.
	ReasonSelectorMatched = "NamespaceSelectorMatched"
	// ReasonSameNamespaceDeny: Same mode but the route is in another
	// namespace.
	ReasonSameNamespaceDeny = "RouteNamespaceIsNotGatewayNamespace"
	// ReasonSelectorMissing: Selector mode without a selector, so nothing can
	// be selected.
	ReasonSelectorMissing = "NamespaceSelectorRequiredButMissing"
	// ReasonSelectorNoMatch: Selector present but the namespace does not
	// satisfy every matchLabels entry.
	ReasonSelectorNoMatch = "NamespaceSelectorNotMatched"
)

// AttachmentDecision is the result of checking whether a route in a given
// namespace may attach to a listener.
type AttachmentDecision struct {
	Revision uint64
	Allowed  bool
	Reason   string

	gatewayNamespace string
	listenerName     string
	routeNamespace   string
	allowedRoutes    *AllowedRoutes
	key              string
}

// ResolveAttachment decides whether a route living in routeNamespace may
// attach to a listener of a gateway living in gatewayNamespace, according to
// the listener's allowedRoutes.
//
// A nil allowedRoutes or namespaces block is equivalent to From=Same.
func (r *Resolver) ResolveAttachment(gatewayNamespace string, listener Listener, routeNamespace string) AttachmentDecision {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := attachmentKey(gatewayNamespace, listener.Name, routeNamespace)
	if entry, ok := r.cache[key]; ok && entry.kind == DecisionAttachment {
		return AttachmentDecision{
			Revision:         r.revision,
			Allowed:          entry.allowed,
			Reason:           entry.reason,
			gatewayNamespace: gatewayNamespace,
			listenerName:     listener.Name,
			routeNamespace:   routeNamespace,
			allowedRoutes:    cloneAllowedRoutes(entry.allowedRoutes),
			key:              key,
		}
	}

	decision := r.evaluateAttachmentLocked(gatewayNamespace, listener, routeNamespace)
	entry := &cacheEntry{
		kind:          DecisionAttachment,
		allowed:       decision.Allowed,
		reason:        decision.Reason,
		allowedRoutes: cloneAllowedRoutes(listener.AllowedRoutes),
	}
	if r.effectiveFrom(listener) == SelectorNamespace {
		entry.labelNS = map[string]struct{}{routeNamespace: {}}
	}
	r.cache[key] = entry

	decision.Revision = r.revision
	decision.allowedRoutes = cloneAllowedRoutes(entry.allowedRoutes)
	decision.key = key
	return decision
}

// IsAttachmentValid reports whether decision still reflects the current
// namespace labels under the listener mode captured in the decision.
func (r *Resolver) IsAttachmentValid(decision AttachmentDecision) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.cache[decision.key]
	if !ok || entry.kind != DecisionAttachment {
		return false
	}
	listener := Listener{
		Name:          decision.listenerName,
		AllowedRoutes: decision.allowedRoutes,
	}
	live := r.evaluateAttachmentLocked(decision.gatewayNamespace, listener, decision.routeNamespace)
	return live.Allowed == decision.Allowed && live.Reason == decision.Reason
}

func (r *Resolver) effectiveFrom(listener Listener) FromNamespaces {
	if listener.AllowedRoutes == nil ||
		listener.AllowedRoutes.Namespaces == nil ||
		listener.AllowedRoutes.Namespaces.From == "" {
		return SameNamespace
	}
	return listener.AllowedRoutes.Namespaces.From
}

// evaluateAttachmentLocked performs the attachment logic. Caller must hold
// r.mu (read lock is sufficient).
func (r *Resolver) evaluateAttachmentLocked(gatewayNamespace string, listener Listener, routeNamespace string) AttachmentDecision {
	decision := AttachmentDecision{
		gatewayNamespace: gatewayNamespace,
		listenerName:     listener.Name,
		routeNamespace:   routeNamespace,
	}

	switch r.effectiveFrom(listener) {
	case AllNamespaces:
		decision.Allowed = true
		decision.Reason = ReasonAllNamespacesMode
		return decision
	case SelectorNamespace:
		ns := listener.AllowedRoutes.Namespaces
		if ns == nil || ns.Selector == nil {
			decision.Reason = ReasonSelectorMissing
			return decision
		}
		if labelsMatch(r.nsLabels[routeNamespace], ns.Selector) {
			decision.Allowed = true
			decision.Reason = ReasonSelectorMatched
			return decision
		}
		decision.Reason = ReasonSelectorNoMatch
		return decision
	default: // Same, including any unknown value: fail closed.
		if routeNamespace == gatewayNamespace {
			decision.Allowed = true
			decision.Reason = ReasonSameNamespaceMode
			return decision
		}
		decision.Reason = ReasonSameNamespaceDeny
		return decision
	}
}

// labelsMatch implements equality-based label selection: every selector
// requirement must be present with the identical value on the namespace. An
// empty selector matches everything; a non-empty selector never matches a
// namespace that has no labels.
func labelsMatch(namespaceLabels map[string]string, selector *LabelSelector) bool {
	if selector == nil || len(selector.MatchLabels) == 0 {
		return true
	}
	for key, value := range selector.MatchLabels {
		if namespaceLabels[key] != value {
			return false
		}
	}
	return true
}

// cloneAllowedRoutes deep-copies the listener configuration so later mutation
// of caller-owned structs cannot rewrite a cached decision.
func cloneAllowedRoutes(in *AllowedRoutes) *AllowedRoutes {
	if in == nil {
		return nil
	}
	out := &AllowedRoutes{}
	if in.Namespaces == nil {
		return out
	}
	out.Namespaces = &RouteNamespaces{From: in.Namespaces.From}
	if in.Namespaces.Selector != nil {
		out.Namespaces.Selector = &LabelSelector{
			MatchLabels: maps.Clone(in.Namespaces.Selector.MatchLabels),
		}
	}
	return out
}
