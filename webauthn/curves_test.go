package webauthn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"testing"
	"time"
)

// TestES384EndToEnd exercises the ES384/P-384 signature and certificate path,
// covering the SHA-384 digest branch end-to-end through full attestation.
func TestES384EndToEnd(t *testing.T) {
	f := newRegistrationFixture(t)

	p384CredKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-384 credential key: %v", err)
	}
	rpIDHash := sha256.Sum256([]byte(f.rpID))
	f.credentialKey = p384CredKey
	f.authData = buildAuthData(authDataOptions{
		rpIDHash:  rpIDHash[:],
		flags:     flagUP | flagAT,
		signCount: 11,
		aaguid:    f.aaguid,
		credID:    []byte("test-credential-identifier-128b!!"),
		coseKey:   encodeCOSEKey(&p384CredKey.PublicKey),
	})

	root := createRootCA(t)
	f.policy.TrustAnchors.AddCert(root.cert)

	leafKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-384 leaf key: %v", err)
	}
	leaf := createP384AttestationCert(t, root, f.aaguid, leafKey)

	digest := sha512.Sum384(f.signedPayload())
	signature, err := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	if err != nil {
		t.Fatalf("sign ES384: %v", err)
	}
	items := [][]byte{encBytes(leaf.der)}
	attStmt := encMap(
		mapEntry{textKey("alg"), encInt(algES384)},
		mapEntry{textKey("sig"), encBytes(signature)},
		mapEntry{textKey("x5c"), encArray(items...)},
	)
	obj := f.attestationObject("packed", attStmt)

	result := mustVerify(t, f, obj)
	if result.Credential.Algorithm != algES384 {
		t.Fatalf("expected ES384 credential, got alg %d", result.Credential.Algorithm)
	}
}

func createP384AttestationCert(t *testing.T, parent *certAuthority, aaguid AAGUID, leafKey *ecdsa.PrivateKey) *certAuthority {
	t.Helper()
	octetDER, err := asn1.Marshal(aaguid[:])
	if err != nil {
		t.Fatalf("marshal aaguid: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(200),
		Subject:               pkix.Name{CommonName: "P384 Security Key", OrganizationalUnit: []string{authenticatorAttestationOU}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  false,
		ExtraExtensions:       []pkix.Extension{{Id: oidFIDOAAGUID, Value: octetDER}},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent.cert, &leafKey.PublicKey, parent.private)
	if err != nil {
		t.Fatalf("create P-384 attestation cert: %v", err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &certAuthority{cert: cert, der: der, private: leafKey}
}
