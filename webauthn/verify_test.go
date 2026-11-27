package webauthn

import (
	"crypto/sha256"
	"errors"
	"testing"
)

func mustVerify(t *testing.T, f *registrationFixture, attestationObject []byte) *RegistrationResult {
	t.Helper()
	result, err := VerifyRegistration(attestationObject, f.clientDataJSON, f.challenge, f.policy)
	if err != nil {
		t.Fatalf("expected successful verification, got %v", err)
	}
	return result
}

func TestPackedFullAttestation_SingleTier(t *testing.T) {
	f := newRegistrationFixture(t)
	root := createRootCA(t)
	f.policy.TrustAnchors.AddCert(root.cert)

	leafKey := mustGenerateECKey(t)
	leaf := createAttestationCert(t, root, f.aaguid, leafKey, nil)

	attestationObject := f.buildPackedFullObject(t, [][]byte{leaf.der}, leafKey)
	result := mustVerify(t, f, attestationObject)

	if result.AttestationType != "packed-full" {
		t.Fatalf("expected packed-full, got %q", result.AttestationType)
	}
	if len(result.Credential.ID) == 0 {
		t.Fatalf("credential ID was not returned")
	}
	if result.Credential.AAGUID != f.aaguid {
		t.Fatalf("AAGUID mismatch in result")
	}
	if result.Credential.Algorithm != algES256 {
		t.Fatalf("expected ES256 algorithm, got %d", result.Credential.Algorithm)
	}
	if result.Credential.SignCount != 42 {
		t.Fatalf("signCount not parsed: %d", result.Credential.SignCount)
	}
	if !result.Authenticator.UP || !result.Authenticator.AT {
		t.Fatalf("UP/AT flags not reported")
	}
}

func TestPackedFullAttestation_IntermediateChain(t *testing.T) {
	f := newRegistrationFixture(t)
	root := createRootCA(t)
	intermediate := createIntermediate(t, root)
	f.policy.TrustAnchors.AddCert(root.cert)

	leafKey := mustGenerateECKey(t)
	leaf := createAttestationCert(t, intermediate, f.aaguid, leafKey, nil)

	attestationObject := f.buildPackedFullObject(t, [][]byte{leaf.der, intermediate.der}, leafKey)
	result := mustVerify(t, f, attestationObject)
	if result.AttestationType != "packed-full" {
		t.Fatalf("expected packed-full via intermediate, got %q", result.AttestationType)
	}
}

func TestPackedSelfAttestation(t *testing.T) {
	f := newRegistrationFixture(t)
	result := mustVerify(t, f, f.buildPackedSelfObject(t))
	if result.AttestationType != "packed-self" {
		t.Fatalf("expected packed-self, got %q", result.AttestationType)
	}
}

func TestNoneAttestation_PolicyControlled(t *testing.T) {
	f := newRegistrationFixture(t)

	if _, err := VerifyRegistration(f.buildNoneObject(), f.clientDataJSON, f.challenge, f.policy); !errors.Is(err, ErrPolicy) {
		t.Fatalf("expected ErrPolicy for disabled none attestation, got %v", err)
	}

	f.policy.AllowNoneAttestation = true
	result := mustVerify(t, f, f.buildNoneObject())
	if result.AttestationType != "none" {
		t.Fatalf("expected none, got %q", result.AttestationType)
	}
}

func TestUserVerificationPolicy(t *testing.T) {
	f := newRegistrationFixture(t)
	f.policy.RequireUserVerification = true

	if _, err := VerifyRegistration(f.buildPackedSelfObject(t), f.clientDataJSON, f.challenge, f.policy); !errors.Is(err, ErrFlags) {
		t.Fatalf("expected ErrFlags without UV, got %v", err)
	}

	rpIDHash := sha256.Sum256([]byte(f.rpID))
	f.authData = buildAuthData(authDataOptions{
		rpIDHash:  rpIDHash[:],
		flags:     flagUP | flagAT | flagUV,
		signCount: 42,
		aaguid:    f.aaguid,
		credID:    []byte("test-credential-identifier-128b!!"),
		coseKey:   encodeCOSEKey(&f.credentialKey.PublicKey),
	})
	mustVerify(t, f, f.buildPackedSelfObject(t))
}

func TestBackupFlags(t *testing.T) {
	f := newRegistrationFixture(t)
	rpIDHash := sha256.Sum256([]byte(f.rpID))

	f.authData = buildAuthData(authDataOptions{
		rpIDHash:  rpIDHash[:],
		flags:     flagUP | flagAT | flagBE | flagBS,
		signCount: 1,
		aaguid:    f.aaguid,
		credID:    []byte("test-credential-identifier-128b!!"),
		coseKey:   encodeCOSEKey(&f.credentialKey.PublicKey),
	})
	mustVerify(t, f, f.buildPackedSelfObject(t))

	illegal := buildAuthData(authDataOptions{
		rpIDHash:  rpIDHash[:],
		flags:     flagUP | flagAT | flagBS,
		signCount: 1,
		aaguid:    f.aaguid,
		credID:    []byte("test-credential-identifier-128b!!"),
		coseKey:   encodeCOSEKey(&f.credentialKey.PublicKey),
	})
	f.authData = illegal
	if _, err := VerifyRegistration(f.buildPackedSelfObject(t), f.clientDataJSON, f.challenge, f.policy); !errors.Is(err, ErrFlags) {
		t.Fatalf("expected ErrFlags for BE=0/BS=1, got %v", err)
	}
}
