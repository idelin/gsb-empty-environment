package depseal

// Authorizer decides whether a tenant may use a given source. Implementations
// must be safe for concurrent use.
type Authorizer interface {
	Allowed(tenant, sourceID string) bool
}

// StaticAuthorizer is an in-memory Authorizer mapping tenants to the set of
// source IDs they may access. It is convenient for installers that resolve
// permissions once at startup and for tests.
type StaticAuthorizer struct {
	grants map[string]map[string]struct{}
}

// NewStaticAuthorizer builds an authorizer from a tenant -> source IDs map.
func NewStaticAuthorizer(grants map[string][]string) *StaticAuthorizer {
	a := &StaticAuthorizer{grants: make(map[string]map[string]struct{}, len(grants))}
	for tenant, sources := range grants {
		set := make(map[string]struct{}, len(sources))
		for _, sourceID := range sources {
			set[sourceID] = struct{}{}
		}
		a.grants[tenant] = set
	}
	return a
}

// Allowed reports whether tenant may use sourceID.
func (a *StaticAuthorizer) Allowed(tenant, sourceID string) bool {
	sources, ok := a.grants[tenant]
	if !ok {
		return false
	}
	_, ok = sources[sourceID]
	return ok
}
