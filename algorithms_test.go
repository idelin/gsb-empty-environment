package netsec_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"hash"
	"strings"
	"testing"

	netsec "example.org/netsec/q0120"
)

func TestRSASignatureAlgorithms(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, algorithm := range []string{"RS256", "RS384", "PS256", "PS384"} {
		t.Run(algorithm, func(t *testing.T) {
			verifier, err := netsec.NewVerifier(credentialEndpoint,
				netsec.WithVerificationKey(&key.PublicKey),
				netsec.WithAllowedAlgorithms(algorithm),
			)
			if err != nil {
				t.Fatal(err)
			}
			nonce := issueNonce(t, verifier)
			proof := signWithRSA(t, algorithm, key, claims(credentialEndpoint, nonce))
			if err := verifier.ValidateProof(proof); err != nil {
				t.Fatalf("%s proof rejected: %v", algorithm, err)
			}
		})
	}
}

func TestES384Signature(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := netsec.NewVerifier(credentialEndpoint, netsec.WithPublicKey(&key.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	nonce := issueNonce(t, verifier)
	proof := signWithECDSA(t, "ES384", key, claims(credentialEndpoint, nonce))
	if err := verifier.ValidateProof(proof); err != nil {
		t.Fatalf("ES384 proof rejected: %v", err)
	}
}

func TestEdDSASignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := netsec.NewVerifier(credentialEndpoint, netsec.WithVerificationKey(publicKey))
	if err != nil {
		t.Fatal(err)
	}
	nonce := issueNonce(t, verifier)
	header := map[string]any{"alg": "EdDSA", "typ": "openid4vci-proof+jwt"}
	signingInput := encodeJSON(t, header) + "." + encodeJSON(t, claims(credentialEndpoint, nonce))
	signature := ed25519.Sign(privateKey, []byte(signingInput))
	proof := signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
	if err := verifier.ValidateProof(proof); err != nil {
		t.Fatalf("EdDSA proof rejected: %v", err)
	}
}

func TestConfiguredJWKWithStringKeyOperations(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwk := map[string]any{
		"kty":     "EC",
		"crv":     "P-256",
		"x":       base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))),
		"y":       base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32))),
		"use":     "sig",
		"key_ops": []string{"verify"},
	}
	verifier, err := netsec.NewVerifier(credentialEndpoint, netsec.WithVerificationKey(jwk))
	if err != nil {
		t.Fatal(err)
	}
	nonce := issueNonce(t, verifier)
	proof := signES256(t, key, nil, claims(credentialEndpoint, nonce))
	if err := verifier.ValidateProof(proof); err != nil {
		t.Fatalf("JWK-configured proof rejected: %v", err)
	}
}

func signWithRSA(t *testing.T, algorithm string, key *rsa.PrivateKey, payload map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": algorithm, "typ": "openid4vci-proof+jwt"}
	signingInput := encodeJSON(t, header) + "." + encodeJSON(t, payload)
	hashAlgorithm := testHash(algorithm)
	digest := hashInput(hashAlgorithm, []byte(signingInput))

	var signature []byte
	var err error
	if strings.HasPrefix(algorithm, "PS") {
		signature, err = rsa.SignPSS(rand.Reader, key, hashAlgorithm, digest, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       hashAlgorithm,
		})
	} else {
		signature, err = rsa.SignPKCS1v15(rand.Reader, key, hashAlgorithm, digest)
	}
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func signWithECDSA(t *testing.T, algorithm string, key *ecdsa.PrivateKey, payload map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": algorithm, "typ": "openid4vci-proof+jwt"}
	signingInput := encodeJSON(t, header) + "." + encodeJSON(t, payload)
	digest := hashInput(testHash(algorithm), []byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest)
	if err != nil {
		t.Fatal(err)
	}
	size := (key.Curve.Params().BitSize + 7) / 8
	signature := make([]byte, 2*size)
	r.FillBytes(signature[:size])
	s.FillBytes(signature[size:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func testHash(algorithm string) crypto.Hash {
	if strings.HasSuffix(algorithm, "384") {
		return crypto.SHA384
	}
	return crypto.SHA256
}

func hashInput(algorithmHash crypto.Hash, value []byte) []byte {
	var hasher hash.Hash
	if algorithmHash == crypto.SHA384 {
		hasher = sha512.New384()
	} else {
		hasher = sha256.New()
	}
	hasher.Write(value)
	return hasher.Sum(nil)
}
