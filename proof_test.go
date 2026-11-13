package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
)

func TestVerifyProofBindingAcceptsMatchingThumbprint(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	for _, alg := range []string{"RS256", "PS256"} {
		t.Run(alg, func(t *testing.T) {
			proof := signRSAProof(t, rsaKey, alg)
			cnf, err := ConfirmationKeyFromVerifiedProof(proof)
			if err != nil {
				t.Fatalf("ConfirmationKeyFromVerifiedProof returned error: %v", err)
			}
			if cnf.JKT == "" {
				t.Fatal("expected non-empty cnf.jkt")
			}
			if err := VerifyProofBinding(proof, cnf); err != nil {
				t.Fatalf("VerifyProofBinding returned error: %v", err)
			}
		})
	}
}

func TestVerifyProofBindingRejectsEmptyConfirmationKey(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	err = VerifyProofBinding(signECProof(t, key, "ES256"), ConfirmationKey{})
	if !errors.Is(err, ErrInvalidProof) {
		t.Fatalf("expected ErrInvalidProof, got %v", err)
	}
}

func TestVerifyProofBindingRejectsDifferentConfirmationKey(t *testing.T) {
	signingKey, err := ecdsa.GenerateKey(ellipticP256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := ecdsa.GenerateKey(ellipticP256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	proof := signECProof(t, signingKey, "ES256")
	verifiedCNF, err := ConfirmationKeyFromVerifiedProof(proof)
	if err != nil {
		t.Fatalf("valid proof was rejected: %v", err)
	}

	otherJWK, err := json.Marshal(publicECJWK(otherKey.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	otherJKT, err := ThumbprintJWK(otherJWK)
	if err != nil {
		t.Fatal(err)
	}
	if verifiedCNF.JKT == otherJKT {
		t.Fatal("test setup requires different JWK thumbprints")
	}

	err = VerifyProofBinding(proof, ConfirmationKey{JKT: otherJKT})
	if !errors.Is(err, ErrMismatchedConfirmationKey) {
		t.Fatalf("expected ErrMismatchedConfirmationKey, got %v", err)
	}
}

func TestProofSignatureAlgorithms(t *testing.T) {
	t.Run("ECDSA", func(t *testing.T) {
		key, err := ecdsa.GenerateKey(ellipticP256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		proof := signECProof(t, key, "ES256")
		if _, err := ConfirmationKeyFromVerifiedProof(proof); err != nil {
			t.Fatalf("valid ES256 proof was rejected: %v", err)
		}
	})

	t.Run("Ed25519", func(t *testing.T) {
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		proof := signEd25519Proof(t, publicKey, privateKey)
		if _, err := ConfirmationKeyFromVerifiedProof(proof); err != nil {
			t.Fatalf("valid EdDSA proof was rejected: %v", err)
		}
	})
}

func TestVerifyProofRejectsInvalidProofs(t *testing.T) {
	key, err := ecdsa.GenerateKey(ellipticP256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	validProof := signECProof(t, key, "ES256")

	t.Run("tampered claims", func(t *testing.T) {
		parts := splitProof(t, validProof)
		claims, err := decodeBase64URL(parts[1])
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(claims, &payload); err != nil {
			t.Fatal(err)
		}
		payload["tampered"] = true
		parts[1] = encodeSegment(mustJSON(t, payload))

		_, err = ConfirmationKeyFromVerifiedProof(joinProof(parts))
		if !errors.Is(err, ErrInvalidProof) {
			t.Fatalf("expected ErrInvalidProof, got %v", err)
		}
	})

	t.Run("none algorithm", func(t *testing.T) {
		header := proofHeader{
			Alg: "none",
			JWK: mustJSON(t, publicECJWK(key.PublicKey)),
		}
		proof := encodeSegment(mustJSON(t, header)) + "." +
			encodeSegment(mustJSON(t, map[string]string{"htm": "POST"})) + "."

		_, err := ConfirmationKeyFromVerifiedProof(proof)
		if !errors.Is(err, ErrInvalidProof) {
			t.Fatalf("expected ErrInvalidProof, got %v", err)
		}
	})

	t.Run("private JWK", func(t *testing.T) {
		header := map[string]any{
			"alg": "ES256",
			"jwk": map[string]any{
				"kty": "EC",
				"crv": "P-256",
				"x":   base64.RawURLEncoding.EncodeToString(key.PublicKey.X.Bytes()),
				"y":   base64.RawURLEncoding.EncodeToString(key.PublicKey.Y.Bytes()),
				"d":   "private",
			},
		}
		proof := encodeSegment(mustJSON(t, header)) + "." +
			encodeSegment(mustJSON(t, map[string]string{"htm": "POST"})) + "." +
			encodeSegment([]byte("signature"))

		_, err := ConfirmationKeyFromVerifiedProof(proof)
		if !errors.Is(err, ErrInvalidProof) {
			t.Fatalf("expected ErrInvalidProof, got %v", err)
		}
	})

	t.Run("unsupported algorithm", func(t *testing.T) {
		header := proofHeader{
			Alg: "ES999",
			JWK: mustJSON(t, publicECJWK(key.PublicKey)),
		}
		proof := encodeSegment(mustJSON(t, header)) + "." +
			encodeSegment(mustJSON(t, map[string]string{"htm": "POST"})) + "." +
			encodeSegment([]byte("signature"))

		_, err := ConfirmationKeyFromVerifiedProof(proof)
		if !errors.Is(err, ErrUnsupportedAlgorithm) {
			t.Fatalf("expected ErrUnsupportedAlgorithm, got %v", err)
		}
	})
}

func TestThumbprintJWKEd25519RFC8037Vector(t *testing.T) {
	raw := json.RawMessage(`{
		"kty": "OKP",
		"crv": "Ed25519",
		"x": "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"
	}`)

	thumbprint, err := ThumbprintJWK(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k"
	if thumbprint != want {
		t.Fatalf("thumbprint = %q, want %q", thumbprint, want)
	}
}

func signRSAProof(t *testing.T, key *rsa.PrivateKey, alg string) string {
	t.Helper()

	header := proofHeader{
		Alg: alg,
		Typ: "dpop+jwt",
		JWK: mustJSON(t, publicRSAJWK(key.PublicKey)),
	}
	headerSegment := encodeSegment(mustJSON(t, header))
	claimsSegment := encodeSegment(mustJSON(t, map[string]string{"htm": "POST", "htu": "https://issuer.example/token"}))
	signingInput := []byte(headerSegment + "." + claimsSegment)

	hash := rsaHashForTest(t, alg)
	hasher := hash.New()
	_, _ = hasher.Write(signingInput)
	var signature []byte
	var err error
	if strings.HasPrefix(alg, "PS") {
		signature, err = rsa.SignPSS(rand.Reader, key, hash, hasher.Sum(nil), nil)
	} else {
		signature, err = rsa.SignPKCS1v15(rand.Reader, key, hash, hasher.Sum(nil))
	}
	if err != nil {
		t.Fatal(err)
	}

	return headerSegment + "." + claimsSegment + "." + encodeSegment(signature)
}

func signECProof(t *testing.T, key *ecdsa.PrivateKey, alg string) string {
	t.Helper()

	header := proofHeader{
		Alg: alg,
		Typ: "dpop+jwt",
		JWK: mustJSON(t, publicECJWK(key.PublicKey)),
	}
	headerSegment := encodeSegment(mustJSON(t, header))
	claimsSegment := encodeSegment(mustJSON(t, map[string]string{"htm": "POST", "htu": "https://issuer.example/token"}))
	signingInput := []byte(headerSegment + "." + claimsSegment)

	hash, err := hashForAlgorithm(alg)
	if err != nil {
		t.Fatal(err)
	}
	hasher := hash.New()
	_, _ = hasher.Write(signingInput)
	r, s, err := ecdsa.Sign(rand.Reader, key, hasher.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}

	coordinateSize := (key.Curve.Params().BitSize + 7) / 8
	signature := make([]byte, 2*coordinateSize)
	r.FillBytes(signature[:coordinateSize])
	s.FillBytes(signature[coordinateSize:])

	return headerSegment + "." + claimsSegment + "." + encodeSegment(signature)
}

func signEd25519Proof(t *testing.T, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) string {
	t.Helper()

	header := proofHeader{
		Alg: "EdDSA",
		Typ: "dpop+jwt",
		JWK: mustJSON(t, map[string]string{
			"kty": "OKP",
			"crv": "Ed25519",
			"x":   base64.RawURLEncoding.EncodeToString(publicKey),
		}),
	}
	headerSegment := encodeSegment(mustJSON(t, header))
	claimsSegment := encodeSegment(mustJSON(t, map[string]string{"htm": "POST", "htu": "https://issuer.example/token"}))
	signingInput := []byte(headerSegment + "." + claimsSegment)
	signature := ed25519.Sign(privateKey, signingInput)

	return headerSegment + "." + claimsSegment + "." + encodeSegment(signature)
}

func publicRSAJWK(key rsa.PublicKey) map[string]string {
	exponent := make([]byte, 4)
	new(big.Int).SetInt64(int64(key.E)).FillBytes(exponent)
	return map[string]string{
		"kty": "RSA",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(exponent),
	}
}

func publicECJWK(key ecdsa.PublicKey) map[string]string {
	coordinateSize := (key.Curve.Params().BitSize + 7) / 8
	x := make([]byte, coordinateSize)
	y := make([]byte, coordinateSize)
	key.X.FillBytes(x)
	key.Y.FillBytes(y)

	return map[string]string{
		"kty": "EC",
		"crv": key.Curve.Params().Name,
		"x":   base64.RawURLEncoding.EncodeToString(x),
		"y":   base64.RawURLEncoding.EncodeToString(y),
	}
}

func ellipticP256() elliptic.Curve {
	return elliptic.P256()
}

func rsaHashForTest(t *testing.T, alg string) crypto.Hash {
	t.Helper()
	hash, err := hashForAlgorithm(alg)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func encodeSegment(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func splitProof(t *testing.T, proof string) []string {
	t.Helper()
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		t.Fatalf("proof has %d parts, want 3", len(parts))
	}
	return parts
}

func joinProof(parts []string) string {
	return parts[0] + "." + parts[1] + "." + parts[2]
}
