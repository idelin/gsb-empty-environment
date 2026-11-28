package nestedjwt_test

import (
	"errors"
	"testing"

	"formal.local/w28-5b20ffbc87/nestedjwt"
)

func TestAudienceMismatchAcrossLayers(t *testing.T) {
	verifier, serverKey, clientEC := newHarness(t)

	opts := defaultJWEOptions(serverKID)
	opts.extraHeaders = map[string]any{"aud": []string{"gateway", "different-audience"}}
	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-aud-bad"), opts)
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrCrossLayerClaim) {
		t.Fatalf("expected ErrCrossLayerClaim on aud mismatch, got %v", err)
	}

	opts = defaultJWEOptions(serverKID)
	opts.extraHeaders = map[string]any{"aud": []string{"treasury", "gateway"}}
	token = buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-aud-ok"), opts)
	if _, err := verifier.Verify(token); err != nil {
		t.Fatalf("reordered audience members should compare equal: %v", err)
	}

	opts = defaultJWEOptions(serverKID)
	opts.extraHeaders = map[string]any{"aud": "gateway"}
	token = buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-aud-single"), opts)
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrCrossLayerClaim) {
		t.Fatalf("single vs multi aud: expected ErrCrossLayerClaim, got %v", err)
	}
}

func TestIssuerSubjectMismatchAcrossLayers(t *testing.T) {
	verifier, serverKey, clientEC := newHarness(t)

	cases := []struct {
		name   string
		header map[string]any
	}{
		{"iss", map[string]any{"iss": "rogue-issuer"}},
		{"sub", map[string]any{"sub": "wrong-user"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := defaultJWEOptions(serverKID)
			opts.extraHeaders = tc.header
			token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-x-"+tc.name), opts)
			if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrCrossLayerClaim) {
				t.Fatalf("header %v: expected ErrCrossLayerClaim, got %v", tc.header, err)
			}
		})
	}

	opts := defaultJWEOptions(serverKID)
	opts.extraHeaders = map[string]any{"iss": "issuer-a", "sub": "user-7788"}
	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-iss-sub-ok"), opts)
	if _, err := verifier.Verify(token); err != nil {
		t.Fatalf("matching outer iss/sub: %v", err)
	}
}

func TestReplayProtection(t *testing.T) {
	verifier, serverKey, clientEC := newHarness(t)
	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-replay"), defaultJWEOptions(serverKID))

	if _, err := verifier.Verify(token); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrReplayed) {
		t.Fatalf("second use: expected ErrReplayed, got %v", err)
	}

	other := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-fresh"), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(other); err != nil {
		t.Fatalf("fresh jti: %v", err)
	}

	foreign := buildNested(serverKey, clientEC, sampleClaims("issuer-b", "jti-replay"), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(foreign); err != nil {
		t.Fatalf("different issuer namespace must be independent: %v", err)
	}

	missingJTI := sampleClaims("issuer-a", "jti-missing")
	delete(missingJTI, "jti")
	token = buildNested(serverKey, clientEC, missingJTI, defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrMissingReplayClaims) {
		t.Fatalf("missing jti: expected ErrMissingReplayClaims, got %v", err)
	}
}

func TestNoneAndSymmetricSignatureRejected(t *testing.T) {
	verifier, serverKey, _ := newHarness(t)

	noneJWS := unsignedJWS(clientKID, mustClaimsJSON(t, sampleClaims("issuer-a", "jti-none")))
	token := encryptJWE(&serverKey.PublicKey, []byte(noneJWS), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrNoneAlgorithm) {
		t.Fatalf("alg=none: expected ErrNoneAlgorithm, got %v", err)
	}

	hmacJWS := hmacSignedJWS("HS256", clientKID, mustClaimsJSON(t, sampleClaims("issuer-a", "jti-hmac")))
	token = encryptJWE(&serverKey.PublicKey, []byte(hmacJWS), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrSymmetricAlgorithm) {
		t.Fatalf("alg=HS256: expected ErrSymmetricAlgorithm, got %v", err)
	}
}
