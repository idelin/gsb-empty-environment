package nestedjwt_test

import (
	"crypto"
	"errors"
	"strings"
	"testing"
	"time"

	"formal.local/w28-5b20ffbc87/nestedjwt"
)

func TestJWEPolicyChecks(t *testing.T) {
	verifier, serverKey, clientEC := newHarness(t)

	opts := defaultJWEOptions(serverKID)
	opts.zip = "DEF"
	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-zip"), opts)
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrCompressionRejected) {
		t.Fatalf("zip=DEF: expected ErrCompressionRejected, got %v", err)
	}

	opts = defaultJWEOptions(serverKID)
	opts.crit = []string{"http://example.invalid/ext"}
	token = buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-crit"), opts)
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrCriticalHeader) {
		t.Fatalf("crit: expected ErrCriticalHeader, got %v", err)
	}

	opts = defaultJWEOptions(serverKID)
	opts.kid = ""
	token = buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-no-kid"), opts)
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrMissingKeyID) {
		t.Fatalf("missing outer kid: expected ErrMissingKeyID, got %v", err)
	}

	opts = defaultJWEOptions("unknown-kid")
	token = buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-unknown-kid"), opts)
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrUnknownKey) {
		t.Fatalf("unknown outer kid: expected ErrUnknownKey, got %v", err)
	}

	opts = defaultJWEOptions(serverKID)
	opts.alg = "RSA-OAEP"
	token = buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-bad-alg"), opts)
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrUnsupportedAlgorithm) {
		t.Fatalf("alg=RSA-OAEP: expected ErrUnsupportedAlgorithm, got %v", err)
	}

	opts = defaultJWEOptions(serverKID)
	opts.enc = "A128GCM"
	token = buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-bad-enc"), opts)
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrUnsupportedAlgorithm) {
		t.Fatalf("enc=A128GCM: expected ErrUnsupportedAlgorithm, got %v", err)
	}
}

func TestTamperedTokenFailsAuthentication(t *testing.T) {
	verifier, serverKey, clientEC := newHarness(t)
	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-tamper"), defaultJWEOptions(serverKID))

	segments := strings.Split(token, ".")
	if len(segments) != 5 {
		t.Fatalf("test setup: expected 5 segments, got %d", len(segments))
	}
	ciphertext, err := testB64.DecodeString(segments[3])
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	ciphertext[0] ^= 0xFF
	segments[3] = testB64.EncodeToString(ciphertext)
	tampered := strings.Join(segments, ".")
	if _, err := verifier.Verify(tampered); !errors.Is(err, nestedjwt.ErrDecryption) {
		t.Fatalf("tampered ciphertext: expected ErrDecryption, got %v", err)
	}
}

func TestSignatureVerificationAndKeyTypeMismatch(t *testing.T) {
	serverKey := mustRSAKey()
	clientEC := mustECKey()
	rsaSigner := mustRSAKey()

	store := nestedjwt.MapKeyStore{
		DecryptionKeys: map[string]crypto.PrivateKey{serverKID: serverKey},
		SigningKeys: map[string]crypto.PublicKey{
			clientKID: &clientEC.PublicKey,
		},
	}
	verifier := nestedjwt.New(store)

	// RS256 signature presented where an ES256 key is bound: must fail.
	jws := signJWS("RS256", clientKID, rsaSigner, mustClaimsJSON(t, sampleClaims("issuer-a", "jti-wrong-key")))
	token := encryptJWE(&serverKey.PublicKey, []byte(jws), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrSignature) {
		t.Fatalf("RSA signature checked against EC key: expected ErrSignature, got %v", err)
	}

	// Same token shape with the matching RSA public key bound to the kid.
	store = nestedjwt.MapKeyStore{
		DecryptionKeys: map[string]crypto.PrivateKey{serverKID: serverKey},
		SigningKeys: map[string]crypto.PublicKey{
			clientKID: &rsaSigner.PublicKey,
		},
	}
	verifier = nestedjwt.New(store)
	jws = signJWS("RS256", clientKID, rsaSigner, mustClaimsJSON(t, sampleClaims("issuer-a", "jti-rsa-ok")))
	token = encryptJWE(&serverKey.PublicKey, []byte(jws), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); err != nil {
		t.Fatalf("valid RS256 nested token: %v", err)
	}

	// ES384 is asymmetric but not on the default JWS whitelist; a valid
	// signature is irrelevant because the header policy check runs first.
	jws = signJWS("ES256", clientKID, clientEC, mustClaimsJSON(t, sampleClaims("issuer-a", "jti-es384")))
	jws = strings.Replace(jws, b64JSON(map[string]string{"alg": "ES256", "kid": clientKID}), b64JSON(map[string]string{"alg": "ES384", "kid": clientKID}), 1)
	token = encryptJWE(&serverKey.PublicKey, []byte(jws), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrUnsupportedAlgorithm) {
		t.Fatalf("ES384 not on whitelist: expected ErrUnsupportedAlgorithm, got %v", err)
	}
}

func TestInnerKIDEnforced(t *testing.T) {
	verifier, serverKey, clientEC := newHarness(t)

	claims := mustClaimsJSON(t, sampleClaims("issuer-a", "jti-inner-nokid"))
	jws := signJWSNoKID("ES256", clientEC, claims)
	token := encryptJWE(&serverKey.PublicKey, []byte(jws), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrMissingKeyID) {
		t.Fatalf("missing inner kid: expected ErrMissingKeyID, got %v", err)
	}

	unknownJWS := signJWS("ES256", "unregistered-client", clientEC, claims)
	token = encryptJWE(&serverKey.PublicKey, []byte(unknownJWS), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrUnknownKey) {
		t.Fatalf("unknown inner kid: expected ErrUnknownKey, got %v", err)
	}
}

func TestLifetimeValidation(t *testing.T) {
	serverKey := mustRSAKey()
	clientEC := mustECKey()
	store := nestedjwt.MapKeyStore{
		DecryptionKeys: map[string]crypto.PrivateKey{serverKID: serverKey},
		SigningKeys: map[string]crypto.PublicKey{
			clientKID: &clientEC.PublicKey,
		},
	}

	fixed := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	verifier := nestedjwt.New(store, nestedjwt.WithClock(func() time.Time { return fixed }))

	expired := sampleClaims("issuer-a", "jti-expired")
	expired["exp"] = fixed.Add(-2 * time.Minute).Unix()
	token := buildNested(serverKey, clientEC, expired, defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrTokenExpired) {
		t.Fatalf("expired token: expected ErrTokenExpired, got %v", err)
	}

	future := sampleClaims("issuer-a", "jti-future")
	future["exp"] = fixed.Add(time.Hour).Unix()
	future["nbf"] = fixed.Add(2 * time.Minute).Unix()
	token = buildNested(serverKey, clientEC, future, defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrTokenNotYetValid) {
		t.Fatalf("future nbf: expected ErrTokenNotYetValid, got %v", err)
	}

	// 30 seconds in the past stays inside the default one-minute skew.
	edge := sampleClaims("issuer-a", "jti-skew")
	edge["exp"] = fixed.Add(-30 * time.Second).Unix()
	token = buildNested(serverKey, clientEC, edge, defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); err != nil {
		t.Fatalf("token inside clock skew should pass: %v", err)
	}
}

func TestMemoryReplayStoreEviction(t *testing.T) {
	store := nestedjwt.NewMemoryReplayStore(time.Minute, 2)

	seen, err := store.Seen("issuer-a", "jti-1")
	if err != nil || seen {
		t.Fatalf("first insert: seen=%v err=%v", seen, err)
	}
	seen, _ = store.Seen("issuer-a", "jti-1")
	if !seen {
		t.Fatal("second insert must report seen")
	}

	// Capacity cap evicts the oldest entry rather than growing unbounded.
	_, _ = store.Seen("issuer-a", "jti-2")
	_, _ = store.Seen("issuer-a", "jti-3")
	if store.Len() > 2 {
		t.Fatalf("store exceeded capacity: %d", store.Len())
	}
}

func TestCrossLayerKIDMatchIsOptIn(t *testing.T) {
	policy := nestedjwt.DefaultPolicy()
	policy.MatchCrossLayerKID = true
	verifier, serverKey, clientEC := newHarness(t, nestedjwt.WithPolicy(policy))

	// Distinct outer (gateway) and inner (client) kids now trip the policy.
	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-kid-match"), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrCrossLayerClaim) {
		t.Fatalf("MatchCrossLayerKID=true: expected ErrCrossLayerClaim, got %v", err)
	}
}
