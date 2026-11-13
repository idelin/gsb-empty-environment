package netsec_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0120"
)

const credentialEndpoint = "https://issuer.example.test/credential"

func TestFirstMatchingProofAcceptedAndReplayRejected(t *testing.T) {
	key := newSigningKey(t)
	verifier := newVerifier(t, key)
	nonce := issueNonce(t, verifier)
	proof := signES256(t, key, nil, claims(credentialEndpoint, nonce))

	if err := verifier.ValidateProof(proof); err != nil {
		t.Fatalf("first valid proof rejected: %v", err)
	}
	if err := verifier.ValidateProof(proof); !errors.Is(err, netsec.ErrNonceReused) {
		t.Fatalf("replay error = %v, want ErrNonceReused", err)
	}
}

func TestChangedAudienceAndReplayRejected(t *testing.T) {
	key := newSigningKey(t)
	verifier := newVerifier(t, key)
	nonce := issueNonce(t, verifier)

	if err := verifier.ValidateProof(signES256(t, key, nil, claims(credentialEndpoint, nonce))); err != nil {
		t.Fatalf("first valid proof rejected: %v", err)
	}

	replay := signES256(t, key, nil, claims("https://issuer.example.test/token", nonce))
	err := verifier.ValidateProof(replay)
	if err == nil {
		t.Fatal("proof with changed audience and replayed nonce was accepted")
	}
	if !errors.Is(err, netsec.ErrAudienceMismatch) && !errors.Is(err, netsec.ErrNonceReused) {
		t.Fatalf("replay error = %v, want audience mismatch or nonce reuse", err)
	}
}

func TestWrongNonceDoesNotConsumeCurrentNonce(t *testing.T) {
	key := newSigningKey(t)
	verifier := newVerifier(t, key)
	nonce := issueNonce(t, verifier)

	wrong := signES256(t, key, nil, claims(credentialEndpoint, base64.RawURLEncoding.EncodeToString([]byte("attacker nonce"))))
	if err := verifier.ValidateProof(wrong); !errors.Is(err, netsec.ErrNonceMismatch) {
		t.Fatalf("wrong nonce error = %v, want ErrNonceMismatch", err)
	}

	if err := verifier.ValidateProof(signES256(t, key, nil, claims(credentialEndpoint, nonce))); err != nil {
		t.Fatalf("current nonce was consumed by wrong proof: %v", err)
	}
}

func TestWrongAudienceDoesNotConsumeCurrentNonce(t *testing.T) {
	key := newSigningKey(t)
	verifier := newVerifier(t, key)
	nonce := issueNonce(t, verifier)

	wrongAudience := signES256(t, key, nil, claims("https://issuer.example.test/other", nonce))
	if err := verifier.ValidateProof(wrongAudience); !errors.Is(err, netsec.ErrAudienceMismatch) {
		t.Fatalf("wrong audience error = %v, want ErrAudienceMismatch", err)
	}

	if err := verifier.ValidateProof(signES256(t, key, nil, claims(credentialEndpoint, nonce))); err != nil {
		t.Fatalf("current nonce was consumed by wrong-audience proof: %v", err)
	}
}

func TestConcurrentReplayAcceptsExactlyOnce(t *testing.T) {
	key := newSigningKey(t)
	verifier := newVerifier(t, key)
	nonce := issueNonce(t, verifier)
	proof := signES256(t, key, nil, claims(credentialEndpoint, nonce))

	const attempts = 32
	var accepted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := verifier.ValidateProof(proof)
			if err == nil {
				accepted.Add(1)
				return
			}
			if !errors.Is(err, netsec.ErrNonceReused) {
				t.Errorf("concurrent replay error = %v, want ErrNonceReused", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if accepted.Load() != 1 {
		t.Fatalf("accepted %d proofs, want exactly 1", accepted.Load())
	}
}

func TestEmbeddedJWK(t *testing.T) {
	key := newSigningKey(t)
	verifier, err := netsec.NewVerifier(credentialEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	nonce := issueNonce(t, verifier)
	header := map[string]any{
		"jwk": map[string]any{
			"kty": "EC",
			"crv": "P-256",
			"x":   base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))),
			"y":   base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32))),
		},
	}
	if err := verifier.ValidateProof(signES256(t, key, header, claims(credentialEndpoint, nonce))); err != nil {
		t.Fatalf("proof with embedded JWK rejected: %v", err)
	}
}

func TestTamperedSignatureRejectedWithoutConsumingNonce(t *testing.T) {
	key := newSigningKey(t)
	verifier := newVerifier(t, key)
	nonce := issueNonce(t, verifier)
	proof := signES256(t, key, nil, claims(credentialEndpoint, nonce))
	tampered := tamperSignature(t, proof)

	if err := verifier.ValidateProof(tampered); !errors.Is(err, netsec.ErrInvalidSignature) && !errors.Is(err, netsec.ErrMalformedProof) {
		t.Fatalf("tampered proof error = %v, want signature or format error", err)
	}
	if err := verifier.ValidateProof(proof); err != nil {
		t.Fatalf("valid proof rejected after invalid signature: %v", err)
	}
}

func TestIssuingFreshNonceInvalidatesPreviousNonce(t *testing.T) {
	key := newSigningKey(t)
	verifier := newVerifier(t, key)
	first := issueNonce(t, verifier)
	second := issueNonce(t, verifier)

	if err := verifier.ValidateProof(signES256(t, key, nil, claims(credentialEndpoint, first))); !errors.Is(err, netsec.ErrNonceMismatch) {
		t.Fatalf("old nonce error = %v, want ErrNonceMismatch", err)
	}
	if err := verifier.ValidateProof(signES256(t, key, nil, claims(credentialEndpoint, second))); err != nil {
		t.Fatalf("new nonce rejected: %v", err)
	}
}

func TestAudienceArrayMustContainOnlyCredentialEndpoint(t *testing.T) {
	key := newSigningKey(t)
	verifier := newVerifier(t, key)
	nonce := issueNonce(t, verifier)
	payload := claims(credentialEndpoint, nonce)
	payload["aud"] = []string{credentialEndpoint, "https://issuer.example.test/token"}
	if err := verifier.ValidateProof(signES256(t, key, nil, payload)); !errors.Is(err, netsec.ErrAudienceMismatch) {
		t.Fatalf("multi-audience proof error = %v, want ErrAudienceMismatch", err)
	}
}

func TestExpiredProofRejected(t *testing.T) {
	key := newSigningKey(t)
	now := time.Unix(1_800_000_000, 0)
	verifier, err := netsec.NewVerifier(credentialEndpoint,
		netsec.WithVerificationKey(&key.PublicKey),
		netsec.WithClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	nonce := issueNonce(t, verifier)
	payload := claims(credentialEndpoint, nonce)
	payload["iat"] = now.Add(-2 * time.Minute).Unix()
	payload["exp"] = now.Add(-time.Minute).Unix()
	if err := verifier.ValidateProof(signES256(t, key, nil, payload)); !errors.Is(err, netsec.ErrExpiredProof) {
		t.Fatalf("expired proof error = %v, want ErrExpiredProof", err)
	}
}

func TestHMACProof(t *testing.T) {
	secret := []byte("a sufficiently long test secret for HMAC")
	verifier, err := netsec.NewVerifier(credentialEndpoint, netsec.WithHMACSecret(secret))
	if err != nil {
		t.Fatal(err)
	}
	nonce := issueNonce(t, verifier)
	proof := signHS256(t, secret, claims(credentialEndpoint, nonce))
	if err := verifier.ValidateProof(proof); err != nil {
		t.Fatalf("HMAC proof rejected: %v", err)
	}
}

func TestWrongSigningKeyRejectedWithoutConsumingNonce(t *testing.T) {
	key := newSigningKey(t)
	attacker := newSigningKey(t)
	verifier := newVerifier(t, key)
	nonce := issueNonce(t, verifier)

	if err := verifier.ValidateProof(signES256(t, attacker, nil, claims(credentialEndpoint, nonce))); !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("wrong-key error = %v, want ErrInvalidSignature", err)
	}
	if err := verifier.ValidateProof(signES256(t, key, nil, claims(credentialEndpoint, nonce))); err != nil {
		t.Fatalf("valid proof rejected after wrong-key attempt: %v", err)
	}
}

func newSigningKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func newVerifier(t *testing.T, key *ecdsa.PrivateKey) *netsec.Verifier {
	t.Helper()
	verifier, err := netsec.NewVerifier(credentialEndpoint, netsec.WithVerificationKey(&key.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func issueNonce(t *testing.T, verifier *netsec.Verifier) string {
	t.Helper()
	nonce, err := verifier.IssueNonce()
	if err != nil {
		t.Fatal(err)
	}
	if nonce == "" {
		t.Fatal("issuer returned an empty c_nonce")
	}
	return nonce
}

func claims(audience, nonce string) map[string]any {
	return map[string]any{
		"aud":   audience,
		"nonce": nonce,
		"iat":   time.Now().Unix(),
	}
}

func signES256(t *testing.T, key *ecdsa.PrivateKey, header, payload map[string]any) string {
	t.Helper()
	if header == nil {
		header = make(map[string]any)
	}
	header["alg"] = "ES256"
	header["typ"] = "openid4vci-proof+jwt"

	signingInput := encodeJSON(t, header) + "." + encodeJSON(t, payload)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func signHS256(t *testing.T, secret []byte, payload map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "HS256", "typ": "openid4vci-proof+jwt"}
	signingInput := encodeJSON(t, header) + "." + encodeJSON(t, payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func encodeJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func tamperSignature(t *testing.T, proof string) string {
	t.Helper()
	parts := splitProof(t, proof)
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	signature[0] ^= 0x01
	parts[2] = base64.RawURLEncoding.EncodeToString(signature)
	return parts[0] + "." + parts[1] + "." + parts[2]
}

func splitProof(t *testing.T, proof string) []string {
	t.Helper()
	var parts []string
	start := 0
	for i := 0; i < len(proof); i++ {
		if proof[i] == '.' {
			parts = append(parts, proof[start:i])
			start = i + 1
		}
	}
	parts = append(parts, proof[start:])
	if len(parts) != 3 {
		t.Fatalf("proof has %d segments, want 3", len(parts))
	}
	return parts
}
