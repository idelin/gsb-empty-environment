package netsec

import (
	"errors"
	"strings"
)

// Errors returned by the router. Gateways should map them to HTTP status
// codes: ErrUnknownVersion/ErrVersionConflict -> 400/404, ErrUnauthorized ->
// 401, ErrForbidden/ErrCapabilityDenied -> 403.
var (
	ErrUnknownVersion   = errors.New("netsec: unknown API version")
	ErrVersionConflict  = errors.New("netsec: version header conflicts with path version")
	ErrUnauthorized     = errors.New("netsec: authentication required")
	ErrForbidden        = errors.New("netsec: identity does not satisfy version policy")
	ErrCapabilityDenied = errors.New("netsec: capability not available in this version")
)

// Identity is the authenticated principal attached to a request by the
// gateway's login middleware. A nil Identity means the request is
// unauthenticated.
type Identity struct {
	UserID   string
	TenantID string
	Roles    []string
}

// HasRole reports whether the identity holds the given role.
func (id *Identity) HasRole(role string) bool {
	if id == nil {
		return false
	}
	for _, r := range id.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Request is the gateway-normalized view of an incoming call.
type Request struct {
	// Path is the request path, expected to start with "/<version>/".
	Path string
	// VersionHeader is the optional version negotiated via header
	// (e.g. X-API-Version). When present it must agree with the path.
	VersionHeader string
	// Identity is the authenticated principal, or nil.
	Identity *Identity
}

// Record is a canonical backend response object keyed by field name.
type Record map[string]any

// Backend is the version-agnostic downstream handler shared by all API
// versions. It receives the requested capability and returns the canonical
// record; the router filters fields per version before responding.
type Backend func(capability string) (Record, error)

// Router resolves the API version first, then applies the resolved version's
// policy, capability allow-list, and field mapping around a shared backend.
type Router struct {
	registry *Registry
	backend  Backend
}

// NewRouter builds a Router over the given registry and backend.
func NewRouter(registry *Registry, backend Backend) *Router {
	return &Router{registry: registry, backend: backend}
}

// ResolveVersion determines the effective version for a request. The version
// is taken from the path prefix; a version header, when present, must match
// it. Unknown or unregistered versions are rejected here, before any
// authentication or backend work happens.
func (r *Router) ResolveVersion(req *Request) (*Version, error) {
	pathVersion := versionFromPath(req.Path)
	if pathVersion == "" {
		return nil, ErrUnknownVersion
	}
	if req.VersionHeader != "" && req.VersionHeader != pathVersion {
		return nil, ErrVersionConflict
	}
	v, ok := r.registry.Lookup(pathVersion)
	if !ok {
		return nil, ErrUnknownVersion
	}
	return v, nil
}

// Serve routes one request: resolve version, enforce that version's policy,
// check the capability allow-list, call the shared backend, and map the
// response fields for the resolved version.
func (r *Router) Serve(req *Request, capability string) (Record, error) {
	v, err := r.ResolveVersion(req)
	if err != nil {
		return nil, err
	}
	if err := authorize(v.Policy, req.Identity); err != nil {
		return nil, err
	}
	if !v.HasCapability(capability) {
		return nil, ErrCapabilityDenied
	}
	rec, err := r.backend(capability)
	if err != nil {
		return nil, err
	}
	return FilterFields(v, rec), nil
}

// authorize enforces a version's policy against the request identity. This
// is the single choke point every version passes through; versions differ
// only in the policy they declare.
func authorize(p Policy, id *Identity) error {
	if p.RequireLogin && id == nil {
		return ErrUnauthorized
	}
	if p.RequireTenant && (id == nil || id.TenantID == "") {
		return ErrForbidden
	}
	if p.RequiredRole != "" && !id.HasRole(p.RequiredRole) {
		return ErrForbidden
	}
	return nil
}

// versionFromPath extracts the version segment from a "/<version>/..." path.
func versionFromPath(path string) string {
	p := strings.TrimPrefix(path, "/")
	seg, _, _ := strings.Cut(p, "/")
	return seg
}
