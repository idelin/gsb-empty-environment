package netsec

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"
)

const (
	testIssuer   = "https://issuer.cluster-a.example"
	testAudience = "payments.svc.example"
	testCluster  = "cluster-a"
	testKeyID    = "issuer-key-1"
)

var (
	testNow   = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	testClock = func() time.Time { return testNow }
)

func testKeys(t *testing.T) (*rsa.PrivateKey, *StaticKeySet) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keys := NewStaticKeySet()
	keys.Add(testKeyID, &priv.PublicKey)
	return priv, keys
}

func newValidator(t *testing.T, keys KeySet, mutate func(*Config)) *Validator {
	t.Helper()
	cfg := Config{
		Issuer:           testIssuer,
		Audience:         testAudience,
		ClusterID:        testCluster,
		Keys:             keys,
		MaxTokenLifetime: time.Hour,
		Clock:            testClock,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return v
}

// validClaims returns a well-formed projected token claim set for the
// payments service; tests mutate copies to build attack scenarios.
func validClaims() *tokenClaims {
	aud := audienceClaim{testAudience}
	return &tokenClaims{
		Issuer:     testIssuer,
		Subject:    "system:serviceaccount:shop:checkout",
		Audience:   &aud,
		IssuedAt:   testNow.Add(-5 * time.Minute).Unix(),
		NotBefore:  testNow.Add(-5 * time.Minute).Unix(),
		ExpiresAt:  testNow.Add(30 * time.Minute).Unix(),
		TokenID:    "token-1",
		Cluster:    testCluster,
		Generation: 7,
		Kubernetes: &kubernetesClaims{
			Namespace:      "shop",
			ServiceAccount: serviceAccountRef{Name: "checkout", UID: "uid-123"},
		},
	}
}

func mint(t *testing.T, key *rsa.PrivateKey, claims *tokenClaims) string {
	t.Helper()
	token, err := signJWT("RS256", testKeyID, key, claims)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func validate(t *testing.T, v *Validator, token string) (*Identity, error) {
	t.Helper()
	return v.Validate(context.Background(), token)
}

func TestValidProjectedTokenPasses(t *testing.T) {
	priv, keys := testKeys(t)
	v := newValidator(t, keys, nil)

	id, err := validate(t, v, mint(t, priv, validClaims()))
	if err != nil {
		t.Fatalf("valid projected token rejected: %v", err)
	}
	if id.Namespace != "shop" || id.ServiceAccount != "checkout" || id.ServiceAccountUID != "uid-123" {
		t.Errorf("unexpected identity: %+v", id)
	}
	if id.Generation != 7 {
		t.Errorf("generation = %d, want 7", id.Generation)
	}
	if id.Legacy {
		t.Error("projected token flagged as legacy")
	}
	if len(id.Audience) != 1 || id.Audience[0] != testAudience {
		t.Errorf("audience = %v, want [%s]", id.Audience, testAudience)
	}
}

func TestWrongAudienceRejected(t *testing.T) {
	priv, keys := testKeys(t)
	v := newValidator(t, keys, nil)

	// Token minted for the monitoring service, replayed against payments.
	claims := validClaims()
	aud := audienceClaim{"monitoring.svc.example"}
	claims.Audience = &aud

	if _, err := validate(t, v, mint(t, priv, claims)); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("err = %v, want ErrAudienceMismatch", err)
	}
}

func TestCrossServiceReplayWithExtraAudienceRejected(t *testing.T) {
	priv, keys := testKeys(t)
	v := newValidator(t, keys, nil)

	// Even a token that lists this service among several audiences is not
	// scoped to this service alone and must not pass.
	claims := validClaims()
	aud := audienceClaim{testAudience, "monitoring.svc.example"}
	claims.Audience = &aud

	if _, err := validate(t, v, mint(t, priv, claims)); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("err = %v, want ErrAudienceMismatch", err)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	priv, keys := testKeys(t)
	v := newValidator(t, keys, nil)

	claims := validClaims()
	claims.IssuedAt = testNow.Add(-2 * time.Hour).Unix()
	claims.NotBefore = claims.IssuedAt
	claims.ExpiresAt = testNow.Add(-time.Minute).Unix()

	if _, err := validate(t, v, mint(t, priv, claims)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
}

func TestForgedIssuerRejected(t *testing.T) {
	priv, keys := testKeys(t)
	v := newValidator(t, keys, nil)

	// Attacker signs a token with a key they control and claims the trusted
	// issuer: the unknown key id fails signature verification.
	attackerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate attacker key: %v", err)
	}
	forged, err := signJWT("RS256", "attacker-key", attackerKey, validClaims())
	if err != nil {
		t.Fatalf("sign forged token: %v", err)
	}
	if _, err := validate(t, v, forged); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("err = %v, want ErrInvalidSignature", err)
	}

	// Attacker reuses the trusted key id but lies about the issuer.
	claims := validClaims()
	claims.Issuer = "https://evil.example"
	if _, err := validate(t, v, mint(t, priv, claims)); !errors.Is(err, ErrIssuerMismatch) {
		t.Fatalf("err = %v, want ErrIssuerMismatch", err)
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	priv, keys := testKeys(t)
	v := newValidator(t, keys, nil)

	claims := validClaims()
	token := mint(t, priv, claims)
	// Flip a payload character without re-signing.
	tampered := token[:len(token)/2] + "A" + token[len(token)/2+1:]
	if _, err := validate(t, v, tampered); !errors.Is(err, ErrInvalidSignature) && !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("err = %v, want signature or malformed error", err)
	}
}

func TestCrossClusterTokenRejected(t *testing.T) {
	priv, keys := testKeys(t)
	v := newValidator(t, keys, nil)

	claims := validClaims()
	claims.Cluster = "cluster-b"
	if _, err := validate(t, v, mint(t, priv, claims)); !errors.Is(err, ErrClusterMismatch) {
		t.Fatalf("err = %v, want ErrClusterMismatch", err)
	}

	claims = validClaims()
	claims.Cluster = ""
	if _, err := validate(t, v, mint(t, priv, claims)); !errors.Is(err, ErrClusterMismatch) {
		t.Fatalf("err = %v, want ErrClusterMismatch for unbound token", err)
	}
}

func TestMirroredTokenRejected(t *testing.T) {
	priv, keys := testKeys(t)
	v := newValidator(t, keys, nil)

	claims := validClaims()
	claims.Mirrored = true
	if _, err := validate(t, v, mint(t, priv, claims)); !errors.Is(err, ErrMirroredToken) {
		t.Fatalf("err = %v, want ErrMirroredToken", err)
	}
}

func TestRevokedTokenRejected(t *testing.T) {
	priv, keys := testKeys(t)
	store := NewInMemoryRevocationStore()
	v := newValidator(t, keys, func(c *Config) { c.Revocations = store })

	claims := validClaims()
	token := mint(t, priv, claims)

	if _, err := validate(t, v, token); err != nil {
		t.Fatalf("token rejected before revocation: %v", err)
	}
	store.Revoke("token-1")
	if _, err := validate(t, v, token); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("err = %v, want ErrTokenRevoked", err)
	}
}

func TestStaleGenerationRejected(t *testing.T) {
	priv, keys := testKeys(t)
	store := NewInMemoryRevocationStore()
	v := newValidator(t, keys, func(c *Config) { c.Revocations = store })

	claims := validClaims() // generation 7
	token := mint(t, priv, claims)

	if _, err := validate(t, v, token); err != nil {
		t.Fatalf("token rejected before generation bump: %v", err)
	}
	// Permissions for shop/checkout were re-granted; generation 8+ required.
	store.SetMinGeneration("shop", "checkout", 8)
	if _, err := validate(t, v, token); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("err = %v, want ErrTokenRevoked", err)
	}
}

func TestLegacyTokenMigrationPolicy(t *testing.T) {
	priv, keys := testKeys(t)

	legacyClaims := func() *tokenClaims {
		c := validClaims()
		c.Audience = nil // pre-projected format: no audience claim
		return c
	}

	// Default policy: rejected.
	v := newValidator(t, keys, nil)
	if _, err := validate(t, v, mint(t, priv, legacyClaims())); !errors.Is(err, ErrLegacyToken) {
		t.Fatalf("default policy: err = %v, want ErrLegacyToken", err)
	}

	// Explicit migration policy: admitted and flagged.
	v = newValidator(t, keys, func(c *Config) { c.LegacyPolicy = LegacyAllow })
	id, err := validate(t, v, mint(t, priv, legacyClaims()))
	if err != nil {
		t.Fatalf("LegacyAllow: token rejected: %v", err)
	}
	if !id.Legacy {
		t.Error("LegacyAllow: identity not flagged as legacy")
	}

	// Migration window expired: rejected again.
	v = newValidator(t, keys, func(c *Config) {
		c.LegacyPolicy = LegacyAllow
		c.LegacyNotAfter = testNow.Add(-time.Minute)
	})
	if _, err := validate(t, v, mint(t, priv, legacyClaims())); !errors.Is(err, ErrLegacyToken) {
		t.Fatalf("expired window: err = %v, want ErrLegacyToken", err)
	}
}

func TestExcessiveLifetimeRejected(t *testing.T) {
	priv, keys := testKeys(t)
	v := newValidator(t, keys, nil) // MaxTokenLifetime = 1h

	claims := validClaims()
	claims.ExpiresAt = testNow.Add(24 * time.Hour).Unix()
	if _, err := validate(t, v, mint(t, priv, claims)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired for over-long expiry", err)
	}
}

func TestMissingIdentityClaimsRejected(t *testing.T) {
	priv, keys := testKeys(t)
	v := newValidator(t, keys, nil)

	claims := validClaims()
	claims.Kubernetes = nil
	if _, err := validate(t, v, mint(t, priv, claims)); !errors.Is(err, ErrMissingIdentity) {
		t.Fatalf("err = %v, want ErrMissingIdentity", err)
	}
}

func TestNotBeforeEnforced(t *testing.T) {
	priv, keys := testKeys(t)
	v := newValidator(t, keys, nil)

	claims := validClaims()
	claims.NotBefore = testNow.Add(10 * time.Minute).Unix()
	if _, err := validate(t, v, mint(t, priv, claims)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired for future nbf", err)
	}
}

func TestMalformedTokenRejected(t *testing.T) {
	_, keys := testKeys(t)
	v := newValidator(t, keys, nil)

	if _, err := validate(t, v, "not-a-jwt"); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("err = %v, want ErrMalformedToken", err)
	}
}

func TestNewValidatorRequiresTrustAnchor(t *testing.T) {
	if _, err := NewValidator(Config{}); err == nil {
		t.Fatal("empty config accepted")
	}
	_, keys := testKeys(t)
	if _, err := NewValidator(Config{Issuer: testIssuer, Audience: testAudience, Keys: keys}); err != nil {
		t.Fatalf("complete config rejected: %v", err)
	}
}
