package netsec_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"testing"

	netsec "example.org/netsec/q0148"
)

func TestCompactProtectedHeaderOnly(t *testing.T) {
	secret := []byte("correct horse battery staple")
	payload := []byte(`{"sub":"alice"}`)
	token := compactToken(t, map[string]any{"alg": "HS256", "kid": "good"}, payload, "HS256", secret)

	var gotKid, gotAlg string
	resolver := netsec.KeyFunc(func(kid, alg string) (any, error) {
		gotKid, gotAlg = kid, alg
		return secret, nil
	})

	got, err := netsec.Verify(token, resolver)
	if err != nil {
		t.Fatalf("Verify() failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
	if gotKid != "good" || gotAlg != "HS256" {
		t.Fatalf("resolver saw kid=%q alg=%q", gotKid, gotAlg)
	}

	result, err := netsec.VerifyResult(token, resolver)
	if err != nil {
		t.Fatalf("VerifyResult() failed: %v", err)
	}
	if result.Algorithm != "HS256" || result.KeyID != "good" {
		t.Fatalf("result alg=%q kid=%q", result.Algorithm, result.KeyID)
	}
	if result.ProtectedHeader.Algorithm != "HS256" || result.ProtectedHeader.KeyID != "good" {
		t.Fatalf("protected header was not exposed correctly")
	}

	parsed, err := netsec.ParseSigned(token, []string{"HS256"})
	if err != nil {
		t.Fatalf("ParseSigned() failed: %v", err)
	}
	got, err = parsed.Verify(resolver)
	if err != nil {
		t.Fatalf("Token.Verify() failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Token.Verify() payload = %q, want %q", got, payload)
	}
}

func TestUnprotectedOnlyAlgorithmIsIgnored(t *testing.T) {
	secret := []byte("secret")
	payload := []byte("payload")
	protected := map[string]any{}
	unprotected := map[string]any{"alg": "HS256", "kid": "good"}
	token := flattenedToken(t, protected, unprotected, payload, "HS256", secret)

	called := false
	_, err := netsec.VerifyJSON(token, netsec.KeyFunc(func(kid, alg string) (any, error) {
		called = true
		return secret, nil
	}))
	if err == nil {
		t.Fatal("VerifyJSON() accepted a token with alg only in the unprotected header")
	}
	if !errors.Is(err, netsec.ErrAlgorithmMissing) {
		t.Fatalf("error = %v, want ErrAlgorithmMissing", err)
	}
	if called {
		t.Fatal("key resolver was called without a protected alg")
	}
}

func TestUnprotectedOnlyKeyIDIsNotPassedToResolver(t *testing.T) {
	secret := []byte("secret")
	payload := []byte("payload")
	protected := map[string]any{"alg": "HS256"}
	unprotected := map[string]any{"kid": "attacker"}
	token := flattenedToken(t, protected, unprotected, payload, "HS256", secret)

	var gotKid string
	got, err := netsec.VerifyJSON(token, netsec.KeyFunc(func(kid, alg string) (any, error) {
		gotKid = kid
		if kid != "" {
			return nil, fmt.Errorf("unexpected unprotected kid %q", kid)
		}
		return secret, nil
	}))
	if err != nil {
		t.Fatalf("VerifyJSON() failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
	if gotKid != "" {
		t.Fatalf("resolver saw unprotected kid %q", gotKid)
	}
}

func TestConflictingAlgorithmFailsBeforeKeyResolution(t *testing.T) {
	secret := []byte("secret")
	payload := []byte("payload")
	protected := map[string]any{"alg": "HS256", "kid": "good"}
	unprotected := map[string]any{"alg": "HS512", "kid": "good"}
	token := flattenedToken(t, protected, unprotected, payload, "HS256", secret)

	called := false
	_, err := netsec.VerifyJSON(token, netsec.KeyFunc(func(kid, alg string) (any, error) {
		called = true
		return secret, nil
	}))
	if err == nil {
		t.Fatal("VerifyJSON() accepted conflicting protected and unprotected alg values")
	}
	if !errors.Is(err, netsec.ErrUnprotectedHeaderConflict) {
		t.Fatalf("error = %v, want ErrUnprotectedHeaderConflict", err)
	}
	if called {
		t.Fatal("key resolver was called despite a header conflict")
	}
}

func TestConflictingKeyIDFails(t *testing.T) {
	secret := []byte("secret")
	payload := []byte("payload")
	protected := map[string]any{"alg": "HS256", "kid": "good"}
	unprotected := map[string]any{"kid": "attacker"}
	token := flattenedToken(t, protected, unprotected, payload, "HS256", secret)

	_, err := netsec.VerifyJSON(token, netsec.KeyFunc(func(kid, alg string) (any, error) {
		return secret, nil
	}))
	if err == nil {
		t.Fatal("VerifyJSON() accepted conflicting protected and unprotected kid values")
	}
	if !errors.Is(err, netsec.ErrUnprotectedHeaderConflict) {
		t.Fatalf("error = %v, want ErrUnprotectedHeaderConflict", err)
	}
}

func TestEqualUnprotectedValuesAreIgnored(t *testing.T) {
	secret := []byte("secret")
	payload := []byte("payload")
	protected := map[string]any{"alg": "HS256", "kid": "good"}
	unprotected := map[string]any{"alg": "HS256", "kid": "good", "typ": "JWT"}
	token := flattenedToken(t, protected, unprotected, payload, "HS256", secret)

	var gotKid, gotAlg string
	got, err := netsec.VerifyJSON(token, netsec.KeyFunc(func(kid, alg string) (any, error) {
		gotKid, gotAlg = kid, alg
		return secret, nil
	}))
	if err != nil {
		t.Fatalf("VerifyJSON() failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
	if gotKid != "good" || gotAlg != "HS256" {
		t.Fatalf("resolver saw kid=%q alg=%q", gotKid, gotAlg)
	}
}

func TestDuplicateProtectedHeaderParameterIsRejected(t *testing.T) {
	secret := []byte("secret")
	payload := []byte("payload")
	token := compactTokenWithRawProtected(t,
		`{"alg":"HS256","alg":"HS512","kid":"good"}`,
		payload,
		"HS256",
		secret,
	)

	_, err := netsec.Verify(token, secret)
	if err == nil {
		t.Fatal("Verify() accepted a protected header with duplicate alg values")
	}
	if !errors.Is(err, netsec.ErrMalformedToken) {
		t.Fatalf("error = %v, want ErrMalformedToken", err)
	}
}

func TestNoneIsRejected(t *testing.T) {
	payload := []byte("payload")
	protected := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"good"}`))
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payload)
	token := protected + "." + payloadEncoded + "."

	called := false
	_, err := netsec.Verify(token, netsec.KeyFunc(func(kid, alg string) (any, error) {
		called = true
		return []byte("secret"), nil
	}))
	if err == nil {
		t.Fatal("Verify() accepted alg=none")
	}
	if !errors.Is(err, netsec.ErrUnsupportedAlgorithm) {
		t.Fatalf("error = %v, want ErrUnsupportedAlgorithm", err)
	}
	if called {
		t.Fatal("key resolver was called for alg=none")
	}
}

func TestAlgorithmConfusionIsRejected(t *testing.T) {
	payload := []byte("payload")
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	hsToken := compactToken(t, map[string]any{"alg": "HS256", "kid": "good"}, payload, "HS256", []byte("secret"))
	_, err = netsec.Verify(hsToken, &rsaKey.PublicKey)
	if err == nil {
		t.Fatal("Verify() accepted an RSA public key for HS256")
	}
	if !errors.Is(err, netsec.ErrInvalidKey) {
		t.Fatalf("HS256 with RSA key error = %v, want ErrInvalidKey", err)
	}

	rsToken := compactToken(t, map[string]any{"alg": "RS256", "kid": "good"}, payload, "RS256", rsaKey)
	_, err = netsec.Verify(rsToken, []byte("secret"))
	if err == nil {
		t.Fatal("Verify() accepted an HMAC secret for RS256")
	}
	if !errors.Is(err, netsec.ErrInvalidKey) {
		t.Fatalf("RS256 with HMAC key error = %v, want ErrInvalidKey", err)
	}

	p384Key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	es256Token := compactToken(t, map[string]any{"alg": "ES256", "kid": "good"}, payload, "ES256", mustECKey(t, elliptic.P256()))
	_, err = netsec.Verify(es256Token, &p384Key.PublicKey)
	if err == nil {
		t.Fatal("Verify() accepted a P-384 key for ES256")
	}
	if !errors.Is(err, netsec.ErrInvalidKey) {
		t.Fatalf("ES256 with P-384 key error = %v, want ErrInvalidKey", err)
	}

	edToken := compactToken(t, map[string]any{"alg": "EdDSA", "kid": "good"}, payload, "EdDSA", ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)))
	_, err = netsec.Verify(edToken, ed25519.PublicKey{1, 2, 3})
	if err == nil {
		t.Fatal("Verify() accepted a malformed Ed25519 public key")
	}
	if !errors.Is(err, netsec.ErrInvalidKey) {
		t.Fatalf("EdDSA with short key error = %v, want ErrInvalidKey", err)
	}
}

func TestAlgorithmMatrix(t *testing.T) {
	payload := []byte(`{"hello":"world"}`)
	secret := []byte("matrix secret")

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p256Key := mustECKey(t, elliptic.P256())
	p384Key := mustECKey(t, elliptic.P384())
	p521Key := mustECKey(t, elliptic.P521())
	edPublic, edPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		alg    string
		signer any
		verify any
	}{
		{"HS256", secret, secret},
		{"HS384", secret, secret},
		{"HS512", secret, secret},
		{"RS256", rsaKey, &rsaKey.PublicKey},
		{"RS384", rsaKey, &rsaKey.PublicKey},
		{"RS512", rsaKey, &rsaKey.PublicKey},
		{"PS256", rsaKey, &rsaKey.PublicKey},
		{"PS384", rsaKey, &rsaKey.PublicKey},
		{"PS512", rsaKey, &rsaKey.PublicKey},
		{"ES256", p256Key, &p256Key.PublicKey},
		{"ES384", p384Key, &p384Key.PublicKey},
		{"ES512", p521Key, &p521Key.PublicKey},
		{"EdDSA", edPrivate, edPublic},
	}

	for _, test := range tests {
		t.Run(test.alg, func(t *testing.T) {
			token := compactToken(t, map[string]any{"alg": test.alg, "kid": "key-1"}, payload, test.alg, test.signer)
			got, err := netsec.Verify(token, test.verify)
			if err != nil {
				t.Fatalf("Verify() failed: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("payload = %q, want %q", got, payload)
			}
		})
	}
}

func TestGeneralJSONSerializationWithMultipleSignatures(t *testing.T) {
	payload := []byte("multi-signature payload")
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payload)

	protected1 := map[string]any{"alg": "HS256", "kid": "one"}
	protected2 := map[string]any{"alg": "HS256", "kid": "two"}
	signature1 := signJSONSignature(t, protected1, nil, payloadEncoded, "HS256", []byte("secret-one"))
	signature2 := signJSONSignature(t, protected2, nil, payloadEncoded, "HS256", []byte("secret-two"))

	token, err := json.Marshal(map[string]any{
		"payload":    payloadEncoded,
		"signatures": []any{signature1, signature2},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := netsec.VerifyJSON(token, map[string]any{
		"one": []byte("secret-one"),
		"two": []byte("secret-two"),
	})
	if err != nil {
		t.Fatalf("VerifyJSON() failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

func TestDetachedPayload(t *testing.T) {
	payload := []byte("detached payload")
	protected := map[string]any{"alg": "HS256", "kid": "good"}
	protectedJSON, err := json.Marshal(protected)
	if err != nil {
		t.Fatal(err)
	}
	protectedEncoded := base64.RawURLEncoding.EncodeToString(protectedJSON)
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := signBytes(t, "HS256", []byte("secret"), []byte(protectedEncoded+"."+payloadEncoded))

	token, err := json.Marshal(map[string]any{
		"protected": protectedEncoded,
		"signature": base64.RawURLEncoding.EncodeToString(signature),
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := netsec.VerifyDetached(token, payload, []byte("secret"))
	if err != nil {
		t.Fatalf("VerifyDetached() failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

func TestResolverForms(t *testing.T) {
	payload := []byte("payload")
	secret := []byte("secret")
	token := compactToken(t, map[string]any{"alg": "HS256", "kid": "good"}, payload, "HS256", secret)

	headerResolver := func(header netsec.Header) (any, error) {
		if header.KeyID != "good" || header.Algorithm != "HS256" {
			return nil, fmt.Errorf("unexpected protected header: %+v", header)
		}
		return secret, nil
	}
	contextResolver := func(ctx context.Context, kid, alg string) (any, error) {
		if ctx == nil {
			return nil, errors.New("nil context")
		}
		if kid != "good" || alg != "HS256" {
			return nil, fmt.Errorf("unexpected values: %q %q", kid, alg)
		}
		return secret, nil
	}
	typedResolver := func(kid string) ([]byte, error) {
		if kid != "good" {
			return nil, fmt.Errorf("unexpected kid %q", kid)
		}
		return secret, nil
	}
	keyMap := map[string][]byte{"good": secret}

	resolvers := []any{
		headerResolver,
		contextResolver,
		typedResolver,
		keyMap,
		netsec.KeyLookupFunc(func(kid string) (any, error) { return keyMap[kid], nil }),
	}
	for index, resolver := range resolvers {
		got, err := netsec.Verify(token, resolver)
		if err != nil {
			t.Fatalf("resolver %d failed: %v", index, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("resolver %d payload = %q", index, got)
		}
	}
}

func TestAllowedAlgorithms(t *testing.T) {
	payload := []byte("payload")
	secret := []byte("secret")
	token := compactToken(t, map[string]any{"alg": "HS256", "kid": "good"}, payload, "HS256", secret)

	verifier := &netsec.Verifier{
		Keys:              secret,
		AllowedAlgorithms: []string{"RS256"},
	}
	_, err := verifier.Verify(token)
	if err == nil {
		t.Fatal("Verifier accepted an algorithm outside its allowlist")
	}
	if !errors.Is(err, netsec.ErrAlgorithmNotAllowed) {
		t.Fatalf("error = %v, want ErrAlgorithmNotAllowed", err)
	}
}

func TestRequireProtectedKeyID(t *testing.T) {
	payload := []byte("payload")
	secret := []byte("secret")
	token := compactToken(t, map[string]any{"alg": "HS256"}, payload, "HS256", secret)

	verifier := &netsec.Verifier{Keys: secret, RequireKeyID: true}
	_, err := verifier.Verify(token)
	if err == nil {
		t.Fatal("Verifier accepted a token without a protected kid")
	}
	if !errors.Is(err, netsec.ErrKeyIDRequired) {
		t.Fatalf("error = %v, want ErrKeyIDRequired", err)
	}
}

func TestTamperedSignatureIsRejected(t *testing.T) {
	secret := []byte("secret")
	payload := []byte("payload")
	token := compactToken(t, map[string]any{"alg": "HS256", "kid": "good"}, payload, "HS256", secret)
	parts := bytes.Split([]byte(token), []byte("."))
	if len(parts) != 3 {
		t.Fatal("unexpected compact token shape")
	}
	if parts[1][0] == 'A' {
		parts[1][0] = 'B'
	} else {
		parts[1][0] = 'A'
	}

	_, err := netsec.Verify(string(bytes.Join(parts, []byte("."))), secret)
	if err == nil {
		t.Fatal("Verify() accepted a tampered signature")
	}
	if !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("error = %v, want ErrInvalidSignature", err)
	}
}

func compactToken(t *testing.T, protected map[string]any, payload []byte, alg string, signer any) string {
	t.Helper()
	protectedJSON, err := json.Marshal(protected)
	if err != nil {
		t.Fatal(err)
	}
	return compactTokenWithRawProtected(t, string(protectedJSON), payload, alg, signer)
}

func compactTokenWithRawProtected(t *testing.T, protectedJSON string, payload []byte, alg string, signer any) string {
	t.Helper()
	protectedEncoded := base64.RawURLEncoding.EncodeToString([]byte(protectedJSON))
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := signBytes(t, alg, signer, []byte(protectedEncoded+"."+payloadEncoded))
	return protectedEncoded + "." + payloadEncoded + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func flattenedToken(t *testing.T, protected, unprotected map[string]any, payload []byte, alg string, signer any) []byte {
	t.Helper()
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := signJSONSignature(t, protected, unprotected, payloadEncoded, alg, signer)
	token, err := json.Marshal(map[string]any{
		"payload":   payloadEncoded,
		"protected": signature["protected"],
		"header":    signature["header"],
		"signature": signature["signature"],
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func signJSONSignature(t *testing.T, protected, unprotected map[string]any, payloadEncoded, alg string, signer any) map[string]any {
	t.Helper()
	protectedJSON, err := json.Marshal(protected)
	if err != nil {
		t.Fatal(err)
	}
	protectedEncoded := base64.RawURLEncoding.EncodeToString(protectedJSON)
	signature := signBytes(t, alg, signer, []byte(protectedEncoded+"."+payloadEncoded))

	result := map[string]any{
		"protected": protectedEncoded,
		"signature": base64.RawURLEncoding.EncodeToString(signature),
	}
	if unprotected != nil {
		result["header"] = unprotected
	}
	return result
}

func signBytes(t *testing.T, alg string, key any, input []byte) []byte {
	t.Helper()
	hashType, digest := digestFor(t, alg, input)

	switch alg {
	case "HS256", "HS384", "HS512":
		secret, ok := key.([]byte)
		if !ok {
			t.Fatalf("HMAC signer must be []byte, got %T", key)
		}
		mac := hmac.New(hashConstructor(t, hashType), secret)
		mac.Write(input)
		return mac.Sum(nil)
	case "RS256", "RS384", "RS512":
		privateKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			t.Fatalf("RSA signer must be *rsa.PrivateKey, got %T", key)
		}
		signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, hashType, digest)
		if err != nil {
			t.Fatal(err)
		}
		return signature
	case "PS256", "PS384", "PS512":
		privateKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			t.Fatalf("RSA-PSS signer must be *rsa.PrivateKey, got %T", key)
		}
		signature, err := rsa.SignPSS(rand.Reader, privateKey, hashType, digest, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       hashType,
		})
		if err != nil {
			t.Fatal(err)
		}
		return signature
	case "ES256", "ES384", "ES512":
		privateKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			t.Fatalf("ECDSA signer must be *ecdsa.PrivateKey, got %T", key)
		}
		r, s, err := ecdsa.Sign(rand.Reader, privateKey, digest)
		if err != nil {
			t.Fatal(err)
		}
		size := (privateKey.Curve.Params().BitSize + 7) / 8
		signature := make([]byte, 2*size)
		r.FillBytes(signature[:size])
		s.FillBytes(signature[size:])
		return signature
	case "EdDSA":
		privateKey, ok := key.(ed25519.PrivateKey)
		if !ok {
			t.Fatalf("EdDSA signer must be ed25519.PrivateKey, got %T", key)
		}
		return ed25519.Sign(privateKey, input)
	default:
		t.Fatalf("unsupported test algorithm %q", alg)
		return nil
	}
}

func digestFor(t *testing.T, alg string, input []byte) (crypto.Hash, []byte) {
	t.Helper()
	switch alg {
	case "HS256", "RS256", "PS256", "ES256":
		sum := sha256.Sum256(input)
		return crypto.SHA256, sum[:]
	case "HS384", "RS384", "PS384", "ES384":
		sum := sha512.Sum384(input)
		return crypto.SHA384, sum[:]
	case "HS512", "RS512", "PS512", "ES512":
		sum := sha512.Sum512(input)
		return crypto.SHA512, sum[:]
	case "EdDSA":
		return 0, nil
	default:
		t.Fatalf("unsupported test algorithm %q", alg)
		return 0, nil
	}
}

func hashConstructor(t *testing.T, hashType crypto.Hash) func() hash.Hash {
	t.Helper()
	switch hashType {
	case crypto.SHA256:
		return sha256.New
	case crypto.SHA384:
		return sha512.New384
	case crypto.SHA512:
		return sha512.New
	default:
		t.Fatalf("unsupported hash %v", hashType)
		return nil
	}
}

func mustECKey(t *testing.T, curve elliptic.Curve) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
