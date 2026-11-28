package nestedjwt_test

import (
	"errors"
	"testing"

	"formal.local/w28-5b20ffbc87/nestedjwt"
)

func TestVerifyHappyPath(t *testing.T) {
	verifier, serverKey, clientEC := newHarness(t)
	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-1"), defaultJWEOptions(serverKID))

	verified, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if verified.Claims.Issuer != "issuer-a" || verified.Claims.JWTID != "jti-1" {
		t.Fatalf("unexpected claims: %+v", verified.Claims)
	}
	if verified.Claims.Raw["role"] != "payment-admin" {
		t.Fatalf("custom claim lost: %v", verified.Claims.Raw)
	}
	if verified.NestingDepth != 2 {
		t.Fatalf("expected depth 2, got %d", verified.NestingDepth)
	}
	if verified.OuterHeader.ContentType != "JWT" || verified.InnerHeader.Algorithm != "ES256" {
		t.Fatalf("unexpected headers: outer=%+v inner=%+v", verified.OuterHeader, verified.InnerHeader)
	}
	if len(verified.Claims.Audience) != 2 {
		t.Fatalf("audience not parsed: %+v", verified.Claims.Audience)
	}
}

func TestBareJWSIsRejected(t *testing.T) {
	verifier, _, clientEC := newHarness(t)
	jws := signJWS("ES256", clientKID, clientEC, mustClaimsJSON(t, sampleClaims("issuer-a", "jti-bare")))

	if _, err := verifier.Verify(jws); !errors.Is(err, nestedjwt.ErrBareJWSRejected) {
		t.Fatalf("expected ErrBareJWSRejected, got %v", err)
	}

	for _, malformed := range []string{"a.b", "a.b.c.d", "a.b.c.d.e.f", ""} {
		if _, err := verifier.Verify(malformed); !errors.Is(err, nestedjwt.ErrMalformedToken) {
			t.Fatalf("token %q: expected ErrMalformedToken, got %v", malformed, err)
		}
	}
}

func TestBareJWSAcceptedWhenPolicyAllows(t *testing.T) {
	policy := nestedjwt.DefaultPolicy()
	policy.RequireNested = false
	verifier, _, clientEC := newHarness(t, nestedjwt.WithPolicy(policy))

	jws := signJWS("ES256", clientKID, clientEC, mustClaimsJSON(t, sampleClaims("issuer-a", "jti-flat")))
	verified, err := verifier.Verify(jws)
	if err != nil {
		t.Fatalf("bare JWS should be accepted with RequireNested=false: %v", err)
	}
	if verified.NestingDepth != 1 {
		t.Fatalf("expected depth 1, got %d", verified.NestingDepth)
	}
}

func TestEncryptedRawJSONIsRejected(t *testing.T) {
	verifier, serverKey, _ := newHarness(t)

	forged := mustClaimsJSON(t, map[string]any{
		"iss":  "issuer-a",
		"sub":  "attacker",
		"aud":  "gateway",
		"jti":  "forged-1",
		"role": "super-root",
	})
	token := encryptJWE(&serverKey.PublicKey, forged, defaultJWEOptions(serverKID))

	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrInnerNotJWS) {
		t.Fatalf("forged JSON: expected ErrInnerNotJWS, got %v", err)
	}

	token = encryptJWE(&serverKey.PublicKey, []byte("not-a-token-at-all"), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrInnerNotJWS) {
		t.Fatalf("plaintext string: expected ErrInnerNotJWS, got %v", err)
	}
}

func TestMissingContentTypeRejected(t *testing.T) {
	verifier, serverKey, clientEC := newHarness(t)

	opts := defaultJWEOptions(serverKID)
	opts.omitCTY = true
	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-no-cty"), opts)
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrMissingContentType) {
		t.Fatalf("missing cty: expected ErrMissingContentType, got %v", err)
	}

	opts = defaultJWEOptions(serverKID)
	opts.cty = "application/json"
	token = buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-wrong-cty"), opts)
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrMissingContentType) {
		t.Fatalf("wrong cty: expected ErrMissingContentType, got %v", err)
	}

	opts = defaultJWEOptions(serverKID)
	opts.cty = "jwt"
	token = buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-lower-cty"), opts)
	if _, err := verifier.Verify(token); err != nil {
		t.Fatalf("lowercase cty=jwt should match case-insensitively: %v", err)
	}
}

func TestThreeLayerNestingRejected(t *testing.T) {
	verifier, serverKey, clientEC := newHarness(t)

	innermost := signJWS("ES256", clientKID, clientEC, mustClaimsJSON(t, sampleClaims("issuer-a", "jti-deep-a")))
	middleJWS := signJWS("ES256", clientKID, clientEC, []byte(innermost))
	token := encryptJWE(&serverKey.PublicKey, []byte(middleJWS), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrNestedDepth) {
		t.Fatalf("JWE(JWS(JWS)): expected ErrNestedDepth, got %v", err)
	}

	nestedJWE := encryptJWE(&serverKey.PublicKey, []byte(innermost), defaultJWEOptions(serverKID))
	doubleEnvelope := encryptJWE(&serverKey.PublicKey, []byte(nestedJWE), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(doubleEnvelope); !errors.Is(err, nestedjwt.ErrNestedDepth) {
		t.Fatalf("JWE(JWE(JWS)): expected ErrNestedDepth, got %v", err)
	}
}
