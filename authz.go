package netsec

import (
	"context"
	"errors"
	"fmt"
)

const (
	// RBACAPIGroup is the API group that owns Role, ClusterRole,
	// RoleBinding, and ClusterRoleBinding.
	RBACAPIGroup = "rbac.authorization.k8s.io"

	// VerbEscalate bypasses the rule-by-rule escalation check for a Role.
	VerbEscalate = "escalate"
	// VerbBind bypasses the rule-by-rule check when binding a Role.
	VerbBind = "bind"

	// KindRole and KindClusterRole are the supported RoleRef kinds.
	KindRole        = "Role"
	KindClusterRole = "ClusterRole"
)

var (
	// ErrEscalation is returned when a Role would grant permissions the
	// requester does not hold and the requester cannot escalate the Role.
	ErrEscalation = errors.New("role contains permissions not held by requester and escalate is not allowed")

	// ErrBinding is returned when a binding would expose permissions the
	// requester does not hold and the requester cannot bind the referenced role.
	ErrBinding = errors.New("binding references permissions not held by requester and bind is not allowed")

	// ErrInvalidRoleRef is returned for a RoleRef that cannot identify an RBAC
	// Role or ClusterRole.
	ErrInvalidRoleRef = errors.New("invalid RoleRef")
)

// Attributes describes one authorization query. Namespace must be the
// namespace in which the permission would be exercised. For a cluster-scoped
// request it is empty.
//
// Resource is the fully-qualified resource, for example "pods" or
// "pods/exec". Name is the resource name being authorized, or empty when the
// request applies to all names.
type Attributes struct {
	Namespace      string
	APIGroup       string
	Resource       string
	Verb           string
	Name           string
	NonResourceURL string
}

// Authorizer answers whether the admission request's user currently holds a
// permission. Callers normally adapt a Kubernetes authorizer or a
// SubjectAccessReview client to this interface.
type Authorizer interface {
	Authorize(ctx context.Context, attrs Attributes) bool
}

// AuthorizeFunc adapts a function to Authorizer.
type AuthorizeFunc func(ctx context.Context, attrs Attributes) bool

// Authorize implements Authorizer.
func (f AuthorizeFunc) Authorize(ctx context.Context, attrs Attributes) bool {
	return f(ctx, attrs)
}

// Role is the subset of a Kubernetes Role needed by the authorization checks.
type Role struct {
	Name      string       `json:"name"`
	Namespace string       `json:"namespace,omitempty"`
	Rules     []PolicyRule `json:"rules"`
}

// RoleRef identifies the role referenced by a binding.
type RoleRef struct {
	APIGroup string `json:"apiGroup,omitempty"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

// RoleBinding describes a RoleBinding or ClusterRoleBinding. A non-empty
// Namespace is a namespace-scoped binding; an empty Namespace is cluster
// scoped.
type RoleBinding struct {
	Name      string  `json:"name"`
	Namespace string  `json:"namespace,omitempty"`
	RoleRef   RoleRef `json:"roleRef"`
}

// DeniedError describes the first permission that caused a check to fail.
// Errors.Is can be used with ErrEscalation or ErrBinding to classify it.
type DeniedError struct {
	Err         error
	Namespace   string
	Subject     string
	SubjectType string
	Missing     Attributes
}

func (e *DeniedError) Error() string {
	if e == nil {
		return "<nil>"
	}
	scope := "cluster-wide"
	if e.Namespace != "" {
		scope = fmt.Sprintf("namespace %q", e.Namespace)
	}
	return fmt.Sprintf("netsec: %s for %s %q in %s; missing permission: verb=%q apiGroup=%q resource=%q name=%q",
		e.Err, e.subjectKind(), e.Subject, scope,
		e.Missing.Verb, e.Missing.APIGroup, e.Missing.Resource, e.Missing.Name)
}

// Unwrap allows errors.Is(err, ErrEscalation) and errors.Is(err, ErrBinding).
func (e *DeniedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *DeniedError) subjectKind() string {
	if e.SubjectType == "" {
		return "role"
	}
	return e.SubjectType
}

// CheckRole checks a Role create or update. It succeeds if the requester can
// escalate roles in the Role's namespace, or if every permission in the new
// Rules is already held by the requester in that namespace.
//
// All rules in the new Role are checked. This is intentional: an update must
// not be able to retain a rule added by an earlier request whose authorization
// context has since changed.
func CheckRole(ctx context.Context, authz Authorizer, role Role) error {
	if authz == nil {
		return errors.New("netsec: nil authorizer")
	}

	if authz.Authorize(ctx, Attributes{
		Namespace: role.Namespace,
		APIGroup:  RBACAPIGroup,
		Resource:  "roles",
		Verb:      VerbEscalate,
		Name:      role.Name,
	}) {
		return nil
	}

	for _, attrs := range roleAttributes(role.Rules, role.Namespace) {
		if !authz.Authorize(ctx, attrs) {
			attrs.Namespace = role.Namespace
			return &DeniedError{
				Err:         ErrEscalation,
				Namespace:   role.Namespace,
				Subject:     role.Name,
				SubjectType: KindRole,
				Missing:     attrs,
			}
		}
	}
	return nil
}

// RoleAllowed is the boolean form of CheckRole.
func RoleAllowed(ctx context.Context, authz Authorizer, role Role) bool {
	return CheckRole(ctx, authz, role) == nil
}

// CheckRoleBinding checks a RoleBinding create or update against the already
// fetched rules of the referenced role. It succeeds if the requester can bind
// that role, or if every referenced permission is already held by the requester
// in the binding's namespace.
//
// referencedRules must come from the Role named by binding.RoleRef. When a
// RoleBinding references a ClusterRole, those rules are checked in the
// RoleBinding namespace. When Namespace is empty, the checks are cluster-wide.
// The function deliberately performs no lookup itself, so the caller should
// fetch the referenced object from an authoritative, live source during
// admission.
func CheckRoleBinding(ctx context.Context, authz Authorizer, binding RoleBinding, referencedRules []PolicyRule) error {
	if authz == nil {
		return errors.New("netsec: nil authorizer")
	}

	kind := normalizeKind(binding.RoleRef.Kind)
	roleResource := "roles"
	if kind == KindClusterRole {
		roleResource = "clusterroles"
	}
	if kind != KindRole && kind != KindClusterRole {
		return fmt.Errorf("%w: unsupported kind %q", ErrInvalidRoleRef, binding.RoleRef.Kind)
	}
	if binding.RoleRef.APIGroup != "" && binding.RoleRef.APIGroup != RBACAPIGroup {
		return fmt.Errorf("%w: unsupported API group %q", ErrInvalidRoleRef, binding.RoleRef.APIGroup)
	}

	if authz.Authorize(ctx, Attributes{
		Namespace: binding.Namespace,
		APIGroup:  RBACAPIGroup,
		Resource:  roleResource,
		Verb:      VerbBind,
		Name:      binding.RoleRef.Name,
	}) {
		return nil
	}

	for _, attrs := range roleAttributes(referencedRules, binding.Namespace) {
		if !authz.Authorize(ctx, attrs) {
			attrs.Namespace = binding.Namespace
			return &DeniedError{
				Err:         ErrBinding,
				Namespace:   binding.Namespace,
				Subject:     binding.RoleRef.Name,
				SubjectType: kind,
				Missing:     attrs,
			}
		}
	}
	return nil
}

// BindingAllowed is the boolean form of CheckRoleBinding.
func BindingAllowed(ctx context.Context, authz Authorizer, binding RoleBinding, referencedRules []PolicyRule) bool {
	return CheckRoleBinding(ctx, authz, binding, referencedRules) == nil
}

func roleAttributes(rules []PolicyRule, namespace string) []Attributes {
	var attrs []Attributes
	for _, rule := range rules {
		for _, attr := range ruleAttributes(rule) {
			attr.Namespace = namespace
			attrs = append(attrs, attr)
		}
	}
	return attrs
}
