package netsec

import (
	"errors"
	"net/url"
	"strings"
	"sync"
)

// Sentinel errors returned by the registry and the token helpers.
var (
	// ErrResourceNotAbsolute is returned when a resource is not an absolute
	// URI (it is missing a scheme, or is otherwise a relative reference).
	ErrResourceNotAbsolute = errors.New("netsec: resource must be an absolute URI")

	// ErrResourceFragment is returned when a resource URI contains a fragment
	// component. RFC 8707 forbids fragments in resource indicators.
	ErrResourceFragment = errors.New("netsec: resource must not contain a fragment")

	// ErrResourceNotRegistered is returned when a requested resource has not
	// been registered with the authorization service.
	ErrResourceNotRegistered = errors.New("netsec: resource is not registered")

	// ErrEmptyResource is returned when a resource string is empty.
	ErrEmptyResource = errors.New("netsec: resource must not be empty")

	// ErrAudienceDenied is returned when a token's audience does not exactly
	// equal a resource the caller is allowed to use.
	ErrAudienceDenied = errors.New("netsec: token audience is not allowed")
)

// Registry holds the complete resource URIs that an authorization service is
// willing to issue tokens for.
//
// Matching is always an exact, case-sensitive string comparison. A registered
// resource is never treated as a prefix: registering "https://api.example/v1"
// does not make "https://api.example/v1/admin" valid.
type Registry struct {
	mu        sync.RWMutex
	resources map[string]struct{}
}

// NewRegistry creates an empty resource registry.
func NewRegistry() *Registry {
	return &Registry{resources: make(map[string]struct{})}
}

// ValidateResource enforces the syntax rules for an OAuth 2.0 resource
// indicator:
//
//   - it must be a non-empty absolute URI with a scheme, and
//   - it must not contain a fragment component.
//
// The URI is validated as supplied; no normalization is performed so that
// later exact matching cannot be bypassed through alternate spellings.
func ValidateResource(resource string) error {
	if resource == "" {
		return ErrEmptyResource
	}

	// Fragments are rejected on the raw string so that any spelling of a
	// fragment is caught regardless of how net/url interprets it.
	if strings.ContainsRune(resource, '#') {
		return ErrResourceFragment
	}

	parsed, err := url.Parse(resource)
	if err != nil {
		return ErrResourceNotAbsolute
	}

	if !parsed.IsAbs() {
		return ErrResourceNotAbsolute
	}

	// An absolute URI needs a real hierarchical location or an opaque
	// component. A bare scheme such as "https:" is not a usable resource.
	if parsed.Host == "" && parsed.Opaque == "" {
		return ErrResourceNotAbsolute
	}

	return nil
}

// Register adds a complete resource URI to the registry. The resource is
// validated first, so every registered value is a fragment-free absolute URI.
func (r *Registry) Register(resource string) error {
	if err := ValidateResource(resource); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.resources[resource] = struct{}{}
	return nil
}

// MustRegister registers a resource and panics if it is invalid. It is
// intended for populating a registry from trusted, static configuration.
func (r *Registry) MustRegister(resource string) {
	if err := r.Register(resource); err != nil {
		panic(err)
	}
}

// Registered reports whether the exact, complete resource URI has been
// registered. It performs no prefix or substring matching.
func (r *Registry) Registered(resource string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.resources[resource]
	return ok
}

// Allow validates the requested resource and confirms that the exact resource
// URI is registered. Appending a path to a registered resource, requesting a
// prefix of one, or using any other non-identical string fails with
// ErrResourceNotRegistered.
func (r *Registry) Allow(resource string) error {
	if err := ValidateResource(resource); err != nil {
		return err
	}
	if !r.Registered(resource) {
		return ErrResourceNotRegistered
	}
	return nil
}

// Resources returns a copy of the registered resource URIs.
func (r *Registry) Resources() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, 0, len(r.resources))
	for resource := range r.resources {
		out = append(out, resource)
	}
	return out
}
