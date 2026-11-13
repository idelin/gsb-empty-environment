package netsec

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
)

func TestQ0156VerifyEmbeddedPayload(t *testing.T) {
	key := []byte("a sufficiently long test key")
	payload := []byte(`{"sub":"alice"}`)

	compact, err := Sign(payload, key, WithSignContentType("application/json"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	token, err := Verify(compact, key, WithContentType("application/json"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if token.Detached {
		t.Fatal("embedded token was reported detached")
	}
	if string(token.Payload) != string(payload) {
		t.Fatalf("payload = %q, want %q", token.Payload, payload)
	}

	if _, err := Verify(compact, key, "application/json"); err != nil {
		t.Fatalf("Verify with positional content type: %v", err)
	}
}

func TestQ0156VerifyDetachedPayload(t *testing.T) {
	key := []byte("a sufficiently long test key")
	payload := []byte(`{"sub":"alice"}`)

	compact, err := SignDetached(payload, key, WithSignContentType("application/json"))
	if err != nil {
		t.Fatalf("SignDetached: %v", err)
	}
	if !strings.Contains(compact, "..") {
		t.Fatalf("detached token does not have an empty payload segment: %s", compact)
	}

	token, err := VerifyDetached(compact, payload, "application/json", key)
	if err != nil {
		t.Fatalf("VerifyDetached: %v", err)
	}
	if !token.Detached {
		t.Fatal("detached token was not reported detached")
	}
	if string(token.Payload) != string(payload) {
		t.Fatalf("payload = %q, want %q", token.Payload, payload)
	}

	if _, err := VerifyDetached(compact, payload, key, "application/json"); err != nil {
		t.Fatalf("VerifyDetached with type after key: %v", err)
	}

	if _, err := Verify(compact, key, true, payload, "application/json"); err != nil {
		t.Fatalf("Verify with detached boolean flag: %v", err)
	}

	if _, err := Verify(compact, key, VerifyOptions{Detached: true}, payload, "application/json"); err != nil {
		t.Fatalf("Verify with detached option and positional payload: %v", err)
	}

	if _, err := Verify(compact, true, payload, "application/json", key); err != nil {
		t.Fatalf("Verify with detached flag first: %v", err)
	}

	if _, err := VerifyDetached(compact, true, payload, "application/json", key); err != nil {
		t.Fatalf("VerifyDetached with flag: %v", err)
	}
}

func TestQ0156DetachedPayloadMustBeExplicit(t *testing.T) {
	key := []byte("a sufficiently long test key")
	payload := []byte(`{"sub":"alice"}`)

	compact, err := SignDetached(payload, key, WithSignContentType("application/json"))
	if err != nil {
		t.Fatalf("SignDetached: %v", err)
	}

	// Supplying an external payload without declaring detached mode must not
	// silently switch the verifier into external-payload mode.
	if _, err := Verify(compact, key, WithDetachedPayload(payload)); err != nil {
		t.Fatalf("WithDetachedPayload declares detached mode: %v", err)
	}

	_, err = Verify(compact, key, VerifyOptions{DetachedPayload: payload})
	if !errors.Is(err, ErrDetachedNotDeclared) {
		t.Fatalf("Verify without detached flag: got %v, want %v", err, ErrDetachedNotDeclared)
	}
	if _, err := Verify(compact, key, payload); !errors.Is(err, ErrDetachedNotDeclared) {
		t.Fatalf("Verify with positional external payload: got %v, want %v", err, ErrDetachedNotDeclared)
	}

	// A detached-shaped token is not verified with an implicit empty payload.
	_, err = Verify(compact, key)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Verify detached token without external payload: got %v, want invalid signature", err)
	}
}

func TestQ0156DetachedTokenRejectsEmbeddedPayload(t *testing.T) {
	key := []byte("a sufficiently long test key")
	embedded := []byte(`{"sub":"mallory"}`)
	external := []byte(`{"sub":"alice"}`)

	compact, err := Sign(embedded, key, WithSignContentType("application/json"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	for _, supplied := range [][]byte{external, embedded} {
		_, err := VerifyDetached(compact, supplied, "application/json", key)
		if !errors.Is(err, ErrEmbeddedPayload) {
			t.Fatalf("VerifyDetached with embedded payload: got %v, want %v", err, ErrEmbeddedPayload)
		}
	}

	// Even a valid detached signature (computed over external) must not make
	// a token with a non-empty payload segment acceptable.
	detached, err := SignDetached(external, key, WithSignContentType("application/json"))
	if err != nil {
		t.Fatalf("SignDetached: %v", err)
	}
	first := strings.IndexByte(detached, '.')
	last := strings.LastIndexByte(detached, '.')
	tampered := detached[:first+1] + base64.RawURLEncoding.EncodeToString(external) + detached[last:]
	if _, err := VerifyDetached(tampered, external, "application/json", key); !errors.Is(err, ErrEmbeddedPayload) {
		t.Fatalf("valid detached signature with embedded payload: got %v, want %v", err, ErrEmbeddedPayload)
	}
}

func TestQ0156DetachedContentTypeMustMatch(t *testing.T) {
	key := []byte("a sufficiently long test key")
	payload := []byte(`{"sub":"alice"}`)

	compact, err := SignDetached(payload, key, WithSignContentType("application/json"))
	if err != nil {
		t.Fatalf("SignDetached: %v", err)
	}

	if _, err := VerifyDetached(compact, payload, "application/cbor", key); !errors.Is(err, ErrContentTypeMismatch) {
		t.Fatalf("type mismatch: got %v, want %v", err, ErrContentTypeMismatch)
	}

	noType, err := SignDetached(payload, key)
	if err != nil {
		t.Fatalf("SignDetached without cty: %v", err)
	}
	if _, err := VerifyDetached(noType, payload, "application/json", key); !errors.Is(err, ErrMissingContentType) {
		t.Fatalf("missing type hint: got %v, want %v", err, ErrMissingContentType)
	}
	if _, err := VerifyDetached(noType, payload, key, WithRequireContentType(true)); !errors.Is(err, ErrMissingContentType) {
		t.Fatalf("required type hint: got %v, want %v", err, ErrMissingContentType)
	}
}

func TestQ0156ContentTypeParametersAndCase(t *testing.T) {
	key := []byte("a sufficiently long test key")
	payload := []byte("payload")

	compact, err := SignDetached(payload, key, WithSignContentType("Application/JSON; Charset=UTF-8"))
	if err != nil {
		t.Fatalf("SignDetached: %v", err)
	}
	if _, err := VerifyDetached(compact, payload, "application/json; charset=utf-8", key); err != nil {
		t.Fatalf("equivalent media types: %v", err)
	}
	if _, err := VerifyDetached(compact, payload, "application/json", key); !errors.Is(err, ErrContentTypeMismatch) {
		t.Fatalf("different parameters: got %v, want %v", err, ErrContentTypeMismatch)
	}
}

func TestQ0156NotDetachedVerifiesOwnPayload(t *testing.T) {
	key := []byte("a sufficiently long test key")
	embedded := []byte("embedded payload")
	external := []byte("external payload")

	compact, err := Sign(embedded, key, WithSignContentType("text/plain"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	token, err := Verify(compact, key)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if string(token.Payload) != string(embedded) {
		t.Fatalf("payload = %q, want embedded %q", token.Payload, embedded)
	}

	// The verifier must not use the external payload or try to reinterpret
	// the token as detached.
	if _, err := Verify(compact, key, WithDetachedPayload(external)); !errors.Is(err, ErrEmbeddedPayload) {
		t.Fatalf("explicit detached mode with embedded payload: got %v, want %v", err, ErrEmbeddedPayload)
	}
}

func TestQ0156B64FalseDetached(t *testing.T) {
	key := []byte("a sufficiently long test key")
	payload := []byte("unencoded payload")

	compact, err := SignDetached(payload, key, WithSignB64(false), WithSignContentType("application/octet-stream"))
	if err != nil {
		t.Fatalf("SignDetached: %v", err)
	}

	if _, err := VerifyDetached(compact, payload, "application/octet-stream", key); err != nil {
		t.Fatalf("VerifyDetached b64=false: %v", err)
	}
	if _, err := VerifyDetached(compact, []byte("tampered"), "application/octet-stream", key); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered b64=false payload: got %v, want %v", err, ErrInvalidSignature)
	}
}

func TestQ0156B64FalseEmbeddedPayloadWithDots(t *testing.T) {
	key := []byte("a sufficiently long test key")
	payload := []byte("one.two.three")

	compact, err := Sign(payload, key, WithSignB64(false))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	token, err := Verify(compact, key)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if string(token.Payload) != string(payload) {
		t.Fatalf("payload = %q, want %q", token.Payload, payload)
	}
}

func TestQ0156AlgorithmPolicyAndConfusion(t *testing.T) {
	key := []byte("a sufficiently long test key")
	compact, err := Sign([]byte("payload"), key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := Verify(compact, key, WithAlgorithms("RS256")); !errors.Is(err, ErrAlgorithmNotAllowed) {
		t.Fatalf("algorithm policy: got %v, want %v", err, ErrAlgorithmNotAllowed)
	}

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	confused, err := Sign([]byte("payload"), rsaKey)
	if err != nil {
		t.Fatalf("Sign RSA: %v", err)
	}
	if _, err := Verify(confused, &rsaKey.PublicKey); err != nil {
		t.Fatalf("Verify RSA: %v", err)
	}

	// An asymmetric key must never be silently treated as an HMAC secret.
	pemBytes := q0156PEMEncodePublicKey(t, &rsaKey.PublicKey)
	if _, err := Verify(compact, pemBytes); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("PEM key used as HMAC secret: got %v, want %v", err, ErrInvalidKey)
	}
}

func TestQ0156AsymmetricAlgorithmsRoundTrip(t *testing.T) {
	payload := []byte("asymmetric payload")

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	for _, algorithm := range []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512"} {
		compact, err := Sign(payload, rsaKey, WithSignAlgorithm(algorithm))
		if err != nil {
			t.Fatalf("Sign %s: %v", algorithm, err)
		}
		if _, err := Verify(compact, &rsaKey.PublicKey); err != nil {
			t.Fatalf("Verify %s: %v", algorithm, err)
		}
	}

	for _, test := range []struct {
		curve     elliptic.Curve
		algorithm string
	}{
		{elliptic.P256(), "ES256"},
		{elliptic.P384(), "ES384"},
		{elliptic.P521(), "ES512"},
	} {
		key, err := ecdsa.GenerateKey(test.curve, rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey %s: %v", test.algorithm, err)
		}
		compact, err := Sign(payload, key)
		if err != nil {
			t.Fatalf("Sign %s: %v", test.algorithm, err)
		}
		if _, err := Verify(compact, &key.PublicKey); err != nil {
			t.Fatalf("Verify %s: %v", test.algorithm, err)
		}
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey Ed25519: %v", err)
	}
	compact, err := Sign(payload, privateKey)
	if err != nil {
		t.Fatalf("Sign EdDSA: %v", err)
	}
	if _, err := Verify(compact, publicKey); err != nil {
		t.Fatalf("Verify EdDSA: %v", err)
	}
}

func TestQ0156MalformedHeaders(t *testing.T) {
	key := []byte("a sufficiently long test key")

	duplicate := q0156Base64URL(`{"alg":"HS256","alg":"HS256"}`) + "." + q0156Base64URL("payload") + "." + q0156Base64URL("signature")
	if _, err := Verify(duplicate, key); !errors.Is(err, ErrMalformed) {
		t.Fatalf("duplicate alg: got %v, want malformed", err)
	}

	missingAlg := q0156Base64URL(`{"typ":"JWT"}`) + "." + q0156Base64URL("payload") + "." + q0156Base64URL("signature")
	if _, err := Verify(missingAlg, key); !errors.Is(err, ErrMissingAlgorithm) {
		t.Fatalf("missing alg: got %v, want %v", err, ErrMissingAlgorithm)
	}

	unknownCritical := q0156Base64URL(`{"alg":"HS256","crit":["exp"]}`) + "." + q0156Base64URL("payload") + "." + q0156Base64URL("signature")
	if _, err := Verify(unknownCritical, key); !errors.Is(err, ErrUnsupportedCritical) {
		t.Fatalf("unknown crit: got %v, want %v", err, ErrUnsupportedCritical)
	}
}

func TestQ0156KeyFunc(t *testing.T) {
	key := []byte("a sufficiently long test key")
	compact, err := Sign([]byte("payload"), key, WithSignKeyID("key-1"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	keyFunc := func(header Header) (any, error) {
		if header.Kid != "key-1" {
			return nil, errors.New("unexpected kid")
		}
		return key, nil
	}
	if _, err := Verify(compact, WithKeyFunc(keyFunc)); err != nil {
		t.Fatalf("Verify with KeyFunc: %v", err)
	}
}

func TestQ0156Verifier(t *testing.T) {
	key := []byte("a sufficiently long test key")
	payload := []byte("payload")

	compact, err := SignDetached(payload, key, WithSignContentType("text/plain"))
	if err != nil {
		t.Fatalf("SignDetached: %v", err)
	}

	verifier := NewVerifier(key)
	if _, err := verifier.VerifyDetached(compact, payload, "text/plain"); err != nil {
		t.Fatalf("Verifier.VerifyDetached: %v", err)
	}
}

func TestQ0156ParseDetachedThenVerify(t *testing.T) {
	key := []byte("a sufficiently long test key")
	payload := []byte("payload")

	compact, err := SignDetached(payload, key, WithSignContentType("text/plain"))
	if err != nil {
		t.Fatalf("SignDetached: %v", err)
	}

	token, err := ParseDetached(compact, payload, "text/plain")
	if err != nil {
		t.Fatalf("ParseDetached: %v", err)
	}
	if !token.Detached {
		t.Fatal("ParseDetached did not mark token detached")
	}
	if err := token.Verify(key); err != nil {
		t.Fatalf("Token.Verify: %v", err)
	}
}

func TestQ0156DetachedSigningInputMatchesRFC7515(t *testing.T) {
	key := []byte("known test key")
	payload := []byte("hello")
	headerJSON := []byte(`{"alg":"HS256","cty":"text/plain"}`)
	protected := base64.RawURLEncoding.EncodeToString(headerJSON)

	// RFC 7515 detached signing input: protected || '.' || base64url(payload).
	signingInput := protected + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingInput))
	compact := protected + ".." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	token, err := VerifyDetached(compact, payload, "text/plain", key)
	if err != nil {
		t.Fatalf("VerifyDetached: %v", err)
	}
	if string(token.Payload) != "hello" {
		t.Fatalf("payload = %q", token.Payload)
	}

	// RFC 7797: with b64=false the raw payload is part of the signing input.
	headerJSON = []byte(`{"alg":"HS256","cty":"text/plain","b64":false,"crit":["b64"]}`)
	protected = base64.RawURLEncoding.EncodeToString(headerJSON)
	signingInput = protected + ".hello"
	mac = hmac.New(sha256.New, key)
	mac.Write([]byte(signingInput))
	compact = protected + ".." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if _, err := VerifyDetached(compact, payload, "text/plain", key); err != nil {
		t.Fatalf("VerifyDetached b64=false: %v", err)
	}
}

func q0156Base64URL(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func q0156PEMEncodePublicKey(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}
