package netsec

import "testing"

func intPtr(v int) *int { return &v }

func baseTemplate() WorkloadTemplate {
	return WorkloadTemplate{
		APIVersion: "autoscaling/v1",
		Kind:       "WorkloadTemplate",
		Metadata: ObjectMeta{
			Name:   "billing-worker",
			Labels: map[string]string{"team": "billing"},
		},
		Spec: WorkloadSpec{
			Image:    "registry.example.net/billing/worker:1.4.0@sha256:9c2b",
			Replicas: intPtr(3),
			Env: []EnvVar{
				{Name: "B", Value: "2"},
				{Name: "A", Value: "1"},
			},
			Labels: map[string]string{"tier": "batch"},
			Identity: Identity{
				ServiceAccountName: "billing-sa",
				GroupNames:         []string{"grp-z", "grp-a", "grp-z"},
			},
			Security: SecurityOptions{
				RunAsNonRoot:        BoolPtr(true),
				ReadOnlyRootFS:      BoolPtr(true),
				AllowPrivilegeEscal: BoolPtr(false),
				Privileged:          BoolPtr(false),
			},
			Capabilities: []string{"NET_BIND", "CAP_A", "CAP_A"},
		},
	}
}

func TestNormalizeSortsAndDedupes(t *testing.T) {
	normalized, err := NormalizeTemplate(baseTemplate())
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	got := normalized.Template.Spec
	if len(got.Env) != 2 || got.Env[0].Name != "A" || got.Env[1].Name != "B" {
		t.Fatalf("env not sorted: %+v", got.Env)
	}
	if len(got.Identity.GroupNames) != 2 ||
		got.Identity.GroupNames[0] != "grp-a" || got.Identity.GroupNames[1] != "grp-z" {
		t.Fatalf("group names not normalized: %+v", got.Identity.GroupNames)
	}
	if len(got.Capabilities) != 2 || got.Capabilities[0] != "CAP_A" || got.Capabilities[1] != "NET_BIND" {
		t.Fatalf("capabilities not normalized: %+v", got.Capabilities)
	}
}

func TestNormalizeFingerprintIsStableAndOrderIndependent(t *testing.T) {
	a, err := NormalizeTemplate(baseTemplate())
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	other := baseTemplate()
	other.Spec.Env = []EnvVar{{Name: "A", Value: "1"}, {Name: "B", Value: "2"}}
	other.Spec.Identity.GroupNames = []string{"grp-a", "grp-z"}
	b, err := NormalizeTemplate(other)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if a.Fingerprint != b.Fingerprint {
		t.Fatalf("fingerprints differ for equivalent templates: %s vs %s", a.Fingerprint, b.Fingerprint)
	}

	mutated := baseTemplate()
	mutated.Spec.Identity.ServiceAccountName = "other-sa"
	c, err := NormalizeTemplate(mutated)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if c.Fingerprint == a.Fingerprint {
		t.Fatal("identity change must change the fingerprint")
	}
}

func TestNormalizeMigratesLegacyAlias(t *testing.T) {
	tmpl := baseTemplate()
	raw, err := toMap(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	spec := raw["spec"].(map[string]any)
	delete(spec["identity"].(map[string]any), "serviceAccountName")
	spec["serviceAccount"] = "billing-sa"

	var migrated WorkloadTemplate
	if err := fromMap(raw, &migrated); err != nil {
		t.Fatal(err)
	}
	normalized, err := NormalizeTemplate(migrated)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if got := normalized.Raw["spec"].(map[string]any)["identity"].(map[string]any)["serviceAccountName"]; got != "billing-sa" {
		t.Fatalf("alias not migrated: %+v", got)
	}
	if _, exists := normalized.Raw["spec"].(map[string]any)["serviceAccount"]; exists {
		t.Fatal("legacy alias must be removed after migration")
	}

	baseline, err := NormalizeTemplate(baseTemplate())
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Fingerprint != baseline.Fingerprint {
		t.Fatal("alias migration should produce the same canonical template")
	}
}

func TestNormalizeRejectsConflictingAlias(t *testing.T) {
	tmpl := baseTemplate()
	raw, err := toMap(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	raw["spec"].(map[string]any)["serviceAccount"] = "different-sa"
	var conflicted WorkloadTemplate
	if err := fromMap(raw, &conflicted); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeTemplate(conflicted); err == nil {
		t.Fatal("conflicting service-account identities must fail normalization")
	}
}

func TestNormalizeMaterializesSecurityDefaults(t *testing.T) {
	tmpl := baseTemplate()
	tmpl.Spec.Identity.ServiceAccountName = ""
	tmpl.Spec.Security = SecurityOptions{}
	normalized, err := NormalizeTemplate(tmpl)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	security := normalized.Template.Spec.Security
	if normalized.Template.Spec.Identity.ServiceAccountName != defaultServiceAccount {
		t.Fatal("default service account not materialized")
	}
	if security.RunAsNonRoot == nil || !*security.RunAsNonRoot ||
		security.ReadOnlyRootFS == nil || !*security.ReadOnlyRootFS ||
		security.AllowPrivilegeEscal == nil || *security.AllowPrivilegeEscal ||
		security.Privileged == nil || *security.Privileged {
		t.Fatalf("security defaults not materialized: %+v", security)
	}
}

func TestNormalizeRejectsUnpinnedImage(t *testing.T) {
	tmpl := baseTemplate()
	tmpl.Spec.Image = "registry.example.net/billing/worker:1.4.0"
	if _, err := NormalizeTemplate(tmpl); err == nil {
		t.Fatal("an image without digest must be rejected")
	}
}
