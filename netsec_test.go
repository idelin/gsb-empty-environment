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
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	netsec "example.org/netsec/q0217"
)

func TestQ0217RSAWithPSSAccepted(t *testing.T) {
	key := q0217RSAKey(t)
	cert := q0217Cert(t, &key.PublicKey, key)
	message := []byte("certificate verify context")
	digest := sha256.Sum256(message)

	exact, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest[:], &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
		Hash:       crypto.SHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := netsec.VerifySignature(cert, tls.PSSWithSHA256, message, exact); err != nil {
		t.Fatalf("RSA certificate with rsa_pss_rsae_sha256 should be accepted: %v", err)
	}

	// Large-salt PSS is still a valid RSA-PSS signature and is accepted by
	// the compatibility-preserving verifier.
	auto, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest[:], nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := netsec.VerifySignature(cert, "rsa_pss_rsae_sha256", message, auto); err != nil {
		t.Fatalf("RSA-PSS signature with auto salt should be accepted: %v", err)
	}
}

func TestQ0217RSAWithPKCS1Accepted(t *testing.T) {
	key := q0217RSAKey(t)
	cert := q0217Cert(t, &key.PublicKey, key)
	message := []byte("message")
	digest := sha256.Sum256(message)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := netsec.VerifySignature(cert, tls.PKCS1WithSHA256, message, sig); err != nil {
		t.Fatalf("RSA certificate with rsa_pkcs1_sha256 should be accepted: %v", err)
	}
}

func TestQ0217RSACertificateRejectsECDSAAndTampering(t *testing.T) {
	key := q0217RSAKey(t)
	cert := q0217Cert(t, &key.PublicKey, key)
	message := []byte("certificate verify context")
	digest := sha256.Sum256(message)
	sig, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest[:], &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
		Hash:       crypto.SHA256,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := netsec.VerifySignature(cert, tls.ECDSAWithP256AndSHA256, message, sig); err == nil {
		t.Fatal("RSA certificate with ecdsa_secp256r1_sha256 must be rejected")
	} else if !errors.Is(err, netsec.ErrSignatureSchemeMismatch) {
		t.Fatalf("expected mismatch error, got %v", err)
	}

	tampered := append([]byte(nil), sig...)
	tampered[len(tampered)-1] ^= 0xff
	if err := netsec.VerifySignature(cert, tls.PSSWithSHA256, message, tampered); err == nil {
		t.Fatal("tampered RSA-PSS signature must be rejected")
	}

	if err := netsec.VerifySignature(cert, tls.PSSWithSHA256, []byte("other message"), sig); err == nil {
		t.Fatal("signature over a different message must be rejected")
	}
}

func TestQ0217ECDSACompatibleCurveAccepted(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := q0217Cert(t, &key.PublicKey, key)
	message := []byte("message")
	digest := sha256.Sum256(message)
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := netsec.VerifySignature(cert, tls.ECDSAWithP256AndSHA256, message, sig); err != nil {
		t.Fatalf("P-256 certificate with ecdsa_secp256r1_sha256 should be accepted: %v", err)
	}

	if err := netsec.VerifySignature(cert, tls.ECDSAWithP384AndSHA384, message, sig); err == nil {
		t.Fatal("P-256 key with ecdsa_secp384r1_sha384 must be rejected")
	} else if !errors.Is(err, netsec.ErrSignatureSchemeMismatch) {
		t.Fatalf("expected curve mismatch error, got %v", err)
	}
}

func TestQ0217ECDSAP384Accepted(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := q0217Cert(t, &key.PublicKey, key)
	message := []byte("message")
	digest := sha512.Sum384(message)
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := netsec.VerifySignature(cert, tls.ECDSAWithP384AndSHA384, message, sig); err != nil {
		t.Fatalf("P-384 certificate with ecdsa_secp384r1_sha384 should be accepted: %v", err)
	}
}

func TestQ0217Ed25519AcceptedAndMismatchRejected(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := q0217Cert(t, pub, priv)
	message := []byte("message")
	sig := ed25519.Sign(priv, message)
	if err := netsec.VerifySignature(cert, tls.Ed25519, message, sig); err != nil {
		t.Fatalf("Ed25519 certificate should be accepted: %v", err)
	}
	if err := netsec.VerifySignature(cert, tls.PSSWithSHA256, message, sig); err == nil {
		t.Fatal("Ed25519 key with RSA-PSS scheme must be rejected")
	}
}

func TestQ0217SchemeOnlyChecks(t *testing.T) {
	rsaKey := q0217RSAKey(t)
	if err := netsec.CheckSignatureScheme(&rsaKey.PublicKey, tls.PSSWithSHA256); err != nil {
		t.Fatalf("RSA/PSS scheme check failed: %v", err)
	}
	if !netsec.SignatureSchemeMatches(&rsaKey.PublicKey, tls.PSSWithSHA256) {
		t.Fatal("RSA/PSS should match")
	}
	if netsec.SignatureSchemeMatches(&rsaKey.PublicKey, tls.ECDSAWithP256AndSHA256) {
		t.Fatal("RSA/ECDSA must not match")
	}
	if err := netsec.VerifySignatureScheme(&rsaKey.PublicKey, "ecdsa_secp256r1_sha256"); err == nil {
		t.Fatal("string scheme mismatch must be rejected")
	}
}

func TestQ0217CertificateFormsAndSchemeNames(t *testing.T) {
	key := q0217RSAKey(t)
	cert := q0217Cert(t, &key.PublicKey, key)
	message := []byte("message")
	digest := sha256.Sum256(message)
	sig, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest[:], nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := netsec.VerifySignature(cert.Raw, "rsa_pss_rsae_sha256", message, sig); err != nil {
		t.Fatalf("DER certificate with string scheme should verify: %v", err)
	}
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := netsec.VerifySignature(pemCert, tls.PSSWithSHA256, message, sig); err != nil {
		t.Fatalf("PEM certificate should verify: %v", err)
	}
	if err := netsec.VerifySignature(tls.Certificate{Certificate: [][]byte{cert.Raw}}, tls.PSSWithSHA256, message, sig); err != nil {
		t.Fatalf("tls.Certificate should verify: %v", err)
	}
	if err := netsec.VerifySignature(cert, tls.PSSWithSHA256, message, sig); err != nil {
		t.Fatalf("x509.Certificate should verify: %v", err)
	}

	// Argument order tolerance.
	if err := netsec.CheckSignatureScheme(tls.PSSWithSHA256, cert); err != nil {
		t.Fatalf("reversed scheme/key arguments should be accepted: %v", err)
	}
}

func TestQ0217CertificateDeclaredKeyMismatchRejected(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := q0217Cert(t, &key.PublicKey, key)
	cert.PublicKeyAlgorithm = x509.RSA
	if err := netsec.CheckSignatureScheme(cert, tls.ECDSAWithP256AndSHA256); err == nil {
		t.Fatal("certificate with contradictory declared key algorithm must be rejected")
	}
}

func q0217RSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func q0217Cert(t *testing.T, pub any, priv any) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
