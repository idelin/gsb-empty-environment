package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"math/big"
	"testing"
	"time"
)

type testFixture struct {
	now              time.Time
	cert             *x509.Certificate
	parentSigner     crypto.Signer
	credentialSigner crypto.Signer
	credential       *DelegatedCredential
	raw              []byte
	role             Role
}

func makeCertificate(t *testing.T, now time.Time, signer crypto.Signer, mutate func(*x509.Certificate)) *x509.Certificate {
	t.Helper()

	template := &x509.Certificate{
		SerialNumber: big.NewInt(9345),
		Subject:      pkix.Name{CommonName: "delegation.example"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(30 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		ExtraExtensions: []pkix.Extension{{
			Id:       oidDelegationUsage,
			Critical: false,
			Value:    asn1Null,
		}},
	}
	if mutate != nil {
		mutate(template)
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, signer.Public(), signer)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return cert
}

var asn1Null = []byte{0x05, 0x00}

func marshalCredential(validTime uint32, certVerifyAlgorithm SignatureScheme, publicKeyDER []byte) []byte {
	out := make([]byte, 0, 9+len(publicKeyDER))
	out = append(out, byte(validTime>>24), byte(validTime>>16), byte(validTime>>8), byte(validTime))
	out = append(out, byte(certVerifyAlgorithm>>8), byte(certVerifyAlgorithm))
	out = append(out, byte(len(publicKeyDER)>>16), byte(len(publicKeyDER)>>8), byte(len(publicKeyDER)))
	out = append(out, publicKeyDER...)
	return out
}

func marshalDelegatedCredential(credentialDER []byte, algorithm SignatureScheme, signature []byte) []byte {
	out := append([]byte(nil), credentialDER...)
	out = append(out, byte(algorithm>>8), byte(algorithm))
	out = append(out, byte(len(signature)>>8), byte(len(signature)))
	out = append(out, signature...)
	return out
}

func signMessage(t *testing.T, signer crypto.Signer, scheme SignatureScheme, message []byte) []byte {
	t.Helper()

	info, ok := signatureScheme(scheme)
	if !ok {
		t.Fatalf("unsupported test signature scheme %s", scheme)
	}
	switch info.kind {
	case keyECDSA:
		key, ok := signer.(*ecdsa.PrivateKey)
		if !ok {
			t.Fatalf("%s signer is not ECDSA", scheme)
		}
		digest, err := hashMessage(info.hash, message)
		if err != nil {
			t.Fatalf("hashMessage: %v", err)
		}
		signature, err := ecdsa.SignASN1(rand.Reader, key, digest)
		if err != nil {
			t.Fatalf("SignASN1: %v", err)
		}
		return signature
	case keyEd25519:
		key, ok := signer.(ed25519.PrivateKey)
		if !ok {
			t.Fatalf("%s signer is not Ed25519", scheme)
		}
		return ed25519.Sign(key, message)
	case keyRSA:
		key, ok := signer.(*rsa.PrivateKey)
		if !ok {
			t.Fatalf("%s signer is not RSA", scheme)
		}
		digest, err := hashMessage(info.hash, message)
		if err != nil {
			t.Fatalf("hashMessage: %v", err)
		}
		signature, err := rsa.SignPSS(rand.Reader, key, info.hash, digest, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       info.hash,
		})
		if err != nil {
			t.Fatalf("SignPSS: %v", err)
		}
		return signature
	default:
		t.Fatalf("unsupported test key kind")
		return nil
	}
}

func issueCredential(t *testing.T, now time.Time, parentSigner crypto.Signer, parentCert *x509.Certificate, delegatedSigner crypto.Signer, validTime uint32, certVerifyAlgorithm, signingAlgorithm SignatureScheme, role Role, contextOverride string) (*DelegatedCredential, []byte) {
	t.Helper()

	publicKeyDER, err := x509.MarshalPKIXPublicKey(delegatedSigner.Public())
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return issueCredentialWithPublicKeyDER(t, now, parentSigner, parentCert, publicKeyDER, validTime, certVerifyAlgorithm, signingAlgorithm, role, contextOverride)
}

func issueCredentialWithPublicKeyDER(t *testing.T, now time.Time, parentSigner crypto.Signer, parentCert *x509.Certificate, publicKeyDER []byte, validTime uint32, certVerifyAlgorithm, signingAlgorithm SignatureScheme, role Role, contextOverride string) (*DelegatedCredential, []byte) {
	t.Helper()

	credentialDER := marshalCredential(validTime, certVerifyAlgorithm, publicKeyDER)

	context, err := role.delegatedContext()
	if err != nil {
		t.Fatalf("delegatedContext: %v", err)
	}
	if contextOverride != "" {
		context = contextOverride
	}
	message := append([]byte(nil), bytes.Repeat([]byte{0x20}, 64)...)
	message = append(message, context...)
	message = append(message, 0x00)
	message = append(message, parentCert.Raw...)
	message = append(message, credentialDER...)
	message = append(message, byte(signingAlgorithm>>8), byte(signingAlgorithm))

	signature := signMessage(t, parentSigner, signingAlgorithm, message)
	raw := marshalDelegatedCredential(credentialDER, signingAlgorithm, signature)
	parsed, err := ParseDelegatedCredential(raw)
	if err != nil {
		t.Fatalf("ParseDelegatedCredential: %v", err)
	}
	return parsed, raw
}

func makeECDSAKey(t *testing.T, curve elliptic.Curve) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return key
}

func makeEd25519Key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return key
}

func makeRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return key
}

func marshalRSAPSSPublicKey(t *testing.T, key *rsa.PublicKey) []byte {
	t.Helper()

	hashAlgorithm, err := asn1.Marshal(pkix.AlgorithmIdentifier{
		Algorithm:  oidSHA256,
		Parameters: asn1.RawValue{FullBytes: asn1.NullBytes},
	})
	if err != nil {
		t.Fatalf("marshal hash AlgorithmIdentifier: %v", err)
	}
	maskAlgorithm, err := asn1.Marshal(pkix.AlgorithmIdentifier{
		Algorithm:  oidMGF1,
		Parameters: asn1.RawValue{FullBytes: hashAlgorithm},
	})
	if err != nil {
		t.Fatalf("marshal MGF AlgorithmIdentifier: %v", err)
	}
	saltLength, err := asn1.Marshal(32)
	if err != nil {
		t.Fatalf("marshal salt length: %v", err)
	}
	trailerField, err := asn1.Marshal(1)
	if err != nil {
		t.Fatalf("marshal trailer field: %v", err)
	}
	parameters, err := asn1.Marshal([]asn1.RawValue{
		{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: hashAlgorithm},
		{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: maskAlgorithm},
		{Class: asn1.ClassContextSpecific, Tag: 2, IsCompound: true, Bytes: saltLength},
		{Class: asn1.ClassContextSpecific, Tag: 3, IsCompound: true, Bytes: trailerField},
	})
	if err != nil {
		t.Fatalf("marshal PSS parameters: %v", err)
	}
	rsaKey := x509.MarshalPKCS1PublicKey(key)
	spki, err := asn1.Marshal(subjectPublicKeyInfo{
		Algorithm: pkix.AlgorithmIdentifier{
			Algorithm:  oidRSAPSS,
			Parameters: asn1.RawValue{FullBytes: parameters},
		},
		PublicKey: asn1.BitString{Bytes: rsaKey, BitLength: len(rsaKey) * 8},
	})
	if err != nil {
		t.Fatalf("marshal RSASSA-PSS SPKI: %v", err)
	}
	return spki
}

func newValidFixture(t *testing.T) *testFixture {
	t.Helper()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	parentSigner := makeECDSAKey(t, elliptic.P256())
	cert := makeCertificate(t, now, parentSigner, nil)
	credentialSigner := makeECDSAKey(t, elliptic.P256())
	credential, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")
	return &testFixture{
		now:              now,
		cert:             cert,
		parentSigner:     parentSigner,
		credentialSigner: credentialSigner,
		credential:       credential,
		raw:              raw,
		role:             RoleServer,
	}
}

func signCertificateVerify(t *testing.T, fixture *testFixture, transcriptHash []byte, contextOverride string) []byte {
	t.Helper()
	context, err := fixture.role.certificateVerifyContext()
	if err != nil {
		t.Fatalf("certificateVerifyContext: %v", err)
	}
	if contextOverride != "" {
		context = contextOverride
	}
	message := append([]byte(nil), bytes.Repeat([]byte{0x20}, 64)...)
	message = append(message, context...)
	message = append(message, 0x00)
	message = append(message, transcriptHash...)
	return signMessage(t, fixture.credentialSigner, fixture.credential.CertVerifyAlgorithm, message)
}

func TestParseAndVerifyValidCredential(t *testing.T) {
	fixture := newValidFixture(t)

	transcriptHash := sha256.Sum256([]byte("handshake transcript"))
	identity, err := VerifyConnection(fixture.cert, fixture.raw, fixture.role, fixture.now, transcriptHash[:], fixture.credential.CertVerifyAlgorithm, signCertificateVerify(t, fixture, transcriptHash[:], ""))
	if err != nil {
		t.Fatalf("VerifyConnection: %v", err)
	}
	if identity == nil {
		t.Fatal("VerifyConnection returned a nil identity without an error")
	}
	if identity.Certificate != fixture.cert {
		t.Fatal("identity is not bound to the delegation certificate")
	}
	if identity.Expires != fixture.cert.NotBefore.Add(time.Hour) {
		t.Fatalf("unexpected expiry %s", identity.Expires)
	}

	if fixture.credential.ValidTime != 3600 {
		t.Fatalf("ValidTime = %d", fixture.credential.ValidTime)
	}
	if fixture.credential.CertVerifyAlgorithm != ECDSA_SECP256R1_SHA256 {
		t.Fatalf("CertVerifyAlgorithm = %s", fixture.credential.CertVerifyAlgorithm)
	}
	if fixture.credential.Algorithm != ECDSA_SECP256R1_SHA256 {
		t.Fatalf("Algorithm = %s", fixture.credential.Algorithm)
	}
}

func TestTwoPhaseVerificationDoesNotLeakIdentityOnBadHandshakeSignature(t *testing.T) {
	fixture := newValidFixture(t)
	verifier := &Verifier{Now: func() time.Time { return fixture.now }}

	verified, err := verifier.VerifyDelegatedCredential(fixture.cert, fixture.raw, fixture.role)
	if err != nil {
		t.Fatalf("VerifyDelegatedCredential: %v", err)
	}
	transcriptHash := sha256.Sum256([]byte("handshake transcript"))
	badSignature := signCertificateVerify(t, fixture, transcriptHash[:], "")
	badSignature[0] ^= 0xff

	identity, err := verified.VerifyCertificateVerify(transcriptHash[:], fixture.credential.CertVerifyAlgorithm, badSignature)
	if err == nil {
		t.Fatal("expected CertificateVerify failure")
	}
	if identity != nil {
		t.Fatal("identity published despite failed CertificateVerify")
	}
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("error = %v, want ErrSignatureInvalid", err)
	}
}

func TestVerifiedCredentialSnapshotsValidatedFields(t *testing.T) {
	fixture := newValidFixture(t)
	verified, err := (&Verifier{Now: func() time.Time { return fixture.now }}).VerifyDelegatedCredential(fixture.cert, fixture.raw, fixture.role)
	if err != nil {
		t.Fatalf("VerifyDelegatedCredential: %v", err)
	}

	transcriptHash := sha256.Sum256([]byte("handshake transcript"))
	signature := signCertificateVerify(t, fixture, transcriptHash[:], "")

	// The caller-visible parse result is mutable. Mutating it after validation
	// must not change what the verified object will accept.
	fixture.credential.CertVerifyAlgorithm = ED25519
	fixture.credential.PublicKey = makeEd25519Key(t).Public()

	if got := verified.ExpectedCertificateVerifyAlgorithm(); got != ECDSA_SECP256R1_SHA256 {
		t.Fatalf("snapshot algorithm = %s", got)
	}
	identity, err := verified.VerifyCertificateVerify(transcriptHash[:], ECDSA_SECP256R1_SHA256, signature)
	if err != nil {
		t.Fatalf("VerifyCertificateVerify after parse-result mutation: %v", err)
	}
	if identity == nil {
		t.Fatal("identity not published after valid handshake signature")
	}
}

func TestAlgorithmMismatch(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	t.Run("declared_credential_algorithm_does_not_match_parent_key", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeECDSAKey(t, elliptic.P256())
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")

		// Replace the credential-signing scheme with Ed25519 while keeping the
		// ECDSA parent key. This is an attacker-controlled field change.
		parsed, err := ParseDelegatedCredential(raw)
		if err != nil {
			t.Fatalf("ParseDelegatedCredential: %v", err)
		}
		parsed.Algorithm = ED25519
		_, err = (&Verifier{Now: func() time.Time { return now }}).VerifyParsedDelegatedCredential(cert, parsed, RoleServer)
		if !errors.Is(err, ErrAlgorithmMismatch) {
			t.Fatalf("error = %v, want ErrAlgorithmMismatch", err)
		}
	})

	t.Run("delegated_key_does_not_match_cert_verify_algorithm", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeEd25519Key(t)
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrAlgorithmMismatch) {
			t.Fatalf("error = %v, want ErrAlgorithmMismatch", err)
		}
	})

	t.Run("certificate_verify_scheme_does_not_match_credential", func(t *testing.T) {
		fixture := newValidFixture(t)
		verified, err := (&Verifier{Now: func() time.Time { return fixture.now }}).VerifyDelegatedCredential(fixture.cert, fixture.raw, RoleServer)
		if err != nil {
			t.Fatalf("VerifyDelegatedCredential: %v", err)
		}
		transcriptHash := sha256.Sum256([]byte("transcript"))
		identity, err := verified.VerifyCertificateVerify(transcriptHash[:], ECDSA_SECP384R1_SHA384, []byte{1, 2, 3})
		if !errors.Is(err, ErrAlgorithmMismatch) {
			t.Fatalf("error = %v, want ErrAlgorithmMismatch", err)
		}
		if identity != nil {
			t.Fatal("identity published on algorithm mismatch")
		}
	})

	t.Run("rsa_rsae_key_is_not_allowed_for_delegated_key", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeRSAKey(t)
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, RSA_PSS_RSAE_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrUnsupportedAlgorithm) {
			t.Fatalf("error = %v, want ErrUnsupportedAlgorithm", err)
		}
	})

	t.Run("rsa_pss_pss_scheme_requires_pss_key_oid", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeRSAKey(t)
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, RSA_PSS_PSS_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrAlgorithmMismatch) {
			t.Fatalf("error = %v, want ErrAlgorithmMismatch", err)
		}
	})
}

func TestValidityLimits(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	t.Run("expired_relative_to_certificate_not_before", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, func(template *x509.Certificate) {
			template.NotBefore = now.Add(-2 * time.Hour)
		})
		credentialSigner := makeECDSAKey(t, elliptic.P256())
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrCredentialExpired) {
			t.Fatalf("error = %v, want ErrCredentialExpired", err)
		}
	})

	t.Run("valid_time_over_seven_days", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeECDSAKey(t, elliptic.P256())
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, uint32((7*24*time.Hour+time.Second)/time.Second), ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrValidityTooLong) {
			t.Fatalf("error = %v, want ErrValidityTooLong", err)
		}
	})

	t.Run("expiry_beyond_certificate_not_after", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, func(template *x509.Certificate) {
			template.NotAfter = now.Add(6 * 24 * time.Hour)
		})
		credentialSigner := makeECDSAKey(t, elliptic.P256())
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, uint32(7*24*time.Hour/time.Second), ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrValidityTooLong) {
			t.Fatalf("error = %v, want ErrValidityTooLong", err)
		}
	})

	t.Run("certificate_not_yet_valid", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, func(template *x509.Certificate) {
			template.NotBefore = now.Add(time.Hour)
			template.NotAfter = now.Add(24 * time.Hour)
		})
		credentialSigner := makeECDSAKey(t, elliptic.P256())
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrCertificateInvalid) {
			t.Fatalf("error = %v, want ErrCertificateInvalid", err)
		}
	})

	t.Run("certificate_expired", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, func(template *x509.Certificate) {
			template.NotBefore = now.Add(-48 * time.Hour)
			template.NotAfter = now.Add(-time.Hour)
		})
		credentialSigner := makeECDSAKey(t, elliptic.P256())
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrCertificateInvalid) {
			t.Fatalf("error = %v, want ErrCertificateInvalid", err)
		}
	})

	t.Run("application_profile_max_validity", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeECDSAKey(t, elliptic.P256())
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 7200, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")
		_, err := (&Verifier{
			Now:         func() time.Time { return now },
			MaxValidity: time.Hour,
		}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrValidityTooLong) {
			t.Fatalf("error = %v, want ErrValidityTooLong", err)
		}
	})
}

func TestCertificateUsageChecks(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		mutate func(*x509.Certificate)
	}{
		{
			name: "missing_delegation_usage",
			mutate: func(template *x509.Certificate) {
				template.ExtraExtensions = nil
			},
		},
		{
			name: "critical_delegation_usage",
			mutate: func(template *x509.Certificate) {
				template.ExtraExtensions[0].Critical = true
			},
		},
		{
			name: "malformed_delegation_usage",
			mutate: func(template *x509.Certificate) {
				template.ExtraExtensions[0].Value = []byte{0x05, 0x01, 0x00}
			},
		},
		{
			name: "missing_digital_signature",
			mutate: func(template *x509.Certificate) {
				template.KeyUsage = x509.KeyUsageKeyEncipherment
			},
		},
		{
			name: "wrong_extended_key_usage_for_server",
			mutate: func(template *x509.Certificate) {
				template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parentSigner := makeECDSAKey(t, elliptic.P256())
			cert := makeCertificate(t, now, parentSigner, test.mutate)
			credentialSigner := makeECDSAKey(t, elliptic.P256())
			_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")
			_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
			if !errors.Is(err, ErrCertificateUsage) {
				t.Fatalf("error = %v, want ErrCertificateUsage", err)
			}
		})
	}
}

func TestWrongContextFails(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	t.Run("credential_signed_with_certificate_verify_context", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeECDSAKey(t, elliptic.P256())
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, CertificateVerifyServerContext)
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("error = %v, want ErrSignatureInvalid", err)
		}
	})

	t.Run("credential_signed_with_wrong_delegation_context", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeECDSAKey(t, elliptic.P256())
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "TLS 1.3, server delegated credentials")
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("error = %v, want ErrSignatureInvalid", err)
		}
	})

	t.Run("server_credential_signed_with_client_context", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeECDSAKey(t, elliptic.P256())
		_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, DelegatedCredentialClientContext)
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, RoleServer)
		if !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("error = %v, want ErrSignatureInvalid", err)
		}
	})

	t.Run("certificate_verify_signed_with_credential_context", func(t *testing.T) {
		fixture := newValidFixture(t)
		verified, err := (&Verifier{Now: func() time.Time { return fixture.now }}).VerifyDelegatedCredential(fixture.cert, fixture.raw, RoleServer)
		if err != nil {
			t.Fatalf("VerifyDelegatedCredential: %v", err)
		}
		transcriptHash := sha256.Sum256([]byte("transcript"))
		signature := signCertificateVerify(t, fixture, transcriptHash[:], DelegatedCredentialServerContext)
		identity, err := verified.VerifyCertificateVerify(transcriptHash[:], fixture.credential.CertVerifyAlgorithm, signature)
		if !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("error = %v, want ErrSignatureInvalid", err)
		}
		if identity != nil {
			t.Fatal("identity published despite wrong CertificateVerify context")
		}
	})

	t.Run("server_certificate_verify_signed_with_client_context", func(t *testing.T) {
		fixture := newValidFixture(t)
		verified, err := (&Verifier{Now: func() time.Time { return fixture.now }}).VerifyDelegatedCredential(fixture.cert, fixture.raw, RoleServer)
		if err != nil {
			t.Fatalf("VerifyDelegatedCredential: %v", err)
		}
		transcriptHash := sha256.Sum256([]byte("transcript"))
		signature := signCertificateVerify(t, fixture, transcriptHash[:], CertificateVerifyClientContext)
		identity, err := verified.VerifyCertificateVerify(transcriptHash[:], fixture.credential.CertVerifyAlgorithm, signature)
		if !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("error = %v, want ErrSignatureInvalid", err)
		}
		if identity != nil {
			t.Fatal("identity published despite cross-role context")
		}
	})
}

func TestTamperingFails(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	parentSigner := makeECDSAKey(t, elliptic.P256())
	cert := makeCertificate(t, now, parentSigner, nil)
	credentialSigner := makeECDSAKey(t, elliptic.P256())
	_, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")

	t.Run("valid_time_changed", func(t *testing.T) {
		tampered := append([]byte(nil), raw...)
		binary.BigEndian.PutUint32(tampered[:4], 7200)
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, tampered, RoleServer)
		if !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("error = %v, want ErrSignatureInvalid", err)
		}
	})

	t.Run("public_key_changed", func(t *testing.T) {
		otherKey := makeECDSAKey(t, elliptic.P256())
		otherDER, err := x509.MarshalPKIXPublicKey(otherKey.Public())
		if err != nil {
			t.Fatalf("MarshalPKIXPublicKey: %v", err)
		}
		tampered := append([]byte(nil), raw...)
		if len(tampered) < 9 {
			t.Fatal("raw credential too short")
		}
		copy(tampered[9:9+len(otherDER)], otherDER)
		_, err = (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, tampered, RoleServer)
		if !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("error = %v, want ErrSignatureInvalid", err)
		}
	})

	t.Run("certificate_substituted", func(t *testing.T) {
		otherParent := makeECDSAKey(t, elliptic.P256())
		otherCert := makeCertificate(t, now, otherParent, nil)
		_, err := (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(otherCert, raw, RoleServer)
		if !errors.Is(err, ErrSignatureInvalid) {
			t.Fatalf("error = %v, want ErrSignatureInvalid", err)
		}
	})
}

func TestParserRejectsMalformedInput(t *testing.T) {
	fixture := newValidFixture(t)
	if _, err := ParseDelegatedCredential(append(fixture.raw, 0x00)); !errors.Is(err, ErrMalformedCredential) {
		t.Fatalf("trailing byte error = %v, want ErrMalformedCredential", err)
	}
	if _, err := ParseDelegatedCredential(fixture.raw[:len(fixture.raw)-1]); !errors.Is(err, ErrMalformedCredential) {
		t.Fatalf("truncated input error = %v, want ErrMalformedCredential", err)
	}
}

func TestClientRoleAndOtherKeyTypes(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	t.Run("client_role_uses_client_contexts", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, func(template *x509.Certificate) {
			template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		})
		credentialSigner := makeECDSAKey(t, elliptic.P256())
		credential, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP256R1_SHA256, ECDSA_SECP256R1_SHA256, RoleClient, "")

		fixture := &testFixture{
			now:              now,
			cert:             cert,
			parentSigner:     parentSigner,
			credentialSigner: credentialSigner,
			credential:       credential,
			raw:              raw,
			role:             RoleClient,
		}
		transcriptHash := sha256.Sum256([]byte("client transcript"))
		identity, err := VerifyConnection(cert, raw, RoleClient, now, transcriptHash[:], credential.CertVerifyAlgorithm, signCertificateVerify(t, fixture, transcriptHash[:], ""))
		if err != nil {
			t.Fatalf("VerifyConnection: %v", err)
		}
		if identity == nil || identity.Role != RoleClient {
			t.Fatal("client identity not published")
		}
	})

	t.Run("ed25519_parent_and_delegated_key", func(t *testing.T) {
		parentSigner := makeEd25519Key(t)
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeEd25519Key(t)
		credential, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ED25519, ED25519, RoleServer, "")
		transcriptHash := sha256.Sum256([]byte("transcript"))
		message, err := CertificateVerifySigningData(transcriptHash[:], RoleServer)
		if err != nil {
			t.Fatalf("CertificateVerifySigningData: %v", err)
		}
		identity, err := VerifyConnection(cert, raw, RoleServer, now, transcriptHash[:], ED25519, signMessage(t, credentialSigner, ED25519, message))
		if err != nil {
			t.Fatalf("VerifyConnection: %v", err)
		}
		if identity == nil {
			t.Fatal("Ed25519 identity not published")
		}
		if credential.CertVerifyAlgorithm != ED25519 {
			t.Fatalf("scheme = %s", credential.CertVerifyAlgorithm)
		}
	})

	t.Run("rsa_parent_signs_delegated_credential", func(t *testing.T) {
		parentSigner := makeRSAKey(t)
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeECDSAKey(t, elliptic.P384())
		credential, raw := issueCredential(t, now, parentSigner, cert, credentialSigner, 3600, ECDSA_SECP384R1_SHA384, RSA_PSS_RSAE_SHA256, RoleServer, "")
		transcriptHash := sha256.Sum256([]byte("transcript"))
		message, err := CertificateVerifySigningData(transcriptHash[:], RoleServer)
		if err != nil {
			t.Fatalf("CertificateVerifySigningData: %v", err)
		}
		identity, err := VerifyConnection(cert, raw, RoleServer, now, transcriptHash[:], credential.CertVerifyAlgorithm, signMessage(t, credentialSigner, credential.CertVerifyAlgorithm, message))
		if err != nil {
			t.Fatalf("VerifyConnection: %v", err)
		}
		if identity == nil {
			t.Fatal("RSA-parent identity not published")
		}
	})

	t.Run("rsassa_pss_delegated_key", func(t *testing.T) {
		parentSigner := makeECDSAKey(t, elliptic.P256())
		cert := makeCertificate(t, now, parentSigner, nil)
		credentialSigner := makeRSAKey(t)
		publicKeyDER := marshalRSAPSSPublicKey(t, &credentialSigner.PublicKey)
		credential, raw := issueCredentialWithPublicKeyDER(t, now, parentSigner, cert, publicKeyDER, 3600, RSA_PSS_PSS_SHA256, ECDSA_SECP256R1_SHA256, RoleServer, "")

		transcriptHash := sha256.Sum256([]byte("transcript"))
		message, err := CertificateVerifySigningData(transcriptHash[:], RoleServer)
		if err != nil {
			t.Fatalf("CertificateVerifySigningData: %v", err)
		}
		identity, err := VerifyConnection(cert, raw, RoleServer, now, transcriptHash[:], credential.CertVerifyAlgorithm, signMessage(t, credentialSigner, credential.CertVerifyAlgorithm, message))
		if err != nil {
			t.Fatalf("VerifyConnection: %v", err)
		}
		if identity == nil {
			t.Fatal("RSASSA-PSS delegated-key identity not published")
		}
	})
}
