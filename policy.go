package netsec

// Decision is the outcome of evaluating a request against the policy set.
type Decision int

const (
	// Deny blocks the request.
	Deny Decision = iota
	// Allow permits the request.
	Allow
)

func (d Decision) String() string {
	if d == Allow {
		return "Allow"
	}
	return "Deny"
}

// DefaultPolicy is the cluster-wide default applied to pod ports that are
// not selected by any Server resource.
type DefaultPolicy int

const (
	// DefaultAllow permits traffic to unselected ports.
	DefaultAllow DefaultPolicy = iota
	// DefaultDeny blocks traffic to unselected ports.
	DefaultDeny
)

// Authentication kinds supported by AuthorizationPolicy.
const (
	KindMeshTLSAuthentication = "MeshTLSAuthentication"
	KindNetworkAuthentication = "NetworkAuthentication"
)

// Server selects a single pod port. Once a port is selected by exactly one
// Server it becomes default-deny; a port selected by more than one Server is
// a configuration error and is always denied.
type Server struct {
	Name string
	Port int32
}

// MeshTLSAuthentication lists client identities. An identity may be exact
// (e.g. "web.ns.serviceaccount.identity.linkerd.cluster.local"), a wildcard
// of the form "*.<suffix>" where "*" replaces exactly the leftmost complete
// DNS label, or a bare "*" matching any non-empty identity.
type MeshTLSAuthentication struct {
	Name       string
	Identities []string
}

// NetworkAuthentication lists source CIDRs, e.g. "10.0.0.0/8".
type NetworkAuthentication struct {
	Name     string
	Networks []string
}

// AuthenticationRef references an authentication resource by kind and name.
type AuthenticationRef struct {
	Kind string
	Name string
}

// AuthorizationPolicy authorizes traffic to a pod port when any of its
// authentication references matches the request.
type AuthorizationPolicy struct {
	Name               string
	Port               int32
	AuthenticationRefs []AuthenticationRef
}

// Request describes an inbound connection to a pod port.
type Request struct {
	// SourceIP is the client IP in string form, e.g. "10.1.2.3".
	SourceIP string
	// Identity is the client's mesh TLS identity; empty means unauthenticated.
	Identity string
	// Port is the destination pod port.
	Port int32
	// Method is the HTTP method, e.g. "GET".
	Method string
	// Path is the HTTP request path.
	Path string
}

// Config bundles the policy resources the resolver evaluates.
type Config struct {
	// DefaultPolicy applies to ports not selected by any Server.
	DefaultPolicy DefaultPolicy
	// NodeCIDRs are the node networks kubelet probes originate from.
	NodeCIDRs []string
	// ProbePaths are the pod's declared HTTP probe paths.
	ProbePaths []string

	Servers      []Server
	Policies     []AuthorizationPolicy
	MeshTLSAuths []MeshTLSAuthentication
	NetworkAuths []NetworkAuthentication
}
