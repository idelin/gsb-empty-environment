package netsec_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0758"
)

const (
	testIssuer   = "https://issuer.example"
	testAudience = "client-0758"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
	keyErr  error
)

func signingKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		testKey, keyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if keyErr != nil {
		t.Fatalf("generate RSA key: %v", keyErr)
	}
	return testKey
}

func newValidator(t *testing.T, clock func() time.Time, cache *netsec.ReplayCache) *netsec.Validator {
	t.Helper()
	key := signingKey(t)
	return newValidatorForKey(t, clock, cache, &key.PublicKey, "test-key")
}

func newValidatorForKey(t *testing.T, clock func() time.Time, cache *netsec.ReplayCache, verificationKey any, keyID string) *netsec.Validator {
	t.Helper()
	validator, err := netsec.NewValidator(netsec.Config{
		Issuer:      testIssuer,
		Audience:    testAudience,
		ReplayCache: cache,
		Clock:       clock,
		KeyFunc: func(_ context.Context, token *netsec.Token) (any, error) {
			if token.Header.KeyID != keyID {
				return nil, fmt.Errorf("unknown kid %q", token.Header.KeyID)
			}
			return verificationKey, nil
		},
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return validator
}

func signRS256(t *testing.T, key *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	return signWith(t, header, claims, func(signingInput []byte) []byte {
		digest := sha256.Sum256(signingInput)
		signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatalf("sign token: %v", err)
		}
		return signature
	})
}

func signWith(t *testing.T, header, claims map[string]any, sign func([]byte) []byte) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := sign([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func logoutHeader() map[string]any {
	return map[string]any{
		"alg": "RS256",
		"typ": "logout+jwt",
		"kid": "test-key",
	}
}

func claimsAt(now time.Time, tokenID string) map[string]any {
	return map[string]any{
		"iss": testIssuer,
		"aud": testAudience,
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
		"jti": tokenID,
		"sub": "user-1",
		"sid": "session-1",
		"events": map[string]any{
			netsec.EventBackchannelLogout: map[string]any{},
		},
	}
}

func TestValidateSessionScopedLogoutToken(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)
	token := signRS256(t, signingKey(t), logoutHeader(), claimsAt(now, "jti-session"))

	result, err := validator.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if result.Scope() != netsec.TerminateSession {
		t.Fatalf("Scope() = %v, want %v", result.Scope(), netsec.TerminateSession)
	}
	if result.Subject != "user-1" || result.SessionID != "session-1" {
		t.Fatalf("unexpected subject/session: %q/%q", result.Subject, result.SessionID)
	}
	if result.TokenID != "jti-session" {
		t.Fatalf("TokenID = %q", result.TokenID)
	}
}

func TestRejectIDTokenImpostor(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	header := logoutHeader()
	header["typ"] = "JWT"
	claims := claimsAt(now, "id-token-jti")
	delete(claims, "sid")
	delete(claims, "events")

	token := signRS256(t, signingKey(t), header, claims)
	_, err := validator.Validate(context.Background(), token)
	if !errors.Is(err, netsec.ErrInvalidTokenType) {
		t.Fatalf("Validate error = %v, want ErrInvalidTokenType", err)
	}
}

func TestRejectNonceInLogoutToken(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	tests := []struct {
		name  string
		nonce any
	}{
		{name: "string", nonce: "attacker-controlled-nonce"},
		{name: "empty string", nonce: ""},
		{name: "null", nonce: nil},
	}

	for i, test := range tests {
		claims := claimsAt(now, fmt.Sprintf("jti-with-nonce-%d", i))
		claims["nonce"] = test.nonce
		token := signRS256(t, signingKey(t), logoutHeader(), claims)

		_, err := validator.Validate(context.Background(), token)
		if !errors.Is(err, netsec.ErrNoncePresent) {
			t.Errorf("%s: Validate error = %v, want ErrNoncePresent", test.name, err)
		}
	}
}

func TestRejectRepeatedJTIWithinTenMinutes(t *testing.T) {
	start := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	now := start
	cache := netsec.NewReplayCache()
	validator := newValidator(t, func() time.Time { return now }, cache)
	token := signRS256(t, signingKey(t), logoutHeader(), claimsAt(start, "replayed-jti"))

	if _, err := validator.Validate(context.Background(), token); err != nil {
		t.Fatalf("first Validate: %v", err)
	}

	now = start.Add(9 * time.Minute)
	if _, err := validator.Validate(context.Background(), token); !errors.Is(err, netsec.ErrReplayDetected) {
		t.Fatalf("second Validate error = %v, want ErrReplayDetected", err)
	}

	now = start.Add(netsec.ReplayWindow + time.Second)
	if _, err := validator.Validate(context.Background(), token); err != nil {
		t.Fatalf("Validate after replay window: %v", err)
	}
}

func TestSubjectOnlyTerminatesAllSessions(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	claims := claimsAt(now, "jti-sub-only")
	delete(claims, "sid")
	token := signRS256(t, signingKey(t), logoutHeader(), claims)

	result, err := validator.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if result.Scope() != netsec.TerminateAllSessions || !result.AllSessions() {
		t.Fatalf("Scope() = %v, AllSessions = %v, want all sessions", result.Scope(), result.AllSessions())
	}
	if result.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty", result.SessionID)
	}
	if result.Subject != "user-1" {
		t.Fatalf("Subject = %q", result.Subject)
	}
}

func TestSIDOnlyTerminatesOneSession(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	claims := claimsAt(now, "jti-sid-only")
	delete(claims, "sub")
	token := signRS256(t, signingKey(t), logoutHeader(), claims)

	result, err := validator.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if result.Scope() != netsec.TerminateSession || result.AllSessions() {
		t.Fatalf("Scope() = %v, AllSessions = %v, want one session", result.Scope(), result.AllSessions())
	}
	if result.SessionID != "session-1" {
		t.Fatalf("SessionID = %q", result.SessionID)
	}
}

func TestRejectBadIssuer(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	claims := claimsAt(now, "jti-bad-issuer")
	claims["iss"] = "https://attacker.example"
	token := signRS256(t, signingKey(t), logoutHeader(), claims)

	_, err := validator.Validate(context.Background(), token)
	if !errors.Is(err, netsec.ErrIssuerMismatch) {
		t.Fatalf("Validate error = %v, want ErrIssuerMismatch", err)
	}
}

func TestRejectBadAudience(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	claims := claimsAt(now, "jti-bad-audience")
	claims["aud"] = []string{"someone-else"}
	token := signRS256(t, signingKey(t), logoutHeader(), claims)

	_, err := validator.Validate(context.Background(), token)
	if !errors.Is(err, netsec.ErrAudienceMismatch) {
		t.Fatalf("Validate error = %v, want ErrAudienceMismatch", err)
	}
}

func TestAudienceArrayContainingRP(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	claims := claimsAt(now, "jti-audience-array")
	claims["aud"] = []string{"another-client", testAudience}
	token := signRS256(t, signingKey(t), logoutHeader(), claims)

	if _, err := validator.Validate(context.Background(), token); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRejectInvalidSignature(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other RSA key: %v", err)
	}
	token := signRS256(t, otherKey, logoutHeader(), claimsAt(now, "jti-bad-signature"))

	_, err = validator.Validate(context.Background(), token)
	if !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("Validate error = %v, want ErrInvalidSignature", err)
	}
}

func TestES256Signature(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	validator := newValidatorForKey(t, func() time.Time { return now }, nil, &key.PublicKey, "ec-key")

	header := logoutHeader()
	header["alg"] = "ES256"
	header["kid"] = "ec-key"
	token := signWith(t, header, claimsAt(now, "jti-es256"), func(signingInput []byte) []byte {
		digest := sha256.Sum256(signingInput)
		r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
		if err != nil {
			t.Fatalf("sign ES256 token: %v", err)
		}
		partSize := (key.Curve.Params().BitSize + 7) / 8
		signature := make([]byte, 2*partSize)
		r.FillBytes(signature[:partSize])
		s.FillBytes(signature[partSize:])
		return signature
	})

	if _, err := validator.Validate(context.Background(), token); err != nil {
		t.Fatalf("Validate ES256: %v", err)
	}
}

func TestEdDSASignature(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	validator := newValidatorForKey(t, func() time.Time { return now }, nil, publicKey, "ed-key")

	header := logoutHeader()
	header["alg"] = "EdDSA"
	header["kid"] = "ed-key"
	token := signWith(t, header, claimsAt(now, "jti-eddsa"), func(signingInput []byte) []byte {
		return ed25519.Sign(privateKey, signingInput)
	})

	if _, err := validator.Validate(context.Background(), token); err != nil {
		t.Fatalf("Validate EdDSA: %v", err)
	}
}

func TestHS256Signature(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	secret := []byte("a-test-hmac-secret-that-is-long-enough")
	validator := newValidatorForKey(t, func() time.Time { return now }, nil, secret, "hmac-key")

	header := logoutHeader()
	header["alg"] = "HS256"
	header["kid"] = "hmac-key"
	token := signWith(t, header, claimsAt(now, "jti-hs256"), func(signingInput []byte) []byte {
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(signingInput)
		return mac.Sum(nil)
	})

	if _, err := validator.Validate(context.Background(), token); err != nil {
		t.Fatalf("Validate HS256: %v", err)
	}
}

func TestRejectMissingLogoutEvent(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	tests := []struct {
		name   string
		events any
	}{
		{name: "absent", events: nil},
		{name: "different event", events: map[string]any{"urn:example:other": map[string]any{}}},
		{name: "event value is not an object", events: map[string]any{netsec.EventBackchannelLogout: true}},
		{name: "events is not an object", events: []any{"backchannel-logout"}},
	}

	for i, test := range tests {
		claims := claimsAt(now, fmt.Sprintf("jti-event-%d", i))
		if test.events == nil {
			delete(claims, "events")
		} else {
			claims["events"] = test.events
		}
		token := signRS256(t, signingKey(t), logoutHeader(), claims)

		_, err := validator.Validate(context.Background(), token)
		if !errors.Is(err, netsec.ErrMissingLogoutEvent) {
			t.Errorf("%s: Validate error = %v, want ErrMissingLogoutEvent", test.name, err)
		}
	}
}

func TestRejectMissingSubjectAndSession(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	claims := claimsAt(now, "jti-no-subject")
	delete(claims, "sub")
	delete(claims, "sid")
	token := signRS256(t, signingKey(t), logoutHeader(), claims)

	_, err := validator.Validate(context.Background(), token)
	if !errors.Is(err, netsec.ErrMissingSubjectOrSession) {
		t.Fatalf("Validate error = %v, want ErrMissingSubjectOrSession", err)
	}
}

func TestRejectExpiredToken(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	claims := claimsAt(now, "jti-expired")
	claims["exp"] = now.Add(-time.Second).Unix()
	token := signRS256(t, signingKey(t), logoutHeader(), claims)

	_, err := validator.Validate(context.Background(), token)
	if !errors.Is(err, netsec.ErrTokenExpired) {
		t.Fatalf("Validate error = %v, want ErrTokenExpired", err)
	}
}

func TestRejectAlgNone(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	validator := newValidator(t, func() time.Time { return now }, nil)

	header := logoutHeader()
	header["alg"] = "none"
	token := signRS256(t, signingKey(t), header, claimsAt(now, "jti-none"))

	_, err := validator.Validate(context.Background(), token)
	if !errors.Is(err, netsec.ErrUnsupportedAlgorithm) {
		t.Fatalf("Validate error = %v, want ErrUnsupportedAlgorithm", err)
	}
}

func TestInvalidTokenDoesNotConsumeJTI(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	cache := netsec.NewReplayCache()
	validator := newValidator(t, func() time.Time { return now }, cache)

	invalidClaims := claimsAt(now, "jti-not-consumed")
	invalidClaims["nonce"] = "nonce"
	invalid := signRS256(t, signingKey(t), logoutHeader(), invalidClaims)
	if _, err := validator.Validate(context.Background(), invalid); !errors.Is(err, netsec.ErrNoncePresent) {
		t.Fatalf("invalid token error = %v, want ErrNoncePresent", err)
	}

	valid := signRS256(t, signingKey(t), logoutHeader(), claimsAt(now, "jti-not-consumed"))
	if _, err := validator.Validate(context.Background(), valid); err != nil {
		t.Fatalf("valid token sharing rejected jti: %v", err)
	}
}

func TestReplayCheckIsAtomic(t *testing.T) {
	now := time.Date(2026, 11, 13, 4, 0, 0, 0, time.UTC)
	cache := netsec.NewReplayCache()
	validator := newValidator(t, func() time.Time { return now }, cache)
	token := signRS256(t, signingKey(t), logoutHeader(), claimsAt(now, "jti-concurrent"))

	const attempts = 32
	var accepted atomic.Int32
	var waitGroup sync.WaitGroup
	waitGroup.Add(attempts)

	for range attempts {
		go func() {
			defer waitGroup.Done()
			if _, err := validator.Validate(context.Background(), token); err == nil {
				accepted.Add(1)
			}
		}()
	}
	waitGroup.Wait()

	if got := accepted.Load(); got != 1 {
		t.Fatalf("accepted %d concurrent validations, want exactly 1", got)
	}
}
