package webauthn

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"
	"time"
)

// 1. Illegal authenticatorData flag combinations are rejected at parse time.
func TestAdversarial_IllegalFlagCombinations(t *testing.T) {
	rpIDHash := sha256.Sum256([]byte("login.example.com"))

	base := func(flags byte) []byte {
		key := mustGenerateECKey(t)
		return buildAuthData(authDataOptions{
			rpIDHash:  rpIDHash[:],
			flags:     flags,
			signCount: 9,
			aaguid:    AAGUID{},
			credID:    []byte("test-credential-identifier-128b!!"),
			coseKey:   encodeCOSEKey(&key.PublicKey),
		})
	}

	parseCases := map[string]byte{
		"reserved bit 1": flagUP | flagAT | (1 << 1),
		"reserved bit 7": flagUP | flagAT | (1 << 7),
		"BS without BE":  flagUP | flagAT | flagBS,
	}
	for name, flags := range parseCases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAuthenticatorData(base(flags)); !errors.Is(err, ErrFlags) {
				t.Fatalf("expected ErrFlags for flags 0x%02x, got %v", flags, err)
			}
		})
	}

	// UP/AT are mandatory registration policy enforced after parsing.
	policyCases := map[string]byte{
		"UP missing": flagAT,
		"AT missing": flagUP,
	}
	policy := &Policy{RPID: "login.example.com"}
	for name, flags := range policyCases {
		t.Run(name, func(t *testing.T) {
			data, err := parseAuthenticatorData(base(flags))
			if err != nil {
				t.Fatalf("parse failed for flags 0x%02x: %v", flags, err)
			}
			if err := enforceAuthDataPolicy(data, policy); !errors.Is(err, ErrFlags) {
				t.Fatalf("expected ErrFlags for flags 0x%02x, got %v", flags, err)
			}
		})
	}
}

// 2. A certificate AAGUID extension that differs from authenticatorData is fatal.
func TestAdversarial_AAGUIDExtensionMismatch(t *testing.T) {
	f := newRegistrationFixture(t)
	root := createRootCA(t)
	f.policy.TrustAnchors.AddCert(root.cert)

	var wrongAAGUID AAGUID
	for i := range wrongAAGUID {
		wrongAAGUID[i] = 0xff
	}
	leafKey := mustGenerateECKey(t)
	leaf := createAttestationCert(t, root, wrongAAGUID, leafKey, nil)

	attestationObject := f.buildPackedFullObject(t, [][]byte{leaf.der}, leafKey)
	_, err := VerifyRegistration(attestationObject, f.clientDataJSON, f.challenge, f.policy)
	if !errors.Is(err, ErrAAGUID) {
		t.Fatalf("expected ErrAAGUID for extension mismatch, got %v", err)
	}
}

// 3. crossOrigin=true in clientDataJSON is rejected even with a valid signature.
func TestAdversarial_CrossOriginRejected(t *testing.T) {
	f := newRegistrationFixture(t)
	crossOriginClientData := buildClientData(t, f.challenge, f.origin, true)

	_, err := VerifyRegistration(f.buildPackedSelfObject(t), crossOriginClientData, f.challenge, f.policy)
	if !errors.Is(err, ErrClientData) {
		t.Fatalf("expected ErrClientData for crossOrigin=true, got %v", err)
	}
}

// 4. A self-signed software key dressed up as a Full attestation is rejected
// because it cannot chain to an injected trust anchor.
func TestAdversarial_SelfSignedFullAttestationRejected(t *testing.T) {
	f := newRegistrationFixture(t)
	root := createRootCA(t)
	f.policy.TrustAnchors.AddCert(root.cert)

	attackerKey := mustGenerateECKey(t)
	octetDER, err := asn1.Marshal(f.aaguid[:])
	if err != nil {
		t.Fatalf("marshal aaguid: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(999),
		Subject:               pkix.Name{CommonName: "Attacker Key", OrganizationalUnit: []string{authenticatorAttestationOU}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  false,
		ExtraExtensions:       []pkix.Extension{{Id: oidFIDOAAGUID, Value: octetDER}},
	}
	selfDER, err := x509.CreateCertificate(rand.Reader, template, template, &attackerKey.PublicKey, attackerKey)
	if err != nil {
		t.Fatalf("create self-signed cert: %v", err)
	}

	attestationObject := f.buildPackedFullObject(t, [][]byte{selfDER}, attackerKey)
	_, err = VerifyRegistration(attestationObject, f.clientDataJSON, f.challenge, f.policy)
	if !errors.Is(err, ErrCertificate) {
		t.Fatalf("expected ErrCertificate for untrusted self-signed attestation, got %v", err)
	}
}
