package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
)

func TestIdentityCacheKeyIncludesEveryField(t *testing.T) {
	base := Identity{
		TenantID:           "tenant-a",
		RoleID:             "role-exact-id",
		PolicyDigest:       DigestBytes([]byte("policy-a")),
		SessionTagsDigest:  SessionTagsDigest([]Tag{{Key: "env", Value: "prod"}}),
		ExternalIdentityID: "external-1",
		Audience:           "sts.example",
		ProviderGeneration: 7,
	}
	baseKey := base.CacheKey()

	mutations := []struct {
		name   string
		mutate func(*Identity)
	}{
		{name: "tenant", mutate: func(i *Identity) { i.TenantID = "tenant-b" }},
		{name: "role", mutate: func(i *Identity) { i.RoleID = "role-name" }},
		{name: "policy", mutate: func(i *Identity) { i.PolicyDigest = DigestBytes([]byte("policy-b")) }},
		{name: "tags", mutate: func(i *Identity) { i.SessionTagsDigest = DigestBytes([]byte("tags-b")) }},
		{name: "external identity", mutate: func(i *Identity) { i.ExternalIdentityID = "external-2" }},
		{name: "audience", mutate: func(i *Identity) { i.Audience = "other.example" }},
		{name: "generation", mutate: func(i *Identity) { i.ProviderGeneration = 8 }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			changed := base
			tc.mutate(&changed)
			if changed.CacheKey() == baseKey {
				t.Fatalf("changing %s did not change the cache key", tc.name)
			}
		})
	}
}

func TestIdentityEncodingUsesFixedTagsAndBigEndianLengths(t *testing.T) {
	identity := Identity{
		TenantID:           "tenant-a",
		RoleID:             "role-id",
		PolicyDigest:       DigestBytes([]byte("policy")),
		SessionTagsDigest:  DigestBytes([]byte("tags")),
		ExternalIdentityID: "external-id",
		Audience:           "audience",
		ProviderGeneration: 0x0102030405060708,
	}

	var expected []byte
	field := func(tag string, value []byte) {
		expected = binary.BigEndian.AppendUint64(expected, uint64(len(tag)))
		expected = append(expected, tag...)
		expected = binary.BigEndian.AppendUint64(expected, uint64(len(value)))
		expected = append(expected, value...)
	}
	field("domain", []byte(cacheKeyDomain))
	field("tenant-id", []byte(identity.TenantID))
	field("role-id", []byte(identity.RoleID))
	field("policy-digest", identity.PolicyDigest[:])
	field("session-tags-digest", identity.SessionTagsDigest[:])
	field("external-identity-id", []byte(identity.ExternalIdentityID))
	field("audience", []byte(identity.Audience))
	field("provider-generation", []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08})

	if !bytes.Equal(identity.CanonicalBytes(), expected) {
		t.Fatalf("canonical encoding does not match fixed-tag, big-endian length format")
	}
	if got, want := identity.CacheKey(), Digest(sha256.Sum256(expected)); got != want {
		t.Fatalf("CacheKey = %s, want %s", got, want)
	}
}

func TestIdentityLengthEncodingAvoidsConcatenationAmbiguity(t *testing.T) {
	a := testIdentity("ab", "c")
	b := testIdentity("a", "bc")
	if a.CacheKey() == b.CacheKey() {
		t.Fatal("field boundaries were not length encoded")
	}
}

func TestIdentityValidationRejectsEmptySecurityFields(t *testing.T) {
	base := testIdentity("tenant", "role")
	for _, tc := range []struct {
		name   string
		mutate func(*Identity)
	}{
		{name: "tenant", mutate: func(i *Identity) { i.TenantID = "" }},
		{name: "role", mutate: func(i *Identity) { i.RoleID = "" }},
		{name: "policy digest", mutate: func(i *Identity) { i.PolicyDigest = Digest{} }},
		{name: "tag digest", mutate: func(i *Identity) { i.SessionTagsDigest = Digest{} }},
		{name: "external identity", mutate: func(i *Identity) { i.ExternalIdentityID = "" }},
		{name: "audience", mutate: func(i *Identity) { i.Audience = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity := base
			tc.mutate(&identity)
			if err := identity.Validate(); err == nil {
				t.Fatalf("empty %s identity validated", tc.name)
			}
		})
	}
}

func TestSessionTagsDigestIsCanonical(t *testing.T) {
	a := []Tag{
		{Key: "z", Value: "last"},
		{Key: "a", Value: "first"},
		{Key: "a", Value: "second"},
	}
	b := []Tag{
		{Key: "a", Value: "first"},
		{Key: "a", Value: "second"},
		{Key: "z", Value: "last"},
	}
	c := []Tag{
		{Key: "a", Value: "second"},
		{Key: "z", Value: "last"},
		{Key: "a", Value: "first"},
	}
	want := SessionTagsDigest(a)
	if got := SessionTagsDigest(b); got != want {
		t.Fatal("tag order changed digest")
	}
	if got := SessionTagsDigest(c); got != want {
		t.Fatal("same-key tag order changed digest")
	}
	if got := SessionTagsDigestFromMap(map[string]string{"a": "first", "z": "last"}); got == want {
		t.Fatal("different tag sets produced the same digest")
	}
}

func TestDigestTextRoundTrip(t *testing.T) {
	digest := DigestBytes([]byte("value"))
	parsed, err := ParseDigest(digest.String())
	if err != nil {
		t.Fatalf("ParseDigest: %v", err)
	}
	if parsed != digest {
		t.Fatalf("round trip = %s, want %s", parsed, digest)
	}
	if _, err := ParseDigest("not-hex"); err == nil {
		t.Fatal("invalid digest accepted")
	}
	var zero Digest
	if !zero.IsZero() {
		t.Fatal("zero digest not recognized")
	}
}

func TestSecretLeaseZeroizesCloneOnRelease(t *testing.T) {
	master := NewMemorySecret("master", []byte("top-secret-material"))
	cloneHandle, err := master.Clone()
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	clone := cloneHandle.(*MemorySecret)
	lease := &Lease{identity: testIdentity("tenant", "role"), secret: clone}

	if got := mustReveal(t, lease.Secret()); got != "top-secret-material" {
		t.Fatalf("lease secret = %q", got)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !lease.Released() {
		t.Fatal("lease not marked released")
	}
	if _, err := clone.Reveal(); !errors.Is(err, ErrSecretDestroyed) {
		t.Fatalf("Reveal after release error = %v", err)
	}
	state := clone.State()
	if !state.Destroyed || !state.Zeroed {
		t.Fatalf("clone state after release = %+v", state)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	if master.State().Destroyed {
		t.Fatal("releasing a lease destroyed the cache's master handle")
	}
}
