package xref

// RouteObject describes the referencing object, typically an HTTPRoute.
// Group and Kind carry the route's type identity; Namespace is the namespace
// the route object lives in.
type RouteObject struct {
	Group     string
	Kind      string
	Namespace string
	Name      string
}

// BackendObject describes a backendRef target, typically a core Service
// (empty Group). Namespace is the target namespace of the reference.
type BackendObject struct {
	Group     string
	Kind      string
	Namespace string
	Name      string
}

// GrantFrom mirrors one ReferenceGrant spec.from entry.
type GrantFrom struct {
	Group     string
	Kind      string
	Namespace string
}

// GrantTo mirrors one ReferenceGrant spec.to entry. An empty Name is the
// wildcard that matches every object of the given kind in the namespace.
type GrantTo struct {
	Group string
	Kind  string
	Name  string
}

// ReferenceGrant is a namespace-scoped grant object. Namespace must be the
// namespace the grant is created in, i.e. the namespace that owns the
// referenced backend. Grants are only ever consulted in the backend's own
// namespace; a grant placed anywhere else has no effect.
type ReferenceGrant struct {
	Namespace string
	Name      string
	From      []GrantFrom
	To        []GrantTo
}

// FromNamespaces mirrors Listener.AllowedRoutes.Namespaces.From.
type FromNamespaces string

const (
	// SameNamespace allows routes only from the gateway's own namespace. This
	// is also the effective default when allowedRoutes or namespaces is nil.
	SameNamespace FromNamespaces = "Same"
	// AllNamespaces allows routes from any namespace.
	AllNamespaces FromNamespaces = "All"
	// SelectorNamespace allows routes from namespaces matched by Selector.
	SelectorNamespace FromNamespaces = "Selector"
)

// LabelSelector is the subset of metav1.LabelSelector used here: every
// MatchLabels entry must be present with the same value on the namespace.
type LabelSelector struct {
	MatchLabels map[string]string
}

// RouteNamespaces mirrors AllowedRoutes.namespaces.
type RouteNamespaces struct {
	From     FromNamespaces
	Selector *LabelSelector
}

// AllowedRoutes mirrors a listener's allowedRoutes block.
type AllowedRoutes struct {
	Namespaces *RouteNamespaces
}

// Listener is the subset of a gateway listener needed for attachment checks.
type Listener struct {
	Name          string
	AllowedRoutes *AllowedRoutes
}
