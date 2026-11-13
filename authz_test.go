package netsec

import (
	"context"
	"errors"
	"testing"
)

func TestRuleCovers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		granted   PolicyRule
		requested PolicyRule
		want      bool
	}{
		{
			name: "exact match",
			granted: PolicyRule{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
				Verbs:     []string{"get", "list"},
			},
			requested: PolicyRule{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
				Verbs:     []string{"get"},
			},
			want: true,
		},
		{
			name: "one extra verb is not covered",
			granted: PolicyRule{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
				Verbs:     []string{"get", "list"},
			},
			requested: PolicyRule{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
				Verbs:     []string{"get", "list", "delete"},
			},
			want: false,
		},
		{
			name: "base resource does not cover subresource",
			granted: PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"create"},
			},
			requested: PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"pods/exec"},
				Verbs:     []string{"create"},
			},
			want: false,
		},
		{
			name: "explicit subresource is covered",
			granted: PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"pods/exec"},
				Verbs:     []string{"create"},
			},
			requested: PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"pods/exec"},
				Verbs:     []string{"create"},
			},
			want: true,
		},
		{
			name: "resource wildcard covers subresource",
			granted: PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			requested: PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"pods/exec"},
				Verbs:     []string{"create"},
			},
			want: true,
		},
		{
			name: "requested wildcard needs wildcard grant",
			granted: PolicyRule{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
				Verbs:     []string{"get"},
			},
			requested: PolicyRule{
				APIGroups: []string{"*"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			want: false,
		},
		{
			name: "unrestricted names cover named request",
			granted: PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get"},
			},
			requested: PolicyRule{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get"},
				ResourceNames: []string{"known"},
			},
			want: true,
		},
		{
			name: "named grant covers subset",
			granted: PolicyRule{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get"},
				ResourceNames: []string{"known", "other"},
			},
			requested: PolicyRule{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get"},
				ResourceNames: []string{"known"},
			},
			want: true,
		},
		{
			name: "named grant does not cover all names",
			granted: PolicyRule{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get"},
				ResourceNames: []string{"known"},
			},
			requested: PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get"},
			},
			want: false,
		},
		{
			name: "all requested names must be covered",
			granted: PolicyRule{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get"},
				ResourceNames: []string{"known"},
			},
			requested: PolicyRule{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get"},
				ResourceNames: []string{"known", "new"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := RuleCovers(tt.granted, tt.requested); got != tt.want {
				t.Fatalf("RuleCovers() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRulesCoverCanCombineGrants(t *testing.T) {
	t.Parallel()

	granted := []PolicyRule{
		{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get"}},
		{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"list"}},
	}
	requested := []PolicyRule{
		{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "list"}},
	}

	if !RulesCover(granted, requested) {
		t.Fatal("RulesCover() = false, want true when the union of grants covers all tuples")
	}
}

func TestCheckRole(t *testing.T) {
	t.Parallel()

	const namespace = "team-a"
	role := Role{
		Name:      "reader",
		Namespace: namespace,
		Rules: []PolicyRule{{
			APIGroups: []string{"apps"},
			Resources: []string{"deployments"},
			Verbs:     []string{"get", "list"},
		}},
	}

	tests := []struct {
		name        string
		role        Role
		grants      []scopedGrant
		wantErr     error
		wantMissing Attributes
	}{
		{
			name: "existing permissions cover the role",
			role: role,
			grants: []scopedGrant{{
				namespace: namespace,
				rule: PolicyRule{
					APIGroups: []string{"apps"},
					Resources: []string{"deployments"},
					Verbs:     []string{"get", "list"},
				},
			}},
		},
		{
			name: "extra verb requires escalate",
			role: Role{
				Name:      role.Name,
				Namespace: namespace,
				Rules: []PolicyRule{{
					APIGroups: []string{"apps"},
					Resources: []string{"deployments"},
					Verbs:     []string{"get", "list", "delete"},
				}},
			},
			grants: []scopedGrant{{
				namespace: namespace,
				rule: PolicyRule{
					APIGroups: []string{"apps"},
					Resources: []string{"deployments"},
					Verbs:     []string{"get", "list"},
				},
			}},
			wantErr: ErrEscalation,
			wantMissing: Attributes{
				APIGroup: "apps",
				Resource: "deployments",
				Verb:     "delete",
			},
		},
		{
			name: "escalate exempts rule checks",
			role: Role{
				Name:      role.Name,
				Namespace: namespace,
				Rules: []PolicyRule{{
					APIGroups: []string{""},
					Resources: []string{"secrets"},
					Verbs:     []string{"delete"},
				}},
			},
			grants: []scopedGrant{escalateGrant(namespace, role.Name)},
		},
		{
			name: "escalate is narrowed by role name",
			role: Role{
				Name:      role.Name,
				Namespace: namespace,
				Rules: []PolicyRule{{
					APIGroups: []string{""},
					Resources: []string{"secrets"},
					Verbs:     []string{"delete"},
				}},
			},
			grants:      []scopedGrant{escalateGrant(namespace, "another-role")},
			wantErr:     ErrEscalation,
			wantMissing: Attributes{APIGroup: "", Resource: "secrets", Verb: "delete"},
		},
		{
			name: "subresource requires explicit permission",
			role: Role{
				Name:      role.Name,
				Namespace: namespace,
				Rules: []PolicyRule{{
					APIGroups: []string{""},
					Resources: []string{"pods/exec"},
					Verbs:     []string{"create"},
				}},
			},
			grants: []scopedGrant{{
				namespace: namespace,
				rule: PolicyRule{
					APIGroups: []string{""},
					Resources: []string{"pods"},
					Verbs:     []string{"create"},
				},
			}},
			wantErr:     ErrEscalation,
			wantMissing: Attributes{APIGroup: "", Resource: "pods/exec", Verb: "create"},
		},
		{
			name: "permissions in another namespace do not count",
			role: role,
			grants: []scopedGrant{{
				namespace: "team-b",
				rule: PolicyRule{
					APIGroups: []string{"apps"},
					Resources: []string{"deployments"},
					Verbs:     []string{"get", "list"},
				},
			}},
			wantErr:     ErrEscalation,
			wantMissing: Attributes{APIGroup: "apps", Resource: "deployments", Verb: "get"},
		},
		{
			name: "wildcard permissions cover concrete role rules",
			role: role,
			grants: []scopedGrant{{
				namespace: namespace,
				rule: PolicyRule{
					APIGroups: []string{"*"},
					Resources: []string{"*"},
					Verbs:     []string{"*"},
				},
			}},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			authz := newFakeAuthorizer(tt.grants...)
			err := CheckRole(context.Background(), authz, tt.role)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("CheckRole() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CheckRole() error = %v, want errors.Is(%v)", err, tt.wantErr)
			}
			var denied *DeniedError
			if !errors.As(err, &denied) {
				t.Fatalf("CheckRole() error type = %T, want *DeniedError", err)
			}
			tt.wantMissing.Namespace = tt.role.Namespace
			if denied.Missing != tt.wantMissing {
				t.Fatalf("missing permission = %#v, want %#v", denied.Missing, tt.wantMissing)
			}
		})
	}
}

func TestCheckRoleBinding(t *testing.T) {
	t.Parallel()

	const namespace = "team-a"

	roleRules := []PolicyRule{{
		APIGroups: []string{""},
		Resources: []string{"configmaps"},
		Verbs:     []string{"get"},
	}}
	binding := RoleBinding{
		Name:      "read-configmaps",
		Namespace: namespace,
		RoleRef: RoleRef{
			APIGroup: RBACAPIGroup,
			Kind:     KindRole,
			Name:     "reader",
		},
	}

	tests := []struct {
		name        string
		binding     RoleBinding
		rules       []PolicyRule
		grants      []scopedGrant
		wantErr     error
		wantMissing Attributes
	}{
		{
			name:    "referenced permissions are already held",
			binding: binding,
			rules:   roleRules,
			grants: []scopedGrant{{
				namespace: namespace,
				rule: PolicyRule{
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
					Verbs:     []string{"get"},
				},
			}},
		},
		{
			name:    "bind exempts uncovered referenced permissions",
			binding: binding,
			rules: []PolicyRule{{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"delete"},
			}},
			grants: []scopedGrant{bindGrant(namespace, "roles", binding.RoleRef.Name)},
		},
		{
			name:    "bind to another role does not exempt",
			binding: binding,
			rules: []PolicyRule{{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"delete"},
			}},
			grants:      []scopedGrant{bindGrant(namespace, "roles", "another-role")},
			wantErr:     ErrBinding,
			wantMissing: Attributes{APIGroup: "", Resource: "secrets", Verb: "delete"},
		},
		{
			name:    "namespace-scoped binding ignores permissions in another namespace",
			binding: binding,
			rules:   roleRules,
			grants: []scopedGrant{{
				namespace: "team-b",
				rule: PolicyRule{
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
					Verbs:     []string{"get"},
				},
			}},
			wantErr:     ErrBinding,
			wantMissing: Attributes{APIGroup: "", Resource: "configmaps", Verb: "get"},
		},
		{
			name:    "bind in another namespace does not exempt",
			binding: binding,
			rules: []PolicyRule{{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"delete"},
			}},
			grants:      []scopedGrant{bindGrant("team-b", "roles", binding.RoleRef.Name)},
			wantErr:     ErrBinding,
			wantMissing: Attributes{APIGroup: "", Resource: "secrets", Verb: "delete"},
		},
		{
			name: "binding a cluster role checks its rules in the binding namespace",
			binding: RoleBinding{
				Name:      "read-configmaps",
				Namespace: namespace,
				RoleRef: RoleRef{
					APIGroup: RBACAPIGroup,
					Kind:     KindClusterRole,
					Name:     "cluster-reader",
				},
			},
			rules: roleRules,
			grants: []scopedGrant{{
				namespace: namespace,
				rule: PolicyRule{
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
					Verbs:     []string{"get"},
				},
			}},
		},
		{
			name: "cluster role bind exemption uses clusterroles resource",
			binding: RoleBinding{
				Name:      "read-configmaps",
				Namespace: namespace,
				RoleRef: RoleRef{
					APIGroup: RBACAPIGroup,
					Kind:     KindClusterRole,
					Name:     "cluster-reader",
				},
			},
			rules: []PolicyRule{{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"delete"},
			}},
			grants: []scopedGrant{bindGrant(namespace, "clusterroles", "cluster-reader")},
		},
		{
			name:    "subresource in referenced role requires explicit permission",
			binding: binding,
			rules: []PolicyRule{{
				APIGroups: []string{""},
				Resources: []string{"pods/exec"},
				Verbs:     []string{"create"},
			}},
			grants: []scopedGrant{{
				namespace: namespace,
				rule: PolicyRule{
					APIGroups: []string{""},
					Resources: []string{"pods"},
					Verbs:     []string{"create"},
				},
			}},
			wantErr:     ErrBinding,
			wantMissing: Attributes{APIGroup: "", Resource: "pods/exec", Verb: "create"},
		},
		{
			name:    "resource name narrowing is enforced",
			binding: binding,
			rules: []PolicyRule{{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get"},
				ResourceNames: []string{"known", "new"},
			}},
			grants: []scopedGrant{{
				namespace: namespace,
				rule: PolicyRule{
					APIGroups:     []string{""},
					Resources:     []string{"secrets"},
					Verbs:         []string{"get"},
					ResourceNames: []string{"known"},
				},
			}},
			wantErr:     ErrBinding,
			wantMissing: Attributes{APIGroup: "", Resource: "secrets", Verb: "get", Name: "new"},
		},
		{
			name: "invalid role reference kind is rejected",
			binding: RoleBinding{
				Namespace: namespace,
				RoleRef: RoleRef{
					Kind: "Deployment",
					Name: "not-a-role",
				},
			},
			wantErr: ErrInvalidRoleRef,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			authz := newFakeAuthorizer(tt.grants...)
			err := CheckRoleBinding(context.Background(), authz, tt.binding, tt.rules)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("CheckRoleBinding() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CheckRoleBinding() error = %v, want errors.Is(%v)", err, tt.wantErr)
			}
			if tt.wantMissing != (Attributes{}) || errors.Is(err, ErrBinding) {
				var denied *DeniedError
				if !errors.As(err, &denied) {
					t.Fatalf("CheckRoleBinding() error type = %T, want *DeniedError", err)
				}
				tt.wantMissing.Namespace = tt.binding.Namespace
				if denied.Missing != tt.wantMissing {
					t.Fatalf("missing permission = %#v, want %#v", denied.Missing, tt.wantMissing)
				}
			}
		})
	}
}

func TestAuthorizeFuncAndBooleanHelpers(t *testing.T) {
	t.Parallel()

	allowRules := AuthorizeFunc(func(_ context.Context, attrs Attributes) bool {
		return attrs.Namespace == "team-a" &&
			attrs.APIGroup == "apps" &&
			attrs.Resource == "deployments" &&
			attrs.Verb == "get"
	})
	role := Role{
		Name:      "reader",
		Namespace: "team-a",
		Rules: []PolicyRule{{
			APIGroups: []string{"apps"},
			Resources: []string{"deployments"},
			Verbs:     []string{"get"},
		}},
	}
	binding := RoleBinding{
		Namespace: "team-a",
		RoleRef: RoleRef{
			Kind: KindRole,
			Name: "reader",
		},
	}

	if !RoleAllowed(context.Background(), allowRules, role) {
		t.Fatal("RoleAllowed() = false, want true")
	}
	if !BindingAllowed(context.Background(), allowRules, binding, role.Rules) {
		t.Fatal("BindingAllowed() = false, want true")
	}

	denied := &DeniedError{
		Err:         ErrEscalation,
		Namespace:   "team-a",
		Subject:     "reader",
		SubjectType: KindRole,
		Missing: Attributes{
			APIGroup: "apps",
			Resource: "deployments",
			Verb:     "delete",
		},
	}
	if !errors.Is(denied, ErrEscalation) {
		t.Fatal("DeniedError must unwrap to ErrEscalation")
	}
	if got := denied.Error(); got == "" {
		t.Fatal("DeniedError.Error() returned an empty message")
	}
}

func TestNonResourceRuleCoverage(t *testing.T) {
	t.Parallel()

	granted := PolicyRule{
		NonResourceURLs: []string{"/healthz"},
		Verbs:           []string{"get"},
	}
	requested := PolicyRule{
		NonResourceURLs: []string{"/healthz"},
		Verbs:           []string{"get"},
	}
	if !RuleCovers(granted, requested) {
		t.Fatal("RuleCovers() = false, want true for an identical non-resource rule")
	}
	if !RulesCover([]PolicyRule{granted}, []PolicyRule{requested}) {
		t.Fatal("RulesCover() = false, want true for an identical non-resource rule")
	}
}

func escalateGrant(namespace, roleName string) scopedGrant {
	return scopedGrant{
		namespace: namespace,
		rule: PolicyRule{
			APIGroups:     []string{RBACAPIGroup},
			Resources:     []string{"roles"},
			Verbs:         []string{VerbEscalate},
			ResourceNames: []string{roleName},
		},
	}
}

func bindGrant(namespace, resource, roleName string) scopedGrant {
	return scopedGrant{
		namespace: namespace,
		rule: PolicyRule{
			APIGroups:     []string{RBACAPIGroup},
			Resources:     []string{resource},
			Verbs:         []string{VerbBind},
			ResourceNames: []string{roleName},
		},
	}
}

type scopedGrant struct {
	namespace string
	rule      PolicyRule
}

type fakeAuthorizer struct {
	grants []scopedGrant
	calls  []Attributes
}

func newFakeAuthorizer(grants ...scopedGrant) *fakeAuthorizer {
	return &fakeAuthorizer{grants: grants}
}

func (f *fakeAuthorizer) Authorize(_ context.Context, attrs Attributes) bool {
	f.calls = append(f.calls, attrs)
	for _, grant := range f.grants {
		if grant.namespace != attrs.Namespace {
			continue
		}
		if !fakeMatch(grant.rule.Verbs, attrs.Verb) ||
			!fakeMatch(grant.rule.APIGroups, attrs.APIGroup) ||
			!fakeMatch(grant.rule.Resources, attrs.Resource) {
			continue
		}
		if len(grant.rule.ResourceNames) == 0 {
			return true
		}
		if attrs.Name == "" {
			continue
		}
		if fakeExactMatch(grant.rule.ResourceNames, attrs.Name) {
			return true
		}
	}
	return false
}

func fakeMatch(values []string, requested string) bool {
	for _, value := range values {
		if value == "*" || value == requested {
			return true
		}
	}
	return false
}

func fakeExactMatch(values []string, requested string) bool {
	for _, value := range values {
		if value == requested {
			return true
		}
	}
	return false
}
