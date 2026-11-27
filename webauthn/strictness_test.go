package webauthn

import (
	"crypto/sha256"
	"errors"
	"testing"
)

func TestRPIDHashBinding(t *testing.T) {
	f := newRegistrationFixture(t)

	// Re-sign authData carrying a hash of a different rpId.
	foreignHash := sha256.Sum256([]byte("attacker.example.org"))
	f.authData = buildAuthData(authDataOptions{
		rpIDHash:  foreignHash[:],
		flags:     flagUP | flagAT,
		signCount: 42,
		aaguid:    f.aaguid,
		credID:    []byte("test-credential-identifier-128b!!"),
		coseKey:   encodeCOSEKey(&f.credentialKey.PublicKey),
	})
	_, err := VerifyRegistration(f.buildPackedSelfObject(t), f.clientDataJSON, f.challenge, f.policy)
	if !errors.Is(err, ErrRPIDHash) {
		t.Fatalf("expected ErrRPIDHash, got %v", err)
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	f := newRegistrationFixture(t)

	// Build an otherwise-valid self attestation, then corrupt only a byte in
	// the middle of the DER signature so parsing still reaches verification.
	validSignature := ecdsaSignASN1(t, f.credentialKey, f.signedPayload())
	corrupted := append([]byte(nil), validSignature...)
	corrupted[len(corrupted)/2] ^= 0xff
	attStmt := encMap(
		mapEntry{textKey("alg"), encInt(algES256)},
		mapEntry{textKey("sig"), encBytes(corrupted)},
	)
	attestationObject := f.attestationObject("packed", attStmt)

	_, err := VerifyRegistration(attestationObject, f.clientDataJSON, f.challenge, f.policy)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("expected ErrSignature for tampered signature, got %v", err)
	}
}

func TestNonDERSignatureRejected(t *testing.T) {
	f := newRegistrationFixture(t)

	// IEEE P1363 fixed-length (r||s) form must be rejected; only DER is valid.
	valid := ecdsaSignASN1(t, f.credentialKey, f.signedPayload())
	rawForm := make([]byte, 64)
	for i := range rawForm {
		rawForm[i] = valid[i%len(valid)]
	}
	attStmt := encMap(
		mapEntry{textKey("alg"), encInt(algES256)},
		mapEntry{textKey("sig"), encBytes(rawForm)},
	)
	_, err := VerifyRegistration(f.attestationObject("packed", attStmt), f.clientDataJSON, f.challenge, f.policy)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("expected ErrSignature for non-DER signature, got %v", err)
	}
}

func TestMalformedAuthenticatorData(t *testing.T) {
	f := newRegistrationFixture(t)

	cases := map[string][]byte{
		"truncated header": make([]byte, minAuthDataLength-1),
		"AT set but no attested data": append(
			append([]byte(nil), f.authData[:minAuthDataLength]...),
			1, 2, 3),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			obj := f.attestationObject("packed", encMap(
				mapEntry{textKey("alg"), encInt(algES256)},
				mapEntry{textKey("sig"), encBytes([]byte{0x30, 0x00})},
			))
			// Replace the embedded authData bytes via a freshly built object.
			obj = encMap(
				mapEntry{textKey("fmt"), encText("packed")},
				mapEntry{textKey("attStmt"), encMap(
					mapEntry{textKey("alg"), encInt(algES256)},
					mapEntry{textKey("sig"), encBytes([]byte{0x30, 0x00})},
				)},
				mapEntry{textKey("authData"), encBytes(raw)},
			)
			if _, err := VerifyRegistration(obj, f.clientDataJSON, f.challenge, f.policy); !errors.Is(err, ErrMalformed) {
				t.Fatalf("expected ErrMalformed for %s, got %v", name, err)
			}
		})
	}
}

func TestCanonicalCBORRejected(t *testing.T) {
	t.Run("non-minimal integer", func(t *testing.T) {
		// Major type 0, value 1 encoded with a one-byte argument (0x18 0x01).
		if _, err := decodeCBOR([]byte{0x18, 0x01}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for non-minimal encoding, got %v", err)
		}
	})

	t.Run("indefinite array", func(t *testing.T) {
		// 0x9f indefinite array, 0x01 element, 0xff break.
		if _, err := decodeCBOR([]byte{0x9f, 0x01, 0xff}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for indefinite array, got %v", err)
		}
	})

	t.Run("map keys out of order", func(t *testing.T) {
		// Keys "b" then "a" (both length 1, descending).
		encoded := encMap(
			mapEntry{encText("b"), encInt(1)},
			mapEntry{encText("a"), encInt(2)},
		)
		if _, err := decodeCBOR(encoded); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for unordered map, got %v", err)
		}
	})

	t.Run("float rejected", func(t *testing.T) {
		// Major 7, additional info 26 => half-float marker with two bytes.
		if _, err := decodeCBOR([]byte{0xf9, 0x00, 0x00}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for float, got %v", err)
		}
	})

	t.Run("trailing bytes rejected", func(t *testing.T) {
		if _, err := decodeCBOR(append(encInt(1), 0x00)); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for trailing bytes, got %v", err)
		}
	})
}
