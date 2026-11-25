// Package impersonate implements Kubernetes-style impersonation authorization
// for an HTTP API server.
//
// An authenticated requester may carry Impersonate-* headers that describe
// another identity. Every impersonated object (user, group, service account,
// uid and extra key) is authorized independently with verb "impersonate"
// before the request is allowed to proceed, and on success the request
// context carries only the impersonated identity: the original identity is
// recorded in the audit trail but its groups and permissions never carry
// over.
package impersonate

import "context"

// Standard impersonation headers, matching the Kubernetes API conventions.
const (
	ImpersonateUserHeader        = "Impersonate-User"
	ImpersonateGroupHeader       = "Impersonate-Group"
	ImpersonateUIDHeader         = "Impersonate-Uid"
	ImpersonateExtraHeaderPrefix = "Impersonate-Extra-"
)

// Verb and resource names used in impersonation authorization checks.
const (
	VerbImpersonate          = "impersonate"
	ResourceUsers            = "users"
	ResourceGroups           = "groups"
	ResourceServiceAccounts  = "serviceaccounts"
	ResourceUIDs             = "uids"
	ResourceUserExtras       = "userextras"
	ServiceAccountAPIGroup   = ""
	ServiceAccountNamePrefix = "system:serviceaccount:"
)

// UserInfo describes an authenticated or impersonated identity.
type UserInfo interface {
	GetUserName() string
	GetUID() string
	GetGroups() []string
	GetExtra() map[string][]string
}

// DefaultUserInfo is the concrete UserInfo implementation produced by the
// impersonation middleware.
type DefaultUserInfo struct {
	Name   string
	UID    string
	Groups []string
	Extra  map[string][]string
}

func (u *DefaultUserInfo) GetUserName() string           { return u.Name }
func (u *DefaultUserInfo) GetUID() string                { return u.UID }
func (u *DefaultUserInfo) GetGroups() []string           { return u.Groups }
func (u *DefaultUserInfo) GetExtra() map[string][]string { return u.Extra }

// Attributes describes a single impersonation authorization request.
//
// Extra keys are represented as Resource=userextras with Subresource set to
// the decoded, lower-cased key (i.e. resource "userextras/<key>"). Name is
// the resourceName that may further restrict the rule; it is empty for
// userextras checks.
type Attributes struct {
	Verb        string
	APIGroup    string
	Namespace   string
	Resource    string
	Subresource string
	Name        string
}

// ResourcePath renders Resource and Subresource as "resource/subresource".
func (a Attributes) ResourcePath() string {
	if a.Subresource == "" {
		return a.Resource
	}
	return a.Resource + "/" + a.Subresource
}

// Decision is the outcome of an authorization check.
type Decision int

const (
	// DecisionNoOpinion means the authorizer has no rule for the request.
	// The impersonation middleware treats this as a denial.
	DecisionNoOpinion Decision = iota
	DecisionAllow
	DecisionDeny
)

// Authorizer decides whether a single impersonation check is allowed.
// A non-nil error is treated as a denial by the middleware.
type Authorizer interface {
	Authorize(ctx context.Context, attrs Attributes) (decision Decision, reason string, err error)
}

type userContextKey struct{}

// WithUser returns a context carrying the given identity. Authentication
// middleware uses it to install the requester; the impersonation middleware
// uses it again to replace the requester with the impersonated identity.
func WithUser(ctx context.Context, user UserInfo) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

// UserFrom extracts the identity installed by WithUser.
func UserFrom(ctx context.Context) (UserInfo, bool) {
	user, ok := ctx.Value(userContextKey{}).(UserInfo)
	return user, ok
}
