package xref

import (
	"maps"
	"reflect"
	"strings"
	"sync"
)

// DecisionKind identifies what a cached decision was computed for.
type DecisionKind string

const (
	// DecisionBackend is a backendRef cross-namespace resolution.
	DecisionBackend DecisionKind = "BackendRef"
	// DecisionAttachment is a listener allowedRoutes attachment check.
	DecisionAttachment DecisionKind = "ListenerAttachment"
)

// Machine-readable reasons for backendRef decisions.
const (
	// ReasonSameNamespace: the backend is in the route's own namespace, so no
	// ReferenceGrant is required.
	ReasonSameNamespace = "SameNamespaceReferenceAllowed"
	// ReasonGranted: a ReferenceGrant in the backend namespace matched.
	ReasonGranted = "ReferenceGrantMatched"
	// ReasonGrantNotFound: the backend namespace has no ReferenceGrant at all.
	ReasonGrantNotFound = "ReferenceGrantNotFound"
	// ReasonFromMismatch: no grant "from" entry matched the route on group,
	// kind and namespace.
	ReasonFromMismatch = "ReferenceGrantFromNotMatched"
	// ReasonToMismatch: a "from" entry matched the route but no "to" entry
	// matched the backend on kind and (wildcard or exact) name.
	ReasonToMismatch = "ReferenceGrantToNotMatched"
)

// BackendDecision is the result of resolving one backendRef.
//
// Keep the returned value and pass it back to IsBackendValid to detect
// revocation. Revision is the resolver revision the decision was computed at.
type BackendDecision struct {
	Revision uint64
	Allowed  bool
	Reason   string

	route   RouteObject
	backend BackendObject
	key     string
}

type cacheEntry struct {
	kind    DecisionKind
	allowed bool
	reason  string
	// grantNS holds namespaces whose ReferenceGrants were consulted.
	grantNS map[string]struct{}
	// labelNS holds namespaces whose labels were consulted.
	labelNS map[string]struct{}
	// allowedRoutes captures the listener configuration for attachment
	// decisions so the decision can be re-evaluated without the caller.
	allowedRoutes *AllowedRoutes
}

// Resolver stores ReferenceGrants and namespace labels, resolves
// cross-namespace references and caches the resulting decisions.
//
// Every state-changing call bumps Revision and purges the cache entries whose
// dependencies changed, so decisions handed out earlier become invalid
// immediately when their authorizing grant is revoked.
type Resolver struct {
	mu sync.RWMutex

	grants   map[string]map[string]*ReferenceGrant // namespace -> grant name -> grant
	nsLabels map[string]map[string]string          // namespace -> labels
	cache    map[string]*cacheEntry
	revision uint64
}

// New returns an empty Resolver.
func New() *Resolver {
	return &Resolver{
		grants:   make(map[string]map[string]*ReferenceGrant),
		nsLabels: make(map[string]map[string]string),
		cache:    make(map[string]*cacheEntry),
	}
}

// Revision returns the current monotonic revision counter. It advances once
// per call that actually changes resolver state.
func (r *Resolver) Revision() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.revision
}

func joinKey(parts ...string) string {
	return strings.Join(parts, "\x00")
}

func backendKey(route RouteObject, backend BackendObject) string {
	return joinKey("backend",
		route.Group, route.Kind, route.Namespace, route.Name,
		backend.Group, backend.Kind, backend.Namespace, backend.Name)
}

func attachmentKey(gatewayNamespace, listenerName, routeNamespace string) string {
	return joinKey("attachment", gatewayNamespace, listenerName, routeNamespace)
}

// ResolveBackend decides whether route may reference backend.
//
// Same-namespace references are always allowed. A cross-namespace reference
// requires a ReferenceGrant in backend.Namespace with a "from" entry equal to
// the route on group, kind and namespace, and a "to" entry whose kind equals
// the backend kind and whose name is empty (wildcard) or equals the backend
// name. Per the required rule set, "to" group is not compared.
func (r *Resolver) ResolveBackend(route RouteObject, backend BackendObject) BackendDecision {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := backendKey(route, backend)
	if entry, ok := r.cache[key]; ok && entry.kind == DecisionBackend {
		return BackendDecision{
			Revision: r.revision,
			Allowed:  entry.allowed,
			Reason:   entry.reason,
			route:    route,
			backend:  backend,
			key:      key,
		}
	}

	decision := r.evaluateBackendLocked(route, backend)
	entry := &cacheEntry{
		kind:    DecisionBackend,
		allowed: decision.Allowed,
		reason:  decision.Reason,
	}
	// Only cross-namespace references consult grants, and only those in the
	// backend's own namespace.
	if backend.Namespace != "" && backend.Namespace != route.Namespace {
		entry.grantNS = map[string]struct{}{backend.Namespace: {}}
	}
	r.cache[key] = entry

	decision.Revision = r.revision
	decision.key = key
	return decision
}

// evaluateBackendLocked performs the actual authorization logic. Caller must
// hold r.mu (read lock is sufficient).
func (r *Resolver) evaluateBackendLocked(route RouteObject, backend BackendObject) BackendDecision {
	decision := BackendDecision{
		route:   route,
		backend: backend,
	}

	if backend.Namespace == "" || backend.Namespace == route.Namespace {
		decision.Allowed = true
		decision.Reason = ReasonSameNamespace
		return decision
	}

	grants := r.grants[backend.Namespace]
	if len(grants) == 0 {
		decision.Reason = ReasonGrantNotFound
		return decision
	}

	fromMatched := false
	for _, grant := range grants {
		for _, from := range grant.From {
			if from.Group != route.Group || from.Kind != route.Kind || from.Namespace != route.Namespace {
				continue
			}
			fromMatched = true
			for _, to := range grant.To {
				if to.Kind != backend.Kind {
					continue
				}
				if to.Name == "" || to.Name == backend.Name {
					decision.Allowed = true
					decision.Reason = ReasonGranted
					return decision
				}
			}
		}
	}

	if fromMatched {
		decision.Reason = ReasonToMismatch
	} else {
		decision.Reason = ReasonFromMismatch
	}
	return decision
}

// IsBackendValid reports whether decision still reflects the current store.
// It returns false as soon as the decision has been invalidated by a grant
// change or a live re-evaluation yields a different result.
func (r *Resolver) IsBackendValid(decision BackendDecision) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.cache[decision.key]
	if !ok || entry.kind != DecisionBackend {
		return false
	}
	live := r.evaluateBackendLocked(decision.route, decision.backend)
	return live.Allowed == decision.Allowed && live.Reason == decision.Reason
}

// UpsertGrant adds or replaces a ReferenceGrant. Decisions depending on
// grants in the grant's namespace are invalidated immediately.
func (r *Resolver) UpsertGrant(grant *ReferenceGrant) {
	if grant == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	bucket := r.grants[grant.Namespace]
	if bucket == nil {
		bucket = make(map[string]*ReferenceGrant)
		r.grants[grant.Namespace] = bucket
	}
	if reflect.DeepEqual(bucket[grant.Name], grant) {
		return
	}
	bucket[grant.Name] = cloneGrant(grant)
	r.bumpLocked()
	r.invalidateGrantNSLocked(grant.Namespace)
}

// DeleteGrant removes a ReferenceGrant. Authorized decisions that depended on
// this namespace become invalid immediately.
func (r *Resolver) DeleteGrant(namespace, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	bucket := r.grants[namespace]
	if bucket == nil {
		return
	}
	if _, ok := bucket[name]; !ok {
		return
	}
	delete(bucket, name)
	if len(bucket) == 0 {
		delete(r.grants, namespace)
	}
	r.bumpLocked()
	r.invalidateGrantNSLocked(namespace)
}

// SetNamespaceLabels creates or replaces the labels of a namespace. Cached
// selector-based attachment decisions for that namespace are invalidated.
func (r *Resolver) SetNamespaceLabels(namespace string, labels map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if reflect.DeepEqual(r.nsLabels[namespace], labels) {
		return
	}
	r.nsLabels[namespace] = maps.Clone(labels)
	r.bumpLocked()
	r.invalidateLabelNSLocked(namespace)
}

// DeleteNamespace removes a namespace together with its labels and every
// ReferenceGrant inside it.
func (r *Resolver) DeleteNamespace(namespace string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, hadLabels := r.nsLabels[namespace]
	_, hadGrants := r.grants[namespace]
	if !hadLabels && !hadGrants {
		return
	}
	delete(r.nsLabels, namespace)
	delete(r.grants, namespace)
	r.bumpLocked()
	r.invalidateLabelNSLocked(namespace)
	r.invalidateGrantNSLocked(namespace)
}

func (r *Resolver) bumpLocked() {
	r.revision++
}

func (r *Resolver) invalidateGrantNSLocked(namespace string) {
	for key, entry := range r.cache {
		if _, ok := entry.grantNS[namespace]; ok {
			delete(r.cache, key)
		}
	}
}

func (r *Resolver) invalidateLabelNSLocked(namespace string) {
	for key, entry := range r.cache {
		if _, ok := entry.labelNS[namespace]; ok {
			delete(r.cache, key)
		}
	}
}

func cloneGrant(grant *ReferenceGrant) *ReferenceGrant {
	copied := *grant
	if grant.From != nil {
		copied.From = append([]GrantFrom(nil), grant.From...)
	}
	if grant.To != nil {
		copied.To = append([]GrantTo(nil), grant.To...)
	}
	return &copied
}
