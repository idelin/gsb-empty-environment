package netsec

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testIssuer   = "https://idp.example.com"
	testAudience = "https://sp.example.com"
	testOrigin   = "https://app.example.com"
)

type testRig struct {
	keys     *KeySet
	issuer   *TokenIssuer
	verifier *Verifier
	store    *MemorySessionStore
	service  *LogoutService
}

func newTestRig(t *testing.T) *testRig {
	t.Helper()
	privateKey := mustRSAKey(t)
	keys := NewKeySet()
	if err := keys.AddSigningKey("key-1", privateKey); err != nil {
		t.Fatalf("add signing key: %v", err)
	}
	issuer, err := NewTokenIssuer(testIssuer, keys, time.Minute)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	verifier, err := NewVerifier(VerifierConfig{
		Audience: testAudience,
		Issuer:   testIssuer,
		Keys:     keys,
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	store := NewMemorySessionStore()
	service := NewLogoutService(verifier, store, WithAllowedOrigins(testOrigin))
	return &testRig{
		keys:     keys,
		issuer:   issuer,
		verifier: verifier,
		store:    store,
		service:  service,
	}
}

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	testRSAKeysOnce.Do(func() {
		for index := 0; index < 4; index++ {
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				testRSAKeysErr = err
				return
			}
			testRSAKeys = append(testRSAKeys, key)
		}
	})
	if testRSAKeysErr != nil {
		t.Fatalf("generate RSA key: %v", testRSAKeysErr)
	}
	index := testRSAKeyIndex.Add(1) - 1
	return testRSAKeys[int(index)%len(testRSAKeys)]
}

var (
	testRSAKeysOnce sync.Once
	testRSAKeys     []*rsa.PrivateKey
	testRSAKeysErr  error
	testRSAKeyIndex atomic.Uint64
)

func mustECDSAKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	return key
}

func (r *testRig) addSessions(t *testing.T) {
	t.Helper()
	for _, session := range []Session{
		{Issuer: testIssuer, ID: "session-alice-1", Subject: "alice"},
		{Issuer: testIssuer, ID: "session-alice-2", Subject: "alice"},
		{Issuer: testIssuer, ID: "session-bob-1", Subject: "bob"},
	} {
		if err := r.store.Put(session); err != nil {
			t.Fatalf("put session: %v", err)
		}
	}
}

func (r *testRig) issue(t *testing.T, audience, sessionID, subject string) string {
	t.Helper()
	token, err := r.issuer.IssueLogoutToken(audience, sessionID, subject)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
}

func backChannelRequest(t *testing.T, service *LogoutService, token string) *httptest.ResponseRecorder {
	t.Helper()
	body := url.Values{"logout_token": []string{token}}.Encode()
	request := httptest.NewRequest(http.MethodPost, "/backchannel-logout", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	recorder := httptest.NewRecorder()
	service.BackChannelHandler().ServeHTTP(recorder, request)
	return recorder
}

func TestValidBackChannelTerminatesOnlyTargetSession(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)
	token := rig.issue(t, testAudience, "session-alice-1", "alice")

	response := backChannelRequest(t, rig.service, token)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("target session still active")
	}
	if !rig.store.HasSession(testIssuer, "session-alice-2") {
		t.Fatal("another session belonging to the same user was terminated")
	}
	if !rig.store.HasSession(testIssuer, "session-bob-1") {
		t.Fatal("unrelated session was terminated")
	}
	if rig.store.Count() != 2 {
		t.Fatalf("active sessions = %d, want 2", rig.store.Count())
	}
}

func TestIssuerScopesSessionTarget(t *testing.T) {
	rig := newTestRig(t)
	const otherIssuer = "https://other-idp.example.com"
	if err := rig.store.AddSession(testIssuer, "shared-session", "alice"); err != nil {
		t.Fatalf("add target session: %v", err)
	}
	if err := rig.store.AddSession(otherIssuer, "shared-session", "alice"); err != nil {
		t.Fatalf("add other issuer session: %v", err)
	}
	token := rig.issue(t, testAudience, "shared-session", "alice")

	if response := backChannelRequest(t, rig.service, token); response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if rig.store.HasSession(testIssuer, "shared-session") {
		t.Fatal("target session still active")
	}
	if !rig.store.HasSession(otherIssuer, "shared-session") {
		t.Fatal("session with the same id under another issuer was terminated")
	}
}

func TestTokenLifetimeIsShort(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC().Truncate(time.Second)
	rig.issuer.Now = func() time.Time { return now }
	rig.verifier.now = func() time.Time { return now }
	token := rig.issue(t, testAudience, "session-alice-1", "alice")

	claims, err := rig.verifier.Validate(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := claims.ExpiresAt.Sub(claims.IssuedAt); got != time.Minute {
		t.Fatalf("token lifetime = %s, want 1m", got)
	}
	if claims.Issuer != testIssuer || claims.Audience != testAudience {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if claims.SessionID != "session-alice-1" || claims.Subject != "alice" {
		t.Fatalf("token is not bound to the target session: %+v", claims)
	}
}

func TestReplayIsRejected(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)
	token := rig.issue(t, testAudience, "session-alice-1", "alice")

	if response := backChannelRequest(t, rig.service, token); response.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", response.Code)
	}
	rig.store.AddSession(testIssuer, "session-alice-1", "alice")

	response := backChannelRequest(t, rig.service, token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("replay status = %d, want 400", response.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("replayed token terminated a session")
	}
	if _, err := rig.verifier.Verify(token); !errors.Is(err, ErrReplay) {
		t.Fatalf("Verify replay error = %v, want ErrReplay", err)
	}
}

func TestForgedIssuerIsRejected(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)

	forgedIssuer, err := NewTokenIssuer("https://attacker.example.com", rig.keys, time.Minute)
	if err != nil {
		t.Fatalf("new forged issuer: %v", err)
	}
	token, err := forgedIssuer.IssueLogoutToken(testAudience, "session-alice-1", "alice")
	if err != nil {
		t.Fatalf("issue forged token: %v", err)
	}

	response := backChannelRequest(t, rig.service, token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("forged issuer terminated a session")
	}
	if _, err := rig.verifier.Verify(token); !errors.Is(err, ErrUnknownIssuer) {
		t.Fatalf("Verify error = %v, want ErrUnknownIssuer", err)
	}
}

func TestCrossAudienceIsRejected(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)
	token := rig.issue(t, "https://other-service.example.com", "session-alice-1", "alice")

	response := backChannelRequest(t, rig.service, token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("cross-audience token terminated a session")
	}
	if _, err := rig.verifier.Verify(token); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("Verify error = %v, want ErrAudienceMismatch", err)
	}
}

func TestRetiredKeyIsRejected(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)
	oldToken := rig.issue(t, testAudience, "session-alice-1", "alice")

	secondKey := mustRSAKey(t)
	if err := rig.keys.ReplaceSigningKey("key-2", secondKey); err != nil {
		t.Fatalf("replace signing key: %v", err)
	}

	response := backChannelRequest(t, rig.service, oldToken)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("old-key status = %d, want 400", response.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("token signed by a retired key terminated a session")
	}
	if _, err := rig.verifier.Verify(oldToken); !errors.Is(err, ErrRetiredKey) {
		t.Fatalf("Verify error = %v, want ErrRetiredKey", err)
	}

	newToken := rig.issue(t, testAudience, "session-alice-1", "alice")
	if response := backChannelRequest(t, rig.service, newToken); response.Code != http.StatusOK {
		t.Fatalf("new-key status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("new key could not terminate the target session")
	}
}

func TestKeyRotationOverlapThenRetirement(t *testing.T) {
	rig := newTestRig(t)
	oldToken := rig.issue(t, testAudience, "session-alice-1", "alice")

	secondKey := mustRSAKey(t)
	if err := rig.keys.RotateSigningKey("key-2", secondKey); err != nil {
		t.Fatalf("rotate signing key: %v", err)
	}
	if _, err := rig.verifier.Validate(oldToken); err != nil {
		t.Fatalf("old token should verify during overlap: %v", err)
	}
	if !rig.keys.RetireKey("key-1") {
		t.Fatal("retire key returned false")
	}
	if _, err := rig.verifier.Validate(oldToken); !errors.Is(err, ErrRetiredKey) {
		t.Fatalf("old token after retirement error = %v, want ErrRetiredKey", err)
	}
}

func TestUnknownSigningKeyIsRejected(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)

	unknownKeys := NewKeySet()
	if err := unknownKeys.AddSigningKey("unknown-key", mustRSAKey(t)); err != nil {
		t.Fatalf("add unknown key: %v", err)
	}
	unknownIssuer, err := NewTokenIssuer(testIssuer, unknownKeys, time.Minute)
	if err != nil {
		t.Fatalf("new unknown issuer: %v", err)
	}
	token, err := unknownIssuer.IssueLogoutToken(testAudience, "session-alice-1", "alice")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	response := backChannelRequest(t, rig.service, token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("unknown-key token terminated a session")
	}
	if _, err := rig.verifier.Verify(token); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Verify error = %v, want ErrUnknownKey", err)
	}
}

func TestExpiredAndUnsignedTokensAreRejected(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)

	expired, err := rig.issuer.Issue(IssueRequest{
		Audience:  testAudience,
		SessionID: "session-alice-1",
		Subject:   "alice",
		IssuedAt:  time.Now().Add(-2 * time.Minute),
		ExpiresAt: time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("issue expired token: %v", err)
	}
	if response := backChannelRequest(t, rig.service, expired); response.Code != http.StatusBadRequest {
		t.Fatalf("expired status = %d, want 400", response.Code)
	}

	unsigned := unsignedToken(t, rig.issuer.Issuer, testAudience, "session-alice-1", "alice")
	if response := backChannelRequest(t, rig.service, unsigned); response.Code != http.StatusBadRequest {
		t.Fatalf("unsigned status = %d, want 400", response.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("invalid token terminated a session")
	}
}

func TestTamperedTokenIsRejected(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)
	token := rig.issue(t, testAudience, "session-alice-1", "alice")

	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	payload = bytes.Replace(payload, []byte(`"sub":"alice"`), []byte(`"sub":"bob"`), 1)
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + parts[2]

	if response := backChannelRequest(t, rig.service, tampered); response.Code != http.StatusBadRequest {
		t.Fatalf("tampered status = %d, want 400", response.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("tampered token terminated a session")
	}
}

func TestConcurrentConsumptionAllowsExactlyOneLogout(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)
	recording := &recordingSessionStore{MemorySessionStore: rig.store}
	rig.service.Sessions = recording

	token := rig.issue(t, testAudience, "session-alice-1", "alice")
	const goroutines = 32
	var successes atomic.Int64
	var replays atomic.Int64
	var failures atomic.Int64
	var wait sync.WaitGroup
	wait.Add(goroutines)
	for index := 0; index < goroutines; index++ {
		go func() {
			defer wait.Done()
			terminated, err := rig.service.LogoutBackChannel(context.Background(), token)
			switch {
			case err == nil && terminated:
				successes.Add(1)
			case errors.Is(err, ErrReplay):
				replays.Add(1)
			default:
				failures.Add(1)
			}
		}()
	}
	wait.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful consumptions = %d, want 1", got)
	}
	if got := replays.Load(); got != goroutines-1 {
		t.Fatalf("replays = %d, want %d", got, goroutines-1)
	}
	if got := failures.Load(); got != 0 {
		t.Fatalf("unexpected failures = %d", got)
	}
	if got := recording.calls.Load(); got != 1 {
		t.Fatalf("session termination calls = %d, want 1", got)
	}
	if rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("target session still active")
	}
	if !rig.store.HasSession(testIssuer, "session-alice-2") || !rig.store.HasSession(testIssuer, "session-bob-1") {
		t.Fatal("concurrent logout affected other sessions")
	}
}

func TestBackChannelContentTypeAndSizeLimits(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)
	token := rig.issue(t, testAudience, "session-alice-1", "alice")

	request := httptest.NewRequest(http.MethodPost, "/backchannel-logout", strings.NewReader("logout_token="+url.QueryEscape(token)))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	rig.service.BackChannelHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type status = %d, want 415", recorder.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("wrong content type terminated a session")
	}

	limited := NewLogoutService(rig.verifier, rig.store, WithMaxBodyBytes(64))
	largeBody := url.Values{"logout_token": []string{strings.Repeat("a", 512)}}.Encode()
	request = httptest.NewRequest(http.MethodPost, "/backchannel-logout", strings.NewReader(largeBody))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder = httptest.NewRecorder()
	limited.BackChannelHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", recorder.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("oversized request terminated a session")
	}
}

func TestFrontChannelChecksIframeOriginAndTargetsOneSession(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)

	badRequest := frontChannelRequest(rig.service, "https://evil.example.com", testIssuer, "session-alice-2")
	if badRequest.Code != http.StatusForbidden {
		t.Fatalf("untrusted origin status = %d, want 403", badRequest.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-alice-2") {
		t.Fatal("untrusted origin terminated a session")
	}

	goodRequest := frontChannelRequest(rig.service, testOrigin, testIssuer, "session-alice-2")
	if goodRequest.Code != http.StatusOK {
		t.Fatalf("trusted origin status = %d, want 200; body=%s", goodRequest.Code, goodRequest.Body.String())
	}
	if rig.store.HasSession(testIssuer, "session-alice-2") {
		t.Fatal("target session still active after front-channel logout")
	}
	if !rig.store.HasSession(testIssuer, "session-alice-1") || !rig.store.HasSession(testIssuer, "session-bob-1") {
		t.Fatal("front-channel logout affected other sessions")
	}
}

func TestFrontChannelAcceptsRefererAndRejectsUnknownIssuer(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)

	request := httptest.NewRequest(http.MethodGet, "/frontchannel-logout?iss="+url.QueryEscape(testIssuer)+"&sid=session-alice-2", nil)
	request.Header.Set("Referer", testOrigin+"/apps/console")
	recorder := httptest.NewRecorder()
	rig.service.FrontChannelHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("referer status = %d, want 200", recorder.Code)
	}

	unknown := frontChannelRequest(rig.service, testOrigin, "https://unknown-idp.example.com", "session-bob-1")
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown issuer status = %d, want 400", unknown.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-bob-1") {
		t.Fatal("unknown issuer terminated a session")
	}
}

func TestSubjectMismatchDoesNotTerminateSession(t *testing.T) {
	rig := newTestRig(t)
	rig.addSessions(t)
	token := rig.issue(t, testAudience, "session-alice-1", "bob")

	response := backChannelRequest(t, rig.service, token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if !rig.store.HasSession(testIssuer, "session-alice-1") {
		t.Fatal("subject mismatch terminated a session")
	}
}

func TestVerifierRejectsLongLifetimeAndOversizedToken(t *testing.T) {
	rig := newTestRig(t)
	rig.issuer.MaxTokenTTL = 0
	token, err := rig.issuer.Issue(IssueRequest{
		Audience:  testAudience,
		SessionID: "session-alice-1",
		Subject:   "alice",
		TTL:       2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	shortLived, err := NewVerifier(VerifierConfig{
		Audience:    testAudience,
		Issuer:      testIssuer,
		Keys:        rig.keys,
		MaxTokenTTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	if _, err := shortLived.Validate(token); !errors.Is(err, ErrTokenLifetime) {
		t.Fatalf("long lifetime error = %v, want ErrTokenLifetime", err)
	}

	small, err := NewVerifier(VerifierConfig{
		Audience:     testAudience,
		Issuer:       testIssuer,
		Keys:         rig.keys,
		MaxTokenSize: 16,
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	if _, err := small.Validate(token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("oversized token error = %v, want ErrInvalidToken", err)
	}
}

func TestECDSAIssuerAndVerifier(t *testing.T) {
	ecdsaKey := mustECDSAKey(t)
	keys := NewKeySet()
	if err := keys.AddSigningKey("ec-key", ecdsaKey); err != nil {
		t.Fatalf("add ECDSA key: %v", err)
	}
	verifyKeys := NewKeySet()
	if err := verifyKeys.AddVerificationKey("ec-key", ecdsaKey.PublicKey); err != nil {
		t.Fatalf("add ECDSA verification key: %v", err)
	}
	issuer, err := NewTokenIssuer(testIssuer, keys, time.Minute)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	verifier, err := NewVerifier(VerifierConfig{
		Audience: testAudience,
		Issuers:  map[string]*KeySet{testIssuer: verifyKeys},
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	token, err := issuer.IssueLogoutToken(testAudience, "ec-session", "carol")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.SessionID != "ec-session" || claims.Subject != "carol" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestEd25519IssuerAndVerifier(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	keys := NewKeySet()
	if err := keys.AddSigningKey("ed-key", privateKey); err != nil {
		t.Fatalf("add Ed25519 key: %v", err)
	}
	issuer, err := NewTokenIssuer(testIssuer, keys, time.Minute)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	verifier, err := NewVerifier(VerifierConfig{
		Audience: testAudience,
		Issuers:  map[string]*KeySet{testIssuer: keys},
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	token, err := issuer.IssueLogoutToken(testAudience, "ed-session", "dave")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.SessionID != "ed-session" || claims.Subject != "dave" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestRSAAlgorithmVariants(t *testing.T) {
	for _, algorithm := range []string{"RS256", "RS512", "PS256"} {
		t.Run(algorithm, func(t *testing.T) {
			keys := NewKeySet()
			if err := keys.AddSigningKeyWithAlgorithm("rsa-"+algorithm, mustRSAKey(t), algorithm); err != nil {
				t.Fatalf("add key: %v", err)
			}
			issuer, err := NewTokenIssuer(testIssuer, keys, time.Minute)
			if err != nil {
				t.Fatalf("new issuer: %v", err)
			}
			verifier, err := NewVerifier(VerifierConfig{
				Audience: testAudience,
				Issuers:  map[string]*KeySet{testIssuer: keys},
			})
			if err != nil {
				t.Fatalf("new verifier: %v", err)
			}
			token, err := issuer.IssueLogoutToken(testAudience, "rsa-session", "erin")
			if err != nil {
				t.Fatalf("issue: %v", err)
			}
			if _, err := verifier.Verify(token); err != nil {
				t.Fatalf("verify: %v", err)
			}
		})
	}
}

type recordingSessionStore struct {
	*MemorySessionStore
	calls atomic.Int64
}

func (s *recordingSessionStore) TerminateSession(ctx context.Context, issuer, sessionID, subject string) (bool, error) {
	s.calls.Add(1)
	return s.MemorySessionStore.TerminateSession(ctx, issuer, sessionID, subject)
}

func frontChannelRequest(service *LogoutService, origin, issuer, sessionID string) *httptest.ResponseRecorder {
	query := url.Values{
		"iss": []string{issuer},
		"sid": []string{sessionID},
	}
	request := httptest.NewRequest(http.MethodGet, "/frontchannel-logout?"+query.Encode(), nil)
	request.Header.Set("Origin", origin)
	recorder := httptest.NewRecorder()
	service.FrontChannelHandler().ServeHTTP(recorder, request)
	return recorder
}

func unsignedToken(t *testing.T, issuer, audience, sessionID, subject string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"key-1","typ":"JWT"}`))
	now := time.Now().Unix()
	payload := fmt.Sprintf(
		`{"iss":%q,"aud":%q,"sid":%q,"sub":%q,"jti":"unsigned","iat":%d,"exp":%d}`,
		issuer, audience, sessionID, subject, now, now+60,
	)
	return header + "." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + "."
}
