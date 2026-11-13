package netsec

import (
	"testing"
	"time"
)

func TestCanonicalDecisionBindsAuthorizationFields(t *testing.T) {
	base := Decision{
		RequestID:                "request-1",
		PrincipalID:              "platform",
		TenantID:                 "tenant-a",
		RoleID:                   "role-a",
		ExternalID:               "external-a",
		SourceResource:           "resource-a",
		ParametersDigest:         "params-a",
		RequestedDurationSeconds: 600,
		EffectiveDurationSeconds: 600,
		PolicyDigest:             "policy-a",
		TenantGeneration:         3,
		Audience:                 "tenant-a",
		SourceIdentity:           "platform",
		SessionID:                "session-a",
		NotAfter:                 time.Unix(1_800_000_000, 0).UTC(),
		Allowed:                  true,
	}
	digest := base.RecomputeDigest()
	if digest == "" {
		t.Fatal("empty digest")
	}
	if again := base.RecomputeDigest(); again != digest {
		t.Fatal("decision digest is not deterministic")
	}

	mutations := map[string]func(*Decision){
		"request ID":         func(d *Decision) { d.RequestID = "request-2" },
		"principal":          func(d *Decision) { d.PrincipalID = "other-platform" },
		"tenant":             func(d *Decision) { d.TenantID = "tenant-b" },
		"role":               func(d *Decision) { d.RoleID = "role-b" },
		"external ID":        func(d *Decision) { d.ExternalID = "external-b" },
		"source resource":    func(d *Decision) { d.SourceResource = "resource-b" },
		"parameters digest":  func(d *Decision) { d.ParametersDigest = "params-b" },
		"requested duration": func(d *Decision) { d.RequestedDurationSeconds++ },
		"effective duration": func(d *Decision) { d.EffectiveDurationSeconds++ },
		"policy digest":      func(d *Decision) { d.PolicyDigest = "policy-b" },
		"generation":         func(d *Decision) { d.TenantGeneration++ },
		"audience":           func(d *Decision) { d.Audience = "audience-b" },
		"source identity":    func(d *Decision) { d.SourceIdentity = "source-b" },
		"session ID":         func(d *Decision) { d.SessionID = "session-b" },
		"expiration cap":     func(d *Decision) { d.NotAfter = d.NotAfter.Add(time.Second) },
		"outcome":            func(d *Decision) { d.Allowed = false },
		"deny code":          func(d *Decision) { d.DenyCode = "role_mismatch" },
	}
	for name, mutate := range mutations {
		changed := base
		mutate(&changed)
		if changed.RecomputeDigest() == digest {
			t.Fatalf("%s did not change the canonical decision digest", name)
		}
	}

	auditOnly := base
	auditOnly.SessionName = "different-session-name"
	if auditOnly.RecomputeDigest() != digest {
		t.Fatal("session name changed the authorization decision digest")
	}
}

func TestConstantTimeStringEquality(t *testing.T) {
	if !constantTimeStringEqual("same-value", "same-value") {
		t.Fatal("equal strings compared unequal")
	}
	if constantTimeStringEqual("same-value", "same-valuf") {
		t.Fatal("different strings compared equal")
	}
	if constantTimeStringEqual("short", "much-longer-value") {
		t.Fatal("different length strings compared equal")
	}
}
