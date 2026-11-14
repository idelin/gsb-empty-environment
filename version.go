package netsec

import "fmt"

// Policy declares the authentication and authorization requirements that a
// request must satisfy before it may use an API version. The router enforces
// the policy of the resolved version for every request, so a legacy path can
// never bypass the checks introduced by a newer version.
type Policy struct {
	// RequireLogin demands an authenticated identity.
	RequireLogin bool
	// RequireTenant demands that the identity carries a tenant scope.
	RequireTenant bool
	// RequiredRole, when non-empty, demands that the identity holds this role.
	RequiredRole string
}

// Version describes a single API version known to the gateway. Every version
// explicitly declares its capabilities and its field mapping; nothing is
// inherited implicitly between versions.
type Version struct {
	// Name is the version identifier as it appears in paths and headers,
	// e.g. "v1".
	Name string
	// Deprecated marks a version that is frozen: it keeps serving only the
	// capabilities it explicitly declares and never gains new ones.
	Deprecated bool
	// Policy is the authentication/authorization policy for this version.
	Policy Policy
	// Capabilities is the allow-list of capability identifiers this version
	// may invoke, e.g. "users.read".
	Capabilities map[string]bool
	// FieldMap maps canonical backend field names to the name exposed by
	// this version. A canonical field absent from FieldMap is hidden from
	// this version entirely; an empty exposed name also hides the field.
	FieldMap map[string]string
}

// HasCapability reports whether the version declares the given capability.
func (v *Version) HasCapability(capability string) bool {
	return v.Capabilities[capability]
}

// Registry holds the set of versions the gateway will route. Versions not
// registered here are unknown and are rejected before any backend call.
type Registry struct {
	versions map[string]*Version
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{versions: make(map[string]*Version)}
}

// Register adds a version to the registry. It returns an error if the
// version is invalid or its name is already registered.
func (r *Registry) Register(v *Version) error {
	if v == nil || v.Name == "" {
		return fmt.Errorf("netsec: version must have a name")
	}
	if _, exists := r.versions[v.Name]; exists {
		return fmt.Errorf("netsec: version %q already registered", v.Name)
	}
	r.versions[v.Name] = v
	return nil
}

// Lookup returns the registered version with the given name.
func (r *Registry) Lookup(name string) (*Version, bool) {
	v, ok := r.versions[name]
	return v, ok
}

// DefaultRegistry builds the registry for the platform incident: v1 keeps
// its legacy login-only policy and never sees the sensitive fields introduced
// for v2, while v2 requires tenant scope and the "admin" role.
func DefaultRegistry() *Registry {
	r := NewRegistry()

	_ = r.Register(&Version{
		Name: "v1",
		Policy: Policy{
			RequireLogin: true,
		},
		Capabilities: map[string]bool{
			"users.read": true,
		},
		FieldMap: map[string]string{
			"id":    "id",
			"name":  "name",
			"email": "email",
			// tenantID, roles, permissions: introduced for v2, hidden from v1.
		},
	})

	_ = r.Register(&Version{
		Name: "v2",
		Policy: Policy{
			RequireLogin:  true,
			RequireTenant: true,
			RequiredRole:  "admin",
		},
		Capabilities: map[string]bool{
			"users.read":           true,
			"users.read.sensitive": true,
			"users.manage":         true,
		},
		FieldMap: map[string]string{
			"id":          "id",
			"name":        "name",
			"email":       "email",
			"tenantID":    "tenantID",
			"roles":       "roles",
			"permissions": "permissions",
		},
	})

	return r
}
