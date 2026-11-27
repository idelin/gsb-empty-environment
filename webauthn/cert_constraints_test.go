package webauthn

import (
	"crypto/x509"
	"errors"
	"testing"
)

func TestFullAttestation_CertificateConstraints(t *testing.T) {
	f := newRegistrationFixture(t)
	root := createRootCA(t)
	f.policy.TrustAnchors.AddCert(root.cert)

	t.Run("certificate version must be 3", func(t *testing.T) {
		leafKey := mustGenerateECKey(t)
		leaf := createAttestationCert(t, root, f.aaguid, leafKey, nil)

		// Go's certificate writer always emits v3 when extensions are present,
		// so exercise the version gate directly on a modified parsed leaf.
		data, perr := parseAuthenticatorData(f.authData)
		if perr != nil {
			t.Fatalf("parse auth data: %v", perr)
		}
		downgraded := *leaf.cert
		downgraded.Version = 2
		if err := enforceAttestationCertificate(&downgraded, data); !errors.Is(err, ErrCertificate) {
			t.Fatalf("expected ErrCertificate for v2 cert, got %v", err)
		}
	})

	t.Run("CA must be false", func(t *testing.T) {
		leafKey := mustGenerateECKey(t)
		leaf := createAttestationCert(t, root, f.aaguid, leafKey, func(template *x509.Certificate) {
			template.IsCA = true
			template.KeyUsage = x509.KeyUsageCertSign
		})
		_, err := VerifyRegistration(f.buildPackedFullObject(t, [][]byte{leaf.der}, leafKey), f.clientDataJSON, f.challenge, f.policy)
		if !errors.Is(err, ErrCertificate) {
			t.Fatalf("expected ErrCertificate for CA=true leaf, got %v", err)
		}
	})

	t.Run("OU must match", func(t *testing.T) {
		leafKey := mustGenerateECKey(t)
		leaf := createAttestationCert(t, root, f.aaguid, leafKey, func(template *x509.Certificate) {
			template.Subject.OrganizationalUnit = []string{"Some Other OU"}
		})
		_, err := VerifyRegistration(f.buildPackedFullObject(t, [][]byte{leaf.der}, leafKey), f.clientDataJSON, f.challenge, f.policy)
		if !errors.Is(err, ErrCertificate) {
			t.Fatalf("expected ErrCertificate for wrong OU, got %v", err)
		}
	})

	t.Run("AAGUID extension must be present", func(t *testing.T) {
		leafKey := mustGenerateECKey(t)
		leaf := createAttestationCert(t, root, f.aaguid, leafKey, func(template *x509.Certificate) {
			template.ExtraExtensions = nil
		})
		_, err := VerifyRegistration(f.buildPackedFullObject(t, [][]byte{leaf.der}, leafKey), f.clientDataJSON, f.challenge, f.policy)
		if !errors.Is(err, ErrAAGUID) {
			t.Fatalf("expected ErrAAGUID when extension missing, got %v", err)
		}
	})

	t.Run("AAGUID must be allow-listed", func(t *testing.T) {
		leafKey := mustGenerateECKey(t)
		leaf := createAttestationCert(t, root, f.aaguid, leafKey, nil)
		f.policy.AllowedAAGUIDs = nil
		_, err := VerifyRegistration(f.buildPackedFullObject(t, [][]byte{leaf.der}, leafKey), f.clientDataJSON, f.challenge, f.policy)
		if !errors.Is(err, ErrAAGUID) {
			t.Fatalf("expected ErrAAGUID when not allow-listed, got %v", err)
		}
	})

	t.Run("missing trust anchors fails closed", func(t *testing.T) {
		leafKey := mustGenerateECKey(t)
		leaf := createAttestationCert(t, root, f.aaguid, leafKey, nil)
		policy := *f.policy
		policy.TrustAnchors = nil
		_, err := VerifyRegistration(f.buildPackedFullObject(t, [][]byte{leaf.der}, leafKey), f.clientDataJSON, f.challenge, &policy)
		if !errors.Is(err, ErrCertificate) {
			t.Fatalf("expected ErrCertificate without trust anchors, got %v", err)
		}
	})
}
