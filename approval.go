package netsec

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

// ResourceGrant is one resource approved by the authorization request and
// the scopes approved for that resource. A caller may use Scopes, or Scope
// (a space-delimited OAuth scope string), but not both.
type ResourceGrant struct {
	Resource string
	Scopes   []string
	Scope    string
}

// NewResourceGrant constructs a grant whose scopes are the supplied values.
func NewResourceGrant(resource string, scopes ...string) ResourceGrant {
	return ResourceGrant{Resource: resource, Scopes: append([]string(nil), scopes...)}
}

// Approval is the fixed resource set approved by an authorization request.
// It is a snapshot: later changes to its source slices do not alter it.
type Approval struct {
	grants    []ResourceGrant
	resources []string
	members   map[string]struct{}
}

// NewApproval validates and snapshots the approved resource grants. Duplicate
// resources are allowed only when they are exact duplicates; conflicting
// duplicate grants are rejected rather than silently merged.
func NewApproval(grants []ResourceGrant) (*Approval, error) {
	approval := &Approval{
		grants:    make([]ResourceGrant, 0, len(grants)),
		resources: make([]string, 0, len(grants)),
		members:   make(map[string]struct{}, len(grants)),
	}

	for _, grant := range grants {
		if err := ValidateResource(grant.Resource); err != nil {
			return nil, err
		}
		scopes, err := grantScopes(grant)
		if err != nil {
			return nil, err
		}

		if _, exists := approval.members[grant.Resource]; exists {
			previous := approval.grantFor(grant.Resource)
			if !sameStrings(previous.Scopes, scopes) {
				return nil, &Error{
					Code:        InvalidTarget,
					Description: "duplicate approved resource has conflicting scopes",
					Resource:    grant.Resource,
				}
			}
			continue
		}

		approval.members[grant.Resource] = struct{}{}
		approval.resources = append(approval.resources, grant.Resource)
		approval.grants = append(approval.grants, ResourceGrant{
			Resource: grant.Resource,
			Scopes:   append([]string(nil), scopes...),
		})
	}
	return approval, nil
}

// NewApprovalFromScopes is a convenience constructor for callers that keep
// approved scopes in a map keyed by resource. Resources are ordered
// lexicographically so construction is deterministic.
func NewApprovalFromScopes(scopes map[string][]string) (*Approval, error) {
	keys := make([]string, 0, len(scopes))
	for resource := range scopes {
		keys = append(keys, resource)
	}
	sort.Strings(keys)

	grants := make([]ResourceGrant, 0, len(keys))
	for _, resource := range keys {
		grants = append(grants, ResourceGrant{
			Resource: resource,
			Scopes:   append([]string(nil), scopes[resource]...),
		})
	}
	return NewApproval(grants)
}

// ApproveResources is an alias for NewApproval.
func ApproveResources(grants []ResourceGrant) (*Approval, error) {
	return NewApproval(grants)
}

// Resources returns a copy of the exact, approved resource identifiers in
// authorization-request order.
func (a *Approval) Resources() []string {
	if a == nil {
		return nil
	}
	return append([]string(nil), a.resources...)
}

// ApprovedResources is an alias for Resources.
func (a *Approval) ApprovedResources() []string {
	return a.Resources()
}

// ResolveTokenResources enforces the resource subset rule for a token
// request. If no resource is supplied, the sole approved resource is used.
// Supplying no resource when multiple resources were approved is
// invalid_target, not an instruction to issue a token for all of them.
func (a *Approval) ResolveTokenResources(requested []string) ([]string, error) {
	if a == nil {
		return nil, &Error{Code: InvalidTarget, Description: "authorization approval is missing"}
	}

	resources, err := ParseResources(requested)
	if err != nil {
		return nil, err
	}
	if len(resources) == 0 {
		if len(a.resources) == 1 {
			return []string{a.resources[0]}, nil
		}
		return nil, &Error{
			Code:        InvalidTarget,
			Description: "resource is required when multiple resources were approved",
		}
	}

	for _, resource := range resources {
		if _, ok := a.members[resource]; !ok {
			return nil, &Error{
				Code:        InvalidTarget,
				Description: "resource was not approved by the authorization request",
				Resource:    resource,
			}
		}
	}
	return resources, nil
}

// ScopeFor returns the intersection of the scopes approved for each resolved
// resource. Requested resources must be a subset of the fixed approval.
func (a *Approval) ScopeFor(resources []string) ([]string, error) {
	resolved, err := a.ResolveTokenResources(resources)
	if err != nil {
		return nil, err
	}
	return a.scopeForResolved(resolved), nil
}

// ScopesFor is an alias for ScopeFor.
func (a *Approval) ScopesFor(resources []string) ([]string, error) {
	return a.ScopeFor(resources)
}

// AudienceFor validates resources and returns the exact audience that must be
// written into a token for them.
func (a *Approval) AudienceFor(resources []string) (Audience, error) {
	resolved, err := a.ResolveTokenResources(resources)
	if err != nil {
		return nil, err
	}
	return NewAudience(resolved), nil
}

func (a *Approval) scopeForResolved(resources []string) []string {
	if len(resources) == 0 {
		return nil
	}
	sets := make([][]string, 0, len(resources))
	for _, resource := range resources {
		grant := a.grantFor(resource)
		sets = append(sets, grant.Scopes)
	}
	return ScopeIntersection(sets...)
}

// Resolution is a fully validated token-request resource decision.
type Resolution struct {
	Resources []string
	Audience  Audience
	Scope     []string
	Scopes    []string
}

// Resolve validates requested resources and computes the exact audience and
// the scope intersection. If requestedScopes is non-empty it may narrow the
// result, but it may not add any scope outside the approved intersection.
func (a *Approval) Resolve(requestedResources, requestedScopes []string) (Resolution, error) {
	resources, err := a.ResolveTokenResources(requestedResources)
	if err != nil {
		return Resolution{}, err
	}

	approvedScopes := a.scopeForResolved(resources)
	scopes := approvedScopes
	if len(requestedScopes) > 0 {
		approved := make(map[string]struct{}, len(approvedScopes))
		for _, scope := range approvedScopes {
			approved[scope] = struct{}{}
		}
		for _, scope := range requestedScopes {
			if _, ok := approved[scope]; !ok {
				return Resolution{}, &Error{
					Code:        InvalidScope,
					Description: "requested scope is not approved for all requested resources",
				}
			}
		}
		scopes = canonicalStrings(requestedScopes)
	}

	return Resolution{
		Resources: append([]string(nil), resources...),
		Audience:  NewAudience(resources),
		Scope:     append([]string(nil), scopes...),
		Scopes:    append([]string(nil), scopes...),
	}, nil
}

func (a *Approval) grantFor(resource string) ResourceGrant {
	for _, grant := range a.grants {
		if grant.Resource == resource {
			return grant
		}
	}
	return ResourceGrant{}
}

func grantScopes(grant ResourceGrant) ([]string, error) {
	if len(grant.Scopes) > 0 && strings.TrimSpace(grant.Scope) != "" {
		return nil, &Error{
			Code:        InvalidTarget,
			Description: "grant must use either Scopes or Scope, not both",
			Resource:    grant.Resource,
		}
	}
	if len(grant.Scopes) > 0 {
		return canonicalStrings(grant.Scopes), nil
	}
	if strings.TrimSpace(grant.Scope) == "" {
		return nil, nil
	}
	return canonicalStrings(strings.Fields(grant.Scope)), nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// TokenRequest contains the resource and scope parameters of an authorization
// code, client credentials, or refresh token request. Resource is an alias for
// Resources to accommodate callers that model the singular parameter name;
// Resources takes precedence when both are set.
type TokenRequest struct {
	Resources []string `json:"resource,omitempty"`
	Resource  []string `json:"-"`
	Scopes    []string `json:"scope,omitempty"`
	Scope     []string `json:"-"`
}

func (r TokenRequest) resourceValues() []string {
	if len(r.Resources) > 0 {
		return r.Resources
	}
	return r.Resource
}

func (r TokenRequest) scopeValues() []string {
	if len(r.Scopes) > 0 {
		return r.Scopes
	}
	return r.Scope
}

// ResolveResources is the package-level resource subset/default resolver.
func ResolveResources(approval *Approval, requested []string) ([]string, error) {
	return approval.ResolveTokenResources(requested)
}

// TokenResources is an alias for ResolveResources.
func TokenResources(approval *Approval, requested []string) ([]string, error) {
	return ResolveResources(approval, requested)
}

// ResolveAudience is the package-level exact-audience resolver.
func ResolveAudience(approval *Approval, requested []string) (Audience, error) {
	return approval.AudienceFor(requested)
}

// ResolveScopes is the package-level approved-scope intersection resolver.
func ResolveScopes(approval *Approval, resources []string) ([]string, error) {
	return approval.ScopeFor(resources)
}

// Token is the security-relevant portion of an issued access token. Audience
// is exactly the resolved token-request resource set, never the full
// authorization approval. Scope is the approved scope intersection (optionally
// narrowed by the token request).
type Token struct {
	Audience    Audience `json:"aud"`
	Scope       []string `json:"-"`
	Scopes      []string `json:"-"`
	ScopeString string   `json:"-"`
	Resources   []string `json:"-"`
	Aud         []string `json:"-"`
}

// MarshalJSON emits a standard aud value and a space-delimited OAuth scope.
func (t Token) MarshalJSON() ([]byte, error) {
	type tokenJSON struct {
		Audience Audience `json:"aud"`
		Scope    string   `json:"scope,omitempty"`
	}
	return json.Marshal(tokenJSON{
		Audience: t.Audience,
		Scope:    FormatScope(t.Scope),
	})
}

// UnmarshalJSON decodes a token's aud and space-delimited scope.
func (t *Token) UnmarshalJSON(data []byte) error {
	var payload struct {
		Audience Audience `json:"aud"`
		Scope    string   `json:"scope"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	t.Audience = payload.Audience
	t.Resources = payload.Audience.Values()
	t.Aud = payload.Audience.Values()
	t.Scope = ParseScope(payload.Scope)
	t.Scopes = append([]string(nil), t.Scope...)
	t.ScopeString = payload.Scope
	return nil
}

// IssueToken resolves a token request against the fixed approval and returns
// the audience and scope that must be put into the signed token.
func (a *Approval) IssueToken(request TokenRequest) (Token, error) {
	resolution, err := a.Resolve(request.resourceValues(), request.scopeValues())
	if err != nil {
		return Token{}, err
	}
	return Token{
		Audience:    resolution.Audience,
		Scope:       append([]string(nil), resolution.Scope...),
		Scopes:      append([]string(nil), resolution.Scope...),
		ScopeString: FormatScope(resolution.Scope),
		Resources:   append([]string(nil), resolution.Resources...),
		Aud:         append([]string(nil), resolution.Resources...),
	}, nil
}

// IssueToken is the package-level form of Approval.IssueToken.
func IssueToken(approval *Approval, request TokenRequest) (Token, error) {
	return approval.IssueToken(request)
}

// IssueTokenFromValues builds a token from decoded request parameters. The
// resource key is read as a repeated form value, not as one comma-joined
// string.
func (a *Approval) IssueTokenFromValues(values url.Values) (Token, error) {
	resources, err := ParseResourceParameter(values)
	if err != nil {
		return Token{}, err
	}
	return a.IssueToken(TokenRequest{
		Resources: resources,
		Scope:     ParseScope(values.Get("scope")),
	})
}

// IssueTokenFromValues is the package-level form of
// Approval.IssueTokenFromValues.
func IssueTokenFromValues(approval *Approval, values url.Values) (Token, error) {
	return approval.IssueTokenFromValues(values)
}

// RefreshGrant represents a refresh token bound to the resource approval from
// the original authorization request. It deliberately carries the original
// Approval, not just the audience of the previous access token.
type RefreshGrant struct {
	approval *Approval
}

// NewRefreshGrant binds a refresh flow to the original fixed approval.
func NewRefreshGrant(approval *Approval) *RefreshGrant {
	return &RefreshGrant{approval: approval}
}

// Issue resolves the new token request against the original approval. A
// resource absent from the original authorization request is invalid_target,
// even if it appeared in a previous access token or is otherwise benign.
func (r *RefreshGrant) Issue(request TokenRequest) (Token, error) {
	if r == nil || r.approval == nil {
		return Token{}, &Error{Code: InvalidTarget, Description: "refresh grant is missing its original approval"}
	}
	return r.approval.IssueToken(request)
}

// IssueFromValues is the refresh-token equivalent of
// Approval.IssueTokenFromValues.
func (r *RefreshGrant) IssueFromValues(values url.Values) (Token, error) {
	if r == nil || r.approval == nil {
		return Token{}, &Error{Code: InvalidTarget, Description: "refresh grant is missing its original approval"}
	}
	return r.approval.IssueTokenFromValues(values)
}

// RefreshToken is the package-level refresh path. The original approval must
// be retained by the refresh token store and supplied here.
func RefreshToken(approval *Approval, request TokenRequest) (Token, error) {
	return NewRefreshGrant(approval).Issue(request)
}

// IssueRefreshToken is an alias for RefreshToken.
func IssueRefreshToken(approval *Approval, request TokenRequest) (Token, error) {
	return RefreshToken(approval, request)
}

// RefreshTokenFromValues is the package-level form of
// RefreshGrant.IssueFromValues.
func RefreshTokenFromValues(approval *Approval, values url.Values) (Token, error) {
	return NewRefreshGrant(approval).IssueFromValues(values)
}
