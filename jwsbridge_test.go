package jwsbridge

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func fixedClock() func() time.Time { return func() time.Time { return testNow } }

const (
	testTenant       = "tenant-a"
	delegationIssuer = "idp.delegation"
	subjectIssuer    = "idp.subject"
	brokerAudience   = "broker.local"
	localAudience    = "workload.local"
)

// fixture wires a full broker with one delegation signer and one subject
// signer registered in the key store.
type fixture struct {
	keys       *KeyStore
	verifier   *Verifier
	handler    *ExchangeHandler
	local      *Signer
	delegation *Signer
	subject    *Signer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	must := func(s *Signer, err error) *Signer {
		t.Helper()
		if err != nil {
			t.Fatalf("NewSigner: %v", err)
		}
		return s
	}
	f := &fixture{
		keys:       NewKeyStore(),
		local:      must(NewSigner(testTenant, "local-issuer", KeyUseSign, 1)),
		delegation: must(NewSigner(testTenant, "delegation-key", KeyUseSign, 1)),
		subject:    must(NewSigner(testTenant, "subject-key", KeyUseSign, 1)),
	}
	for _, rec := range []KeyRecord{f.local.Record(), f.delegation.Record(), f.subject.Record()} {
		if err := f.keys.Register(testTenant, rec); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	f.verifier = NewVerifier(f.keys, fixedClock())
	f.handler = NewExchangeHandler(f.verifier, f.local, ExchangeConfig{
		Tenant:           testTenant,
		DelegationIssuer: delegationIssuer,
		SubjectIssuer:    subjectIssuer,
		Audience:         brokerAudience,
		LocalAudience:    localAudience,
		TTL:              5 * time.Minute,
	}, NewReplayCache(fixedClock()), fixedClock())
	return f
}

func (f *fixture) tokenPair(t *testing.T, jtiSuffix string, delScopes, subScopes []string) (string, string) {
	t.Helper()
	exp := testNow.Add(time.Hour)
	del, err := f.delegation.Sign(Claims{
		Issuer: delegationIssuer, Subject: "admin",
		Audience: []string{brokerAudience}, Scopes: delScopes,
		ID: "del-" + jtiSuffix, ExpiresAt: exp,
	})
	if err != nil {
		t.Fatalf("sign delegation: %v", err)
	}
	sub, err := f.subject.Sign(Claims{
		Issuer: subjectIssuer, Subject: "alice",
		Audience: []string{brokerAudience}, Scopes: subScopes,
		ID: "sub-" + jtiSuffix, ExpiresAt: exp,
	})
	if err != nil {
		t.Fatalf("sign subject: %v", err)
	}
	return del, sub
}

func TestExchangeIssuesIntersectedScope(t *testing.T) {
	f := newFixture(t)
	del, sub := f.tokenPair(t, "1", []string{"read", "write", "admin"}, []string{"read", "write"})

	out, err := f.handler.Exchange(del, sub)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	claims, err := f.verifier.Verify(out, VerifyPolicy{
		Tenant:   testTenant,
		Issuer:   testTenant + "/broker",
		Audience: localAudience,
	})
	if err != nil {
		t.Fatalf("verify issued credential: %v", err)
	}
	if claims.Subject != "alice" {
		t.Fatalf("subject = %q, want alice", claims.Subject)
	}
	if want := []string{"read", "write"}; !reflect.DeepEqual(claims.Scopes, want) {
		t.Fatalf("scopes = %v, want intersection %v", claims.Scopes, want)
	}
	if ttl := claims.ExpiresAt.Sub(testNow); ttl != 5*time.Minute {
		t.Fatalf("ttl = %v, want 5m short-lived credential", ttl)
	}
}

func TestSameKidDifferentKeysAcrossRotation(t *testing.T) {
	f := newFixture(t)

	// Rotate the delegation key but reuse the same kid: gen 1 -> gen 2.
	rotated, err := NewSigner(testTenant, "delegation-key", KeyUseSign, 2)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	if err := f.keys.Register(testTenant, rotated.Record()); err != nil {
		t.Fatalf("Register rotated key: %v", err)
	}

	// Tokens minted by the retired key (same kid, gen 1) must now fail.
	staleDel, staleSub := f.tokenPair(t, "rot", []string{"read"}, []string{"read"})
	if _, err := f.handler.Exchange(staleDel, staleSub); !errors.Is(err, ErrSignature) {
		t.Fatalf("stale key exchange err = %v, want ErrSignature", err)
	}

	// Tokens minted by the current generation verify fine.
	f.delegation = rotated
	del, sub := f.tokenPair(t, "rot2", []string{"read"}, []string{"read"})
	if _, err := f.handler.Exchange(del, sub); err != nil {
		t.Fatalf("rotated key exchange: %v", err)
	}

	// The cache distinguishes generations: only the current generation of
	// the reused kid remains cached.
	for _, key := range f.keys.CacheKeys() {
		if key == testTenant+"/delegation-key#1" {
			t.Fatalf("stale generation still cached: %v", f.keys.CacheKeys())
		}
	}
}

func TestCacheIsolatesTenants(t *testing.T) {
	f := newFixture(t)
	other, err := NewSigner("tenant-b", "delegation-key", KeyUseSign, 1)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	if err := f.keys.Register("tenant-b", other.Record()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := f.keys.Resolve("tenant-a", "delegation-key"); err != nil {
		t.Fatalf("Resolve tenant-a: %v", err)
	}
	if _, err := f.keys.Resolve("tenant-b", "delegation-key"); err != nil {
		t.Fatalf("Resolve tenant-b: %v", err)
	}
	got := f.keys.CacheKeys()
	sort.Strings(got)
	want := []string{"tenant-a/delegation-key#1", "tenant-b/delegation-key#1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cache keys = %v, want %v", got, want)
	}

	// A token from tenant-b's signer must not verify under tenant-a's
	// policy even though the kid matches.
	tok, err := other.Sign(Claims{
		Issuer: delegationIssuer, Subject: "mallory",
		Audience: []string{brokerAudience}, Scopes: []string{"read"},
		ID: "x1", ExpiresAt: testNow.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	_, err = f.verifier.Verify(tok, VerifyPolicy{
		Tenant: testTenant, Issuer: delegationIssuer, Audience: brokerAudience,
	})
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("cross-tenant verify err = %v, want ErrTenantMismatch", err)
	}
}

func TestAudienceSwapRejected(t *testing.T) {
	f := newFixture(t)

	// Subject token addressed to another service, not the broker.
	wrongAud, err := f.subject.Sign(Claims{
		Issuer: subjectIssuer, Subject: "alice",
		Audience: []string{"payments-api"}, Scopes: []string{"read"},
		ID: "aud-1", ExpiresAt: testNow.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	del, _ := f.tokenPair(t, "aud", []string{"read"}, []string{"read"})
	if _, err := f.handler.Exchange(del, wrongAud); !errors.Is(err, ErrAudience) {
		t.Fatalf("foreign audience err = %v, want ErrAudience", err)
	}

	// Swapping the two tokens: the delegation token fails the subject
	// issuer check and vice versa.
	del2, sub2 := f.tokenPair(t, "aud2", []string{"read"}, []string{"read"})
	if _, err := f.handler.Exchange(sub2, del2); !errors.Is(err, ErrIssuer) {
		t.Fatalf("swapped tokens err = %v, want ErrIssuer", err)
	}
}

func TestReplayRedemptionRejected(t *testing.T) {
	f := newFixture(t)
	del, sub := f.tokenPair(t, "replay", []string{"read"}, []string{"read"})

	if _, err := f.handler.Exchange(del, sub); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	if _, err := f.handler.Exchange(del, sub); !errors.Is(err, ErrReplayedToken) {
		t.Fatalf("replayed exchange err = %v, want ErrReplayedToken", err)
	}

	// Fresh token IDs still redeem normally.
	del2, sub2 := f.tokenPair(t, "replay2", []string{"read"}, []string{"read"})
	if _, err := f.handler.Exchange(del2, sub2); err != nil {
		t.Fatalf("fresh exchange after replay: %v", err)
	}
}

func TestKeyUseEnforced(t *testing.T) {
	f := newFixture(t)
	encSigner, err := NewSigner(testTenant, "enc-key", KeyUseEncrypt, 1)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	if err := f.keys.Register(testTenant, encSigner.Record()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	tok, err := encSigner.Sign(Claims{
		Issuer: subjectIssuer, Subject: "alice",
		Audience: []string{brokerAudience}, Scopes: []string{"read"},
		ID: "enc-1", ExpiresAt: testNow.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	_, err = f.verifier.Verify(tok, VerifyPolicy{
		Tenant: testTenant, Issuer: subjectIssuer, Audience: brokerAudience,
	})
	if !errors.Is(err, ErrKeyUse) {
		t.Fatalf("encryption-key token err = %v, want ErrKeyUse", err)
	}
}

func TestEmptyScopeIntersectionRejected(t *testing.T) {
	f := newFixture(t)
	del, sub := f.tokenPair(t, "scope", []string{"read"}, []string{"delete"})
	if _, err := f.handler.Exchange(del, sub); !errors.Is(err, ErrEmptyScopeGrant) {
		t.Fatalf("err = %v, want ErrEmptyScopeGrant", err)
	}
}
