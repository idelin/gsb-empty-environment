package nestedjwt_test

import (
	"crypto"
	"errors"
	"strings"
	"testing"

	"formal.local/w28-5b20ffbc87/nestedjwt"
)

type stubReplayStore struct {
	seenKeys []string
	seen     map[string]bool
	fail     error
}

func (s *stubReplayStore) Seen(issuer, jti string) (bool, error) {
	if s.fail != nil {
		return false, s.fail
	}
	key := issuer + "/" + jti
	s.seenKeys = append(s.seenKeys, key)
	if s.seen[key] {
		return true, nil
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	s.seen[key] = true
	return false, nil
}

func TestCustomReplayStore(t *testing.T) {
	serverKey := mustRSAKey()
	clientEC := mustECKey()
	store := nestedjwt.MapKeyStore{
		DecryptionKeys: map[string]crypto.PrivateKey{serverKID: serverKey},
		SigningKeys:    map[string]crypto.PublicKey{clientKID: &clientEC.PublicKey},
	}
	replay := &stubReplayStore{}
	verifier := nestedjwt.New(store, nestedjwt.WithReplayStore(replay))

	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-store"), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); err != nil {
		t.Fatalf("first verification: %v", err)
	}
	if len(replay.seenKeys) != 1 || replay.seenKeys[0] != "issuer-a/jti-store" {
		t.Fatalf("replay store not invoked correctly: %v", replay.seenKeys)
	}
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrReplayed) {
		t.Fatalf("custom store replay: expected ErrReplayed, got %v", err)
	}
}

func TestReplayStoreFailureSurfaces(t *testing.T) {
	serverKey := mustRSAKey()
	clientEC := mustECKey()
	store := nestedjwt.MapKeyStore{
		DecryptionKeys: map[string]crypto.PrivateKey{serverKID: serverKey},
		SigningKeys:    map[string]crypto.PublicKey{clientKID: &clientEC.PublicKey},
	}
	boom := errors.New("redis unavailable")
	verifier := nestedjwt.New(store, nestedjwt.WithReplayStore(&stubReplayStore{fail: boom}))

	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-down"), defaultJWEOptions(serverKID))
	_, err := verifier.Verify(token)
	if err == nil || !strings.Contains(err.Error(), "replay store failure") {
		t.Fatalf("expected replay store failure to surface, got %v", err)
	}
}

func TestReplayClaimsCanBeRelaxed(t *testing.T) {
	policy := nestedjwt.DefaultPolicy()
	policy.RequireReplayClaims = false
	policy.EnableReplayStore = false
	verifier, serverKey, clientEC := newHarness(t, nestedjwt.WithPolicy(policy))

	claims := sampleClaims("issuer-a", "jti-relaxed")
	delete(claims, "jti")
	token := buildNested(serverKey, clientEC, claims, defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); err != nil {
		t.Fatalf("relaxed policy should accept token without jti: %v", err)
	}
}

func TestMalformedJWESegments(t *testing.T) {
	verifier, serverKey, clientEC := newHarness(t)
	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-badseg"), defaultJWEOptions(serverKID))

	segments := strings.Split(token, ".")

	ivBroken := append([]string(nil), segments...)
	ivBroken[2] = "@@@"
	if _, err := verifier.Verify(strings.Join(ivBroken, ".")); !errors.Is(err, nestedjwt.ErrMalformedToken) {
		t.Fatalf("bad IV: expected ErrMalformedToken, got %v", err)
	}

	tagBroken := append([]string(nil), segments...)
	tagBroken[4] = "@@@"
	if _, err := verifier.Verify(strings.Join(tagBroken, ".")); !errors.Is(err, nestedjwt.ErrMalformedToken) {
		t.Fatalf("bad tag: expected ErrMalformedToken, got %v", err)
	}

	emptyIV := append([]string(nil), segments...)
	emptyIV[2] = ""
	if _, err := verifier.Verify(strings.Join(emptyIV, ".")); !errors.Is(err, nestedjwt.ErrMalformedToken) {
		t.Fatalf("empty IV: expected ErrMalformedToken, got %v", err)
	}
}

func TestWrongKeyTypeForDecryption(t *testing.T) {
	clientEC := mustECKey()
	// Register an EC key where the RSA decryption key is required.
	store := nestedjwt.MapKeyStore{
		DecryptionKeys: map[string]crypto.PrivateKey{serverKID: clientEC},
		SigningKeys:    map[string]crypto.PublicKey{clientKID: &clientEC.PublicKey},
	}
	verifier := nestedjwt.New(store)
	serverKey := mustRSAKey()
	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-keytype"), defaultJWEOptions(serverKID))

	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrSignature) {
		t.Fatalf("EC key used for RSA-OAEP: expected ErrSignature, got %v", err)
	}
}

func TestAudienceJSONRoundTrip(t *testing.T) {
	single := nestedjwt.Audience{"only-one"}
	raw, err := single.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal single: %v", err)
	}
	if string(raw) != `"only-one"` {
		t.Fatalf("single audience should encode as string, got %s", raw)
	}

	multi := nestedjwt.Audience{"a", "b"}
	raw, err = multi.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal multi: %v", err)
	}
	var decoded nestedjwt.Audience
	if err := decoded.UnmarshalJSON(raw); err != nil {
		t.Fatalf("unmarshal multi: %v", err)
	}
	if len(decoded) != 2 || decoded[0] != "a" || decoded[1] != "b" {
		t.Fatalf("audience round trip failed: %v", decoded)
	}

	if err := decoded.UnmarshalJSON([]byte("123")); err == nil {
		t.Fatal("invalid audience encoding must error")
	}
}
