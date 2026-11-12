package netsec

import "fmt"

// Access identifies one AMQP permission check.
type Access uint8

const (
	// Configure covers operations that create or delete broker resources.
	Configure Access = 1 << iota
	// Write covers operations that send data to a broker resource.
	Write
	// Read covers operations that consume data from a broker resource.
	Read
)

// String returns the AMQP permission name.
func (a Access) String() string {
	switch a {
	case Configure:
		return "configure"
	case Write:
		return "write"
	case Read:
		return "read"
	default:
		return fmt.Sprintf("access(%d)", uint8(a))
	}
}

// ResourceKind distinguishes the AMQP resource types that can be authorized.
type ResourceKind uint8

const (
	// ResourceQueue is a queue resource.
	ResourceQueue ResourceKind = iota + 1
	// ResourceExchange is an exchange resource.
	ResourceExchange
)

// String returns the AMQP resource-kind name.
func (k ResourceKind) String() string {
	switch k {
	case ResourceQueue:
		return "queue"
	case ResourceExchange:
		return "exchange"
	default:
		return fmt.Sprintf("resource-kind(%d)", uint8(k))
	}
}

// Resource is the resource a permission check applies to. Resource values are
// comparable and can therefore be used as cache keys.
type Resource struct {
	Kind ResourceKind
	Name string
}

// Queue returns a queue resource with the given name.
func Queue(name string) Resource {
	return Resource{Kind: ResourceQueue, Name: name}
}

// Exchange returns an exchange resource with the given name.
func Exchange(name string) Resource {
	return Resource{Kind: ResourceExchange, Name: name}
}

// String returns a readable resource description.
func (r Resource) String() string {
	return fmt.Sprintf("%s %q", r.Kind, r.Name)
}

// Credential is the credential snapshot supplied to an Authorizer. Generation
// is increased by every successful Connection.UpdateSecret call. Secret is the
// current connection secret; it is deliberately distinct from the generation so
// callers cannot mistake a repeated secret for a fresh authorization decision.
type Credential struct {
	ConnectionID string
	Generation   uint64
	Secret       string
}

// Authorizer decides whether credential grants access to resource. Implementations
// must be safe for concurrent calls and must not mutate the supplied values.
type Authorizer interface {
	Authorize(credential Credential, resource Resource, access Access) bool
}

// AuthorizerFunc adapts a function to an Authorizer. A nil function denies all
// requests.
type AuthorizerFunc func(credential Credential, resource Resource, access Access) bool

// Authorize implements Authorizer.
func (f AuthorizerFunc) Authorize(credential Credential, resource Resource, access Access) bool {
	if f == nil {
		return false
	}
	return f(credential, resource, access)
}
