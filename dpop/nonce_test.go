package dpop_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"formal.local/w28-dd7127bdf9/dpop"
)

func newNonceManager(t *testing.T, clock *fixedClock, period time.Duration) *dpop.NonceManager {
	t.Helper()
	manager, err := dpop.NewNonceManager(bytes.Repeat([]byte{0x7a}, 32), period)
	if err != nil {
		t.Fatalf("nonce manager: %v", err)
	}
	return manager
}

func TestNonceRequiredChallenge(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	nonce := newNonceManager(t, clock, time.Hour)
	verifier := newVerifier(t, clock, nonce)
	keys := loadTestKeys(t)

	req, _ := validRequest(t, keys, clock, nil) // proof contains no nonce
	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrUseDPoPNonce)
	if dpop.AsError(err).Nonce == "" {
		t.Fatalf("use_dpop_nonce error must carry a fresh DPoP-Nonce")
	}
}

func TestNonceChallengeEmittedByMiddleware(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	nonce := newNonceManager(t, clock, time.Hour)
	verifier := newVerifier(t, clock, nonce)
	handler := newTestHandler(t, verifier)

	keys := loadTestKeys(t)
	accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodGet, htu, clock.t)

	rec := invoke(t, handler, http.MethodGet, "https://api.example.com/resource/1", func(req *http.Request) {
		req.Header.Set("Authorization", "DPoP "+accessToken)
		req.Header.Set("DPoP", signProof(t, opts)) // proof carries no nonce
	})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	challenge := rec.Header().Get("Www-Authenticate")
	if !bytes.Contains([]byte(challenge), []byte(`error="use_dpop_nonce"`)) {
		t.Fatalf("challenge = %q", challenge)
	}
	if rec.Header().Get("Dpop-Nonce") == "" {
		t.Fatalf("response must provide a DPoP-Nonce to retry with")
	}
}

func TestNonceRotationAcceptsPreviousPeriod(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	period := time.Hour
	nonce := newNonceManager(t, clock, period)
	verifier := newVerifier(t, clock, nonce)
	keys := loadTestKeys(t)

	oldNonce := nonce.Issue()

	// Rotate into the next period: a nonce from the previous period remains
	// valid for in-flight requests, while a fresh current-period nonce works.
	clock.advance(period + time.Second)
	currentNonce := nonce.Issue()

	for index, value := range []string{oldNonce, currentNonce} {
		req, _ := validRequest(t, keys, clock, nil)
		htu := "https://api.example.com/resource/1"
		accessToken := req.Header.Get("Authorization")
		accessToken = accessToken[len("DPoP "):]
		opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
		opts.nonce = value
		opts.jti = "jti-nonce-rotation-" + string(rune('a'+index))
		req.Header.Set("DPoP", signProof(t, opts))
		if _, err := verifier.VerifyRequest(req); err != nil {
			t.Fatalf("nonce %q should verify after rotation: %v", value[:12], err)
		}
	}
}

func TestExpiredAndForgedNonceRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	period := time.Hour
	nonce := newNonceManager(t, clock, period)
	verifier := newVerifier(t, clock, nonce)
	keys := loadTestKeys(t)

	issued := nonce.Issue()

	// Two rotations later the nonce is outside the current/previous window.
	clock.advance(2*period + time.Second)

	forged := bytes.Repeat([]byte{'A'}, 48)

	cases := map[string]string{
		"expired": issued,
		"forged":  b64url(forged),
		"empty":   "",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			req, accessToken := validRequest(t, keys, clock, nil)
			htu := "https://api.example.com/resource/1"
			opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
			opts.nonce = value
			opts.jti = "jti-bad-nonce-" + name
			req.Header.Set("DPoP", signProof(t, opts))
			_, err := verifier.VerifyRequest(req)
			assertDPoPError(t, err, dpop.ErrUseDPoPNonce)
		})
	}
}

func TestNonceSignedByDifferentSecretRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	managerA := newNonceManager(t, clock, time.Hour)
	managerB, err := dpop.NewNonceManager(bytes.Repeat([]byte{0x33}, 32), time.Hour)
	if err != nil {
		t.Fatalf("manager B: %v", err)
	}

	verifier := newVerifier(t, clock, managerA)
	keys := loadTestKeys(t)

	req, accessToken := validRequest(t, keys, clock, nil)
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
	opts.nonce = managerB.Issue()
	opts.jti = "jti-wrong-secret"
	req.Header.Set("DPoP", signProof(t, opts))

	_, verifyErr := verifier.VerifyRequest(req)
	assertDPoPError(t, verifyErr, dpop.ErrUseDPoPNonce)
}

func TestNewNonceManagerRejectsWeakSecret(t *testing.T) {
	if _, err := dpop.NewNonceManager([]byte("too-short"), time.Hour); err == nil {
		t.Fatalf("weak nonce secret must be rejected")
	}
}
