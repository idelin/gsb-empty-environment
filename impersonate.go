package netsec

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Header names recognized by the impersonation protocol, mirroring the
// Kubernetes authentication header conventions.
const (
	ImpersonateUserHeader  = "Impersonate-User"
	ImpersonateUIDHeader   = "Impersonate-Uid"
	ImpersonateGroupHeader = "Impersonate-Group"
	ImpersonateExtraPrefix = "Impersonate-Extra-"
)

// Resources and verb used in impersonation authorization checks.
const (
	ImpersonateVerb = "impersonate"

	UserResource           = "users"
	GroupResource          = "groups"
	ServiceAccountResource = "serviceaccounts"
	UIDResource            = "uids"
	UserExtraResource      = "userextras"
)

// ServiceAccountUsernamePrefix marks service-account usernames of the form
// system:serviceaccount:<namespace>:<name>.
const ServiceAccountUsernamePrefix = "system:serviceaccount:"

const serviceAccountUsernameSegments = 4

// ErrGroupWithoutUser is returned when an Impersonate-Group header is present
// without an Impersonate-User header.
var ErrGroupWithoutUser = errors.New("impersonating a group without impersonating a user is not allowed")

// Identity describes an authenticated request principal.
type Identity interface {
	GetName() string
	GetUID() string
	GetGroups() []string
	GetExtra() map[string][]string
}

// UserInfo is the concrete Identity implementation carried in request
// contexts and authorization attributes.
type UserInfo struct {
	Name   string
	UID    string
	Groups []string
	Extra  map[string][]string
}

func (u UserInfo) GetName() string               { return u.Name }
func (u UserInfo) GetUID() string                { return u.UID }
func (u UserInfo) GetGroups() []string           { return u.Groups }
func (u UserInfo) GetExtra() map[string][]string { return u.Extra }

// Decision is the outcome of an authorization check.
type Decision int

const (
	// DecisionDeny means the authorizer explicitly forbids the action.
	DecisionDeny Decision = iota
	// DecisionAllow means the authorizer explicitly permits the action.
	DecisionAllow
	// DecisionNoOpinion means the authorizer has no configured rule for the
	// request; it must not be treated as an allow.
	DecisionNoOpinion
)

// Allowed reports whether the decision is an explicit allow.
func (d Decision) Allowed() bool { return d == DecisionAllow }

// Attributes describes a single authorization check. Every impersonated
// object produces its own Attributes value, so each object is checked
// independently.
type Attributes struct {
	User        Identity
	Verb        string
	APIGroup    string
	Resource    string
	Subresource string
	Name        string
}

// CombinedResource returns resource[/subresource], e.g. "userextras/role".
func (a Attributes) CombinedResource() string {
	if a.Subresource == "" {
		return a.Resource
	}
	return a.Resource + "/" + a.Subresource
}

// Authorizer decides whether a single described action is allowed.
type Authorizer interface {
	Authorize(attrs Attributes) (decision Decision, reason string, err error)
}

// ForbiddenError explains which impersonation check failed.
type ForbiddenError struct {
	Attrs  Attributes
	Reason string
}

func (e *ForbiddenError) Error() string {
	what := e.Attrs.CombinedResource()
	if e.Attrs.Name != "" {
		what += "/" + e.Attrs.Name
	}
	if e.Reason != "" {
		return fmt.Sprintf("forbidden: %q cannot %s %s: %s",
			e.Attrs.User.GetName(), e.Attrs.Verb, what, e.Reason)
	}
	return fmt.Sprintf("forbidden: %q cannot %s %s",
		e.Attrs.User.GetName(), e.Attrs.Verb, what)
}

// Check evaluates every impersonation header against authz. Each impersonated
// user, group, service account, UID and extra key triggers an independent
// "impersonate" authorization check.
//
// The returned bool reports whether impersonation was requested at all. When
// it is true and err is nil, the returned UserInfo is the identity that must
// replace the requestor for all downstream authorization decisions; the
// requestor's own permissions must not be carried forward.
func Check(requestor Identity, header http.Header, authz Authorizer) (UserInfo, bool, error) {
	if requestor == nil {
		return UserInfo{}, false, errors.New("impersonation requires an authenticated requestor")
	}
	if authz == nil {
		return UserInfo{}, false, errors.New("impersonation requires an authorizer")
	}

	requestedUser := header.Get(ImpersonateUserHeader)
	requestedGroups := dedup(header.Values(ImpersonateGroupHeader))
	requestedUID := header.Get(ImpersonateUIDHeader)

	extras, err := parseExtraHeaders(header)
	if err != nil {
		return UserInfo{}, true, err
	}

	requested := requestedUser != "" || len(requestedGroups) > 0 || requestedUID != "" || len(extras) > 0
	if !requested {
		return UserInfo{}, false, nil
	}

	// A group may only be claimed together with an explicitly impersonated
	// user; otherwise the requestor could silently attach privileged groups
	// to its own identity.
	if len(requestedGroups) > 0 && requestedUser == "" {
		return UserInfo{}, true, ErrGroupWithoutUser
	}

	if requestedUser != "" {
		if err := authorizeOne(authz, Attributes{
			User:     requestor,
			Verb:     ImpersonateVerb,
			Resource: UserResource,
			Name:     requestedUser,
		}); err != nil {
			return UserInfo{}, true, err
		}
		// Impersonating a service-account username additionally requires the
		// impersonate permission on the concrete serviceaccounts object.
		if _, name, ok := parseServiceAccountUsername(requestedUser); ok {
			if err := authorizeOne(authz, Attributes{
				User:     requestor,
				Verb:     ImpersonateVerb,
				Resource: ServiceAccountResource,
				Name:     name,
			}); err != nil {
				return UserInfo{}, true, err
			}
		}
	}

	for _, group := range requestedGroups {
		if err := authorizeOne(authz, Attributes{
			User:     requestor,
			Verb:     ImpersonateVerb,
			Resource: GroupResource,
			Name:     group,
		}); err != nil {
			return UserInfo{}, true, err
		}
	}

	if requestedUID != "" {
		if err := authorizeOne(authz, Attributes{
			User:     requestor,
			Verb:     ImpersonateVerb,
			Resource: UIDResource,
			Name:     requestedUID,
		}); err != nil {
			return UserInfo{}, true, err
		}
	}

	// Stable ordering keeps authorization evaluation and tests deterministic.
	extraKeys := make([]string, 0, len(extras))
	for key := range extras {
		extraKeys = append(extraKeys, key)
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		// Extra keys authorize on the key only; individual extra values are
		// not addressable via resourceNames.
		if err := authorizeOne(authz, Attributes{
			User:        requestor,
			Verb:        ImpersonateVerb,
			Resource:    UserExtraResource,
			Subresource: key,
		}); err != nil {
			return UserInfo{}, true, err
		}
	}

	impersonated := UserInfo{
		Name:   requestedUser,
		UID:    requestedUID,
		Groups: requestedGroups,
		Extra:  extras,
	}
	if impersonated.Name == "" {
		// UID/extra-only impersonation keeps the requestor's username.
		impersonated.Name = requestor.GetName()
	}
	return impersonated, true, nil
}

// parseExtraHeaders collects every Impersonate-Extra-<key> header. The key is
// percent-decoded and lowercased before it is used, so encoded variants of a
// forbidden key cannot bypass the check. Values for equivalent keys merge.
func parseExtraHeaders(header http.Header) (map[string][]string, error) {
	var extras map[string][]string
	for fullHeader, values := range header {
		if !strings.HasPrefix(strings.ToLower(fullHeader), strings.ToLower(ImpersonateExtraPrefix)) {
			continue
		}
		encodedKey := fullHeader[len(ImpersonateExtraPrefix):]
		decodedKey, err := url.QueryUnescape(encodedKey)
		if err != nil {
			return nil, fmt.Errorf("invalid percent-encoding in impersonation extra header %q: %w", fullHeader, err)
		}
		key := strings.ToLower(decodedKey)
		if extras == nil {
			extras = make(map[string][]string)
		}
		extras[key] = append(extras[key], values...)
	}
	return extras, nil
}

// parseServiceAccountUsername splits system:serviceaccount:<ns>:<name>.
func parseServiceAccountUsername(username string) (namespace, name string, ok bool) {
	parts := strings.Split(username, ":")
	if len(parts) != serviceAccountUsernameSegments ||
		parts[0] != "system" || parts[1] != "serviceaccount" {
		return "", "", false
	}
	return parts[2], parts[3], true
}

func authorizeOne(authz Authorizer, attrs Attributes) error {
	decision, reason, err := authz.Authorize(attrs)
	if err != nil {
		return fmt.Errorf("impersonation authorization error for %s: %w", attrs.CombinedResource(), err)
	}
	if !decision.Allowed() {
		return &ForbiddenError{Attrs: attrs, Reason: reason}
	}
	return nil
}

func dedup(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
