package nestedjwt_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"formal.local/w28-5b20ffbc87/nestedjwt"
)

func TestMalformedProtectedHeaderAndPayload(t *testing.T) {
	verifier, serverKey, clientEC := newHarness(t)
	goodToken := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-good"), defaultJWEOptions(serverKID))

	// Replace the outer protected header with non-base64url content.
	segments := strings.Split(goodToken, ".")
	badHeader := make([]string, len(segments))
	copy(badHeader, segments)
	badHeader[0] = "!!!not-base64!!!"
	if _, err := verifier.Verify(strings.Join(badHeader, ".")); !errors.Is(err, nestedjwt.ErrProtectedHeader) {
		t.Fatalf("non-base64 outer header: expected ErrProtectedHeader, got %v", err)
	}

	// Replace the outer protected header with base64url that is not JSON.
	badHeader[0] = testB64.EncodeToString([]byte("not-json"))
	if _, err := verifier.Verify(strings.Join(badHeader, ".")); !errors.Is(err, nestedjwt.ErrProtectedHeader) {
		t.Fatalf("non-JSON outer header: expected ErrProtectedHeader, got %v", err)
	}

	// Inner JWS whose payload is not a JSON object must not be accepted as
	// claims even though the signature is valid.
	jws := signJWS("ES256", clientKID, clientEC, []byte("plain-text-payload"))
	token := encryptJWE(&serverKey.PublicKey, []byte(jws), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrPayloadNotJSON) {
		t.Fatalf("non-JSON inner payload: expected ErrPayloadNotJSON, got %v", err)
	}

	// Whitespace around the compact token is rejected rather than trimmed.
	if _, err := verifier.Verify(" " + goodToken); !errors.Is(err, nestedjwt.ErrMalformedToken) {
		t.Fatalf("leading whitespace: expected ErrMalformedToken, got %v", err)
	}

	// A protected header whose alg has the wrong JSON type.
	opts := defaultJWEOptions(serverKID)
	opts.extraHeaders = map[string]any{}
	token = buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-types"), opts)
	typedSegments := strings.Split(token, ".")
	rawHeader, err := testB64.DecodeString(typedSegments[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(rawHeader, &generic); err != nil {
		t.Fatalf("parse header: %v", err)
	}
	generic["alg"] = 12345
	typedSegments[0] = b64JSON(generic)
	if _, err := verifier.Verify(strings.Join(typedSegments, ".")); !errors.Is(err, nestedjwt.ErrProtectedHeader) {
		t.Fatalf("wrong-typed alg: expected ErrProtectedHeader, got %v", err)
	}
}

func TestOversizedTokenRejected(t *testing.T) {
	policy := nestedjwt.DefaultPolicy()
	policy.MaxTokenSize = 64
	verifier, serverKey, clientEC := newHarness(t, nestedjwt.WithPolicy(policy))

	token := buildNested(serverKey, clientEC, sampleClaims("issuer-a", "jti-big"), defaultJWEOptions(serverKID))
	if _, err := verifier.Verify(token); !errors.Is(err, nestedjwt.ErrMalformedToken) {
		t.Fatalf("oversize token: expected ErrMalformedToken, got %v", err)
	}
}
