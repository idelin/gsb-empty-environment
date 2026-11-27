package webauthn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestAuthenticatorData_ExtensionsAndTruncation(t *testing.T) {
	rpIDHash := sha256.Sum256([]byte("login.example.com"))
	key := mustGenerateECKey(t)

	t.Run("ED with valid extension map", func(t *testing.T) {
		extensions := encMap(mapEntry{encText("hmac-secret"), encBool(true)})
		raw := buildAuthData(authDataOptions{
			rpIDHash:   rpIDHash[:],
			flags:      flagUP | flagAT | flagED,
			signCount:  7,
			aaguid:     AAGUID{},
			credID:     []byte("test-credential-identifier-128b!!"),
			coseKey:    encodeCOSEKey(&key.PublicKey),
			extensions: extensions,
		})
		data, err := parseAuthenticatorData(raw)
		if err != nil {
			t.Fatalf("expected successful parse with extensions: %v", err)
		}
		if data.Extensions == nil {
			t.Fatalf("extension map was not captured")
		}
	})

	t.Run("ED set but extension data missing", func(t *testing.T) {
		raw := buildAuthData(authDataOptions{
			rpIDHash:  rpIDHash[:],
			flags:     flagUP | flagAT | flagED,
			signCount: 7,
			aaguid:    AAGUID{},
			credID:    []byte("test-credential-identifier-128b!!"),
			coseKey:   encodeCOSEKey(&key.PublicKey),
		})
		if _, err := parseAuthenticatorData(raw); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for missing extensions, got %v", err)
		}
	})

	t.Run("truncated before attested credential", func(t *testing.T) {
		raw := buildAuthData(authDataOptions{
			rpIDHash: rpIDHash[:],
			flags:    flagUP | flagAT,
		})
		if _, err := parseAuthenticatorData(raw[:minAuthDataLength+4]); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for truncation, got %v", err)
		}
	})

	t.Run("credential id length overruns buffer", func(t *testing.T) {
		var out []byte
		out = append(out, rpIDHash[:]...)
		out = append(out, flagUP|flagAT)
		out = append(out, 0, 0, 0, 7)
		out = append(out, make([]byte, aaguidLength)...)
		out = append(out, 0xff, 0xff) // huge credIdLength
		if _, err := parseAuthenticatorData(out); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for overrun, got %v", err)
		}
	})
}

func TestCOSEPublicKey_Strictness(t *testing.T) {
	key := mustGenerateECKey(t)

	t.Run("reject non-EC2 key type", func(t *testing.T) {
		node, err := decodeCBOR(encodeCOSEKey(&key.PublicKey))
		if err != nil {
			t.Fatalf("decode fixture: %v", err)
		}
		node.mapIndex(coseKty).uintVal = 8 // RSA kty, unsupported here
		if _, _, err := parseCOSEPublicKey(node); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for non-EC2 kty, got %v", err)
		}
	})

	t.Run("reject algorithm/curve mismatch", func(t *testing.T) {
		node, err := decodeCBOR(encodeCOSEKey(&key.PublicKey))
		if err != nil {
			t.Fatalf("decode fixture: %v", err)
		}
		// Rebuild the COSE map advertising ES384 while keeping P-256 coords.
		node.entries = []cborMapEntry{
			{key: mustNode(t, encInt(coseKty)), value: mustNode(t, encInt(coseKtyEC2))},
			{key: mustNode(t, encInt(coseAlg)), value: mustNode(t, encInt(algES384))},
			{key: mustNode(t, encInt(coseCrv)), value: mustNode(t, encInt(coseCrvP256))},
			{key: mustNode(t, encInt(coseX)), value: mustNode(t, encBytes(key.X.FillBytes(make([]byte, 32))))},
			{key: mustNode(t, encInt(coseY)), value: mustNode(t, encBytes(key.Y.FillBytes(make([]byte, 32))))},
		}
		if _, _, err := parseCOSEPublicKey(node); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for alg/curve mismatch, got %v", err)
		}
	})

	t.Run("reject point off curve", func(t *testing.T) {
		node, err := decodeCBOR(encodeCOSEKey(&key.PublicKey))
		if err != nil {
			t.Fatalf("decode fixture: %v", err)
		}
		x := node.mapIndex(coseX)
		x.bytes[31] ^= 0x01
		if _, _, err := parseCOSEPublicKey(node); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for off-curve point, got %v", err)
		}
	})

	t.Run("reject wrong coordinate length", func(t *testing.T) {
		node, err := decodeCBOR(encodeCOSEKey(&key.PublicKey))
		if err != nil {
			t.Fatalf("decode fixture: %v", err)
		}
		node.mapIndex(coseX).bytes = node.mapIndex(coseX).bytes[:31]
		if _, _, err := parseCOSEPublicKey(node); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for short coordinate, got %v", err)
		}
	})

	t.Run("P-384 key accepted", func(t *testing.T) {
		p384Key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			t.Fatalf("generate P-384 key: %v", err)
		}
		node, err := decodeCBOR(encodeCOSEKey(&p384Key.PublicKey))
		if err != nil {
			t.Fatalf("decode P-384 COSE: %v", err)
		}
		parsed, alg, err := parseCOSEPublicKey(node)
		if err != nil {
			t.Fatalf("parse P-384 COSE: %v", err)
		}
		if alg != algES384 || parsed.Curve != elliptic.P384() {
			t.Fatalf("P-384 key not parsed correctly (alg=%d)", alg)
		}
	})
}

func mustNode(t *testing.T, raw []byte) *cborNode {
	t.Helper()
	node, err := decodeCBOR(raw)
	if err != nil {
		t.Fatalf("decode node: %v", err)
	}
	return node
}

func TestPackedStatement_Strictness(t *testing.T) {
	f := newRegistrationFixture(t)

	t.Run("unknown attStmt member rejected in self mode", func(t *testing.T) {
		attStmt := encMap(
			mapEntry{textKey("alg"), encInt(algES256)},
			mapEntry{textKey("sig"), encBytes(ecdsaSignASN1(t, f.credentialKey, f.signedPayload()))},
			mapEntry{textKey("x5c"), encArray()}, // present but empty -> malformed
		)
		obj := f.attestationObject("packed", attStmt)
		_, err := VerifyRegistration(obj, f.clientDataJSON, f.challenge, f.policy)
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for empty x5c, got %v", err)
		}
	})

	t.Run("unsupported format rejected", func(t *testing.T) {
		obj := f.attestationObject("tpm", encMap())
		_, err := VerifyRegistration(obj, f.clientDataJSON, f.challenge, f.policy)
		if !errors.Is(err, ErrPolicy) {
			t.Fatalf("expected ErrPolicy for unsupported fmt, got %v", err)
		}
	})

	t.Run("none with non-empty statement rejected", func(t *testing.T) {
		f.policy.AllowNoneAttestation = true
		attStmt := encMap(mapEntry{textKey("sig"), encBytes([]byte{0x30, 0x00})})
		obj := f.attestationObject("none", attStmt)
		_, err := VerifyRegistration(obj, f.clientDataJSON, f.challenge, f.policy)
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for non-empty none statement, got %v", err)
		}
	})

	t.Run("nil policy rejected", func(t *testing.T) {
		if _, err := VerifyRegistration(f.buildNoneObject(), f.clientDataJSON, f.challenge, nil); !errors.Is(err, ErrPolicy) {
			t.Fatalf("expected ErrPolicy for nil policy, got %v", err)
		}
	})
}

func TestClientData_MalformedInputs(t *testing.T) {
	f := newRegistrationFixture(t)

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := VerifyRegistration(f.buildPackedSelfObject(t), []byte("{not json"), f.challenge, f.policy)
		if !errors.Is(err, ErrClientData) {
			t.Fatalf("expected ErrClientData, got %v", err)
		}
	})

	t.Run("non-object JSON", func(t *testing.T) {
		_, err := VerifyRegistration(f.buildPackedSelfObject(t), []byte(`[1,2,3]`), f.challenge, f.policy)
		if !errors.Is(err, ErrClientData) {
			t.Fatalf("expected ErrClientData for array JSON, got %v", err)
		}
	})

	t.Run("invalid base64url challenge", func(t *testing.T) {
		raw := buildClientDataCustom(t, expectedClientDataType, "!!!not-base64!!!", f.origin, false)
		_, err := VerifyRegistration(f.buildPackedSelfObject(t), raw, f.challenge, f.policy)
		if !errors.Is(err, ErrClientData) {
			t.Fatalf("expected ErrClientData for bad challenge encoding, got %v", err)
		}
	})

	t.Run("standard base64 alphabet rejected", func(t *testing.T) {
		// '+' and '/' are not part of the base64url alphabet.
		raw := buildClientDataCustom(t, expectedClientDataType, "ab+/", f.origin, false)
		_, err := VerifyRegistration(f.buildPackedSelfObject(t), raw, f.challenge, f.policy)
		if !errors.Is(err, ErrClientData) {
			t.Fatalf("expected ErrClientData for standard base64, got %v", err)
		}
	})
}
