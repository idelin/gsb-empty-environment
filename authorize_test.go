package netsec

import (
	"errors"
	"testing"
)

func approvedFixture(t *testing.T, mutable ...string) (*Approval, *fieldRegistry) {
	t.Helper()
	approval, err := BindApproval("ap-1", baseTemplate(), mutable)
	if err != nil {
		t.Fatalf("BindApproval failed: %v", err)
	}
	registry, err := newFieldRegistry(mutable)
	if err != nil {
		t.Fatalf("newFieldRegistry failed: %v", err)
	}
	return approval, registry
}

func TestAuthorizeAllowsMutableField(t *testing.T) {
	approval, registry := approvedFixture(t, "/spec/replicas", "/spec/labels")
	result, decisions, err := Authorize(approval.Bound, registry, []PatchOperation{
		{Op: "replace", Path: "/spec/replicas", Value: float64(9)},
	})
	if err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
	if replicas := result.Template.Spec.Replicas; replicas == nil || *replicas != 9 {
		t.Fatalf("replicas not applied: %+v", replicas)
	}
	if len(decisions) != 1 || decisions[0].Verdict != VerdictAllow ||
		decisions[0].Basis != BasisMutableAllowList {
		t.Fatalf("unexpected decisions: %+v", decisions)
	}
}

func TestAuthorizeDeniesImmutableAndUnregisteredFields(t *testing.T) {
	approval, registry := approvedFixture(t, "/spec/replicas")

	_, decisions, err := Authorize(approval.Bound, registry, []PatchOperation{
		{Op: "replace", Path: "/spec/env/0/value", Value: "hacked"},
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("expected errDenied, got %v", err)
	}
	if decisions[0].Basis != BasisImmutableField || decisions[0].Verdict != VerdictDeny {
		t.Fatalf("env change should be immutable: %+v", decisions[0])
	}

	_, decisions, err = Authorize(approval.Bound, registry, []PatchOperation{
		{Op: "add", Path: "/spec/hostNetwork", Value: true},
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("expected errDenied, got %v", err)
	}
	if decisions[0].Basis != BasisUnregisteredField {
		t.Fatalf("unknown field should be unregistered: %+v", decisions[0])
	}
}

func TestAuthorizeFreezesIdentityAndSecurity(t *testing.T) {
	approval, registry := approvedFixture(t, "/spec/replicas")

	cases := []struct {
		name string
		op   PatchOperation
	}{
		{"identity", PatchOperation{Op: "replace", Path: "/spec/identity/serviceAccountName", Value: "evil-sa"}},
		{"group", PatchOperation{Op: "add", Path: "/spec/identity/groupNames/-", Value: "cluster-admin"}},
		{"nonroot", PatchOperation{Op: "replace", Path: "/spec/security/runAsNonRoot", Value: false}},
		{"privileged", PatchOperation{Op: "replace", Path: "/spec/security/privileged", Value: true}},
		{"capability", PatchOperation{Op: "add", Path: "/spec/capabilities/-", Value: "SYS_ADMIN"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, decisions, err := Authorize(approval.Bound, registry, []PatchOperation{tc.op})
			if !errors.Is(err, errDenied) {
				t.Fatalf("expected denial, got %v", err)
			}
			if len(decisions) != 1 || decisions[0].Basis != BasisFrozenSecurity {
				t.Fatalf("expected frozen-security basis: %+v", decisions)
			}
		})
	}
}

func TestAuthorizeDeletionRestoresApprovedDefault(t *testing.T) {
	tmpl := baseTemplate()
	tmpl.Spec.Security.RunAsNonRoot = BoolPtr(defaultRunAsNonRoot)
	approval, err := BindApproval("ap-defaults", tmpl, []string{"/spec/replicas"})
	if err != nil {
		t.Fatal(err)
	}
	registry, _ := newFieldRegistry(approval.MutableFields)

	result, decisions, err := Authorize(approval.Bound, registry, []PatchOperation{
		{Op: "remove", Path: "/spec/security/runAsNonRoot"},
	})
	if err != nil {
		t.Fatalf("default-restoring removal must be allowed: %v", err)
	}
	if result.Template.Spec.Security.RunAsNonRoot == nil ||
		!*result.Template.Spec.Security.RunAsNonRoot {
		t.Fatal("default value was not restored")
	}
	if len(decisions) != 1 || decisions[0].Basis != BasisFrozenSecurity ||
		decisions[0].Verdict != VerdictAllow ||
		decisions[0].Detail == "" {
		t.Fatalf("decision must cite the frozen default basis: %+v", decisions)
	}
}

func TestAuthorizeDeletionToDifferentValueIsDenied(t *testing.T) {
	tmpl := baseTemplate()
	tmpl.Spec.Security.RunAsNonRoot = BoolPtr(false)
	approval, err := BindApproval("ap-false", tmpl, []string{"/spec/replicas"})
	if err != nil {
		t.Fatal(err)
	}
	registry, _ := newFieldRegistry(approval.MutableFields)

	_, _, err = Authorize(approval.Bound, registry, []PatchOperation{
		{Op: "remove", Path: "/spec/security/runAsNonRoot"},
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("removal that changes a bound value must be denied, got %v", err)
	}
}

func TestAuthorizeDigestPinnedImage(t *testing.T) {
	approval, registry := approvedFixture(t, "/spec/replicas")

	allowed, _, err := Authorize(approval.Bound, registry, []PatchOperation{
		{Op: "replace", Path: "/spec/image",
			Value: "registry.example.net/billing/worker:2.0.0@sha256:9c2b"},
	})
	if err != nil {
		t.Fatalf("same-digest retag must be allowed: %v", err)
	}
	if allowed.Image.Digest != approval.Bound.Image.Digest {
		t.Fatal("digest binding changed")
	}

	_, decisions, err := Authorize(approval.Bound, registry, []PatchOperation{
		{Op: "replace", Path: "/spec/image",
			Value: "registry.example.net/billing/worker:2.0.0@sha256:deadbeef"},
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("different digest must be denied, got %v", err)
	}
	if decisions[0].Basis != BasisDigestPinned || decisions[0].Verdict != VerdictDeny {
		t.Fatalf("digest basis missing: %+v", decisions[0])
	}

	_, _, err = Authorize(approval.Bound, registry, []PatchOperation{
		{Op: "replace", Path: "/spec/image",
			Value: "registry.example.net/other/repo@sha256:9c2b"},
	})
	if !errors.Is(err, errDenied) {
		t.Fatal("same digest on another repository must be denied")
	}
}

func TestAuthorizeMoveBetweenSecurityFieldsIsDenied(t *testing.T) {
	approval, registry := approvedFixture(t, "/spec/labels")

	// Moving an identity value into another identity slot attempts to carry an
	// unapproved identity: both ends are frozen, so the change must be denied.
	_, decisions, err := Authorize(approval.Bound, registry, []PatchOperation{
		{Op: "move",
			From: "/spec/identity/serviceAccountName",
			Path: "/spec/identity/groupNames/-"},
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("security-to-security move must be denied, got %v", err)
	}
	for _, decision := range decisions {
		if decision.Basis != BasisFrozenSecurity {
			t.Fatalf("every move end must cite the frozen binding: %+v", decision)
		}
	}
}

func TestAuthorizeMoveAllowedOnlyOnMutableFields(t *testing.T) {
	approval, registry := approvedFixture(t, "/spec/labels", "/metadata/labels")

	result, decisions, err := Authorize(approval.Bound, registry, []PatchOperation{
		{Op: "move", From: "/metadata/labels/team", Path: "/spec/labels/team"},
	})
	if err != nil {
		t.Fatalf("move between allow-listed maps must be allowed: %v", err)
	}
	if result.Template.Spec.Labels["team"] != "billing" {
		t.Fatal("moved label missing at destination")
	}
	for _, decision := range decisions {
		if decision.Verdict != VerdictAllow || decision.Basis != BasisMutableAllowList {
			t.Fatalf("both move ends must cite the allow-list: %+v", decision)
		}
	}
}

func TestAuthorizeMoveFromMutableToSecurityIsDenied(t *testing.T) {
	approval, registry := approvedFixture(t, "/spec/labels")

	_, _, err := Authorize(approval.Bound, registry, []PatchOperation{
		{Op: "move", From: "/spec/labels/tier", Path: "/spec/identity/serviceAccountName"},
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("move into a frozen field must be denied, got %v", err)
	}
}

func TestRegistryRejectsSecurityPathOnAllowList(t *testing.T) {
	if _, err := newFieldRegistry([]string{"/spec/security/privileged"}); !errors.Is(err, errMutableDenied) {
		t.Fatalf("expected errMutableDenied, got %v", err)
	}
	if _, err := newFieldRegistry([]string{"/spec/unknown"}); !errors.Is(err, errMutableUnknown) {
		t.Fatalf("expected errMutableUnknown, got %v", err)
	}
}
