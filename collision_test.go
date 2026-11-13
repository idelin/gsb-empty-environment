package netsec

import (
	"testing"
)

func TestDifferentialCollisionFindsShortestWeakKeyWitness(t *testing.T) {
	candidates := FieldCandidates{
		TenantID:           [2]string{"tenant-a", "tenant-b"},
		RoleID:             [2]string{"role-exact-id", "role-exact-id"},
		PolicyDigest:       [2]Digest{DigestBytes([]byte("policy-a")), DigestBytes([]byte("policy-b"))},
		SessionTagsDigest:  [2]Digest{DigestBytes([]byte("tags-a")), DigestBytes([]byte("tags-b"))},
		ExternalIdentityID: [2]string{"external-a", "external-b"},
		Audience:           [2]string{"audience-a", "audience-b"},
		ProviderGeneration: [2]uint64{1, 2},
	}

	weakRoleOnly := func(identity Identity) string { return identity.RoleID }
	collision, ok := FindDifferentialCollision(weakRoleOnly, candidates)
	if !ok {
		t.Fatal("expected a differential collision")
	}
	if collision.WeakKeyA != collision.WeakKeyB {
		t.Fatal("weak keys differ")
	}
	if collision.CanonicalKeyA == collision.CanonicalKeyB {
		t.Fatal("canonical keys are equal")
	}
	if collision.A.TenantID == collision.B.TenantID && collision.A.PolicyDigest == collision.B.PolicyDigest {
		t.Fatal("witness does not cross tenant or policy digest")
	}
	if len(collision.DifferingFields) != 1 {
		t.Fatalf("differing fields = %v, want a one-field shortest witness", collision.DifferingFields)
	}
	field := collision.DifferingFields[0]
	if field != "tenantID" && field != "policyDigest" {
		t.Fatalf("shortest witness changes %q, want tenantID or policyDigest", field)
	}
}

func TestDifferentialCollisionRejectsStrongCanonicalKey(t *testing.T) {
	candidates := FieldCandidates{
		TenantID:           [2]string{"tenant-a", "tenant-b"},
		RoleID:             [2]string{"role-a", "role-b"},
		PolicyDigest:       [2]Digest{DigestBytes([]byte("policy-a")), DigestBytes([]byte("policy-b"))},
		SessionTagsDigest:  [2]Digest{DigestBytes([]byte("tags-a")), DigestBytes([]byte("tags-b"))},
		ExternalIdentityID: [2]string{"external-a", "external-b"},
		Audience:           [2]string{"audience-a", "audience-b"},
		ProviderGeneration: [2]uint64{1, 2},
	}
	strong := func(identity Identity) string { return identity.CacheKey().String() }
	if collision, ok := FindDifferentialCollision(strong, candidates); ok {
		t.Fatalf("strong key produced a collision: %+v", collision)
	}
	if _, ok := FindDifferentialCollision(nil, candidates); ok {
		t.Fatal("nil weak key produced a collision")
	}
}

func TestDifferentialCollisionRequiresTenantOrPolicyDifference(t *testing.T) {
	policy := DigestBytes([]byte("same-policy"))
	candidates := FieldCandidates{
		TenantID:           [2]string{"tenant-a", "tenant-a"},
		RoleID:             [2]string{"role-a", "role-b"},
		PolicyDigest:       [2]Digest{policy, policy},
		SessionTagsDigest:  [2]Digest{DigestBytes([]byte("tags")), DigestBytes([]byte("tags"))},
		ExternalIdentityID: [2]string{"external", "external"},
		Audience:           [2]string{"audience", "audience"},
		ProviderGeneration: [2]uint64{1, 1},
	}
	weakConstant := func(Identity) string { return "constant" }
	if collision, ok := FindDifferentialCollision(weakConstant, candidates); ok {
		t.Fatalf("collision outside tenant/policy boundary accepted: %+v", collision)
	}
}
