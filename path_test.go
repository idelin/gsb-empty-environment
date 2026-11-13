package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

type testCertificate struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func issueCertificate(
	t *testing.T,
	subject string,
	key *ecdsa.PrivateKey,
	parent *testCertificate,
	isCA bool,
	dnsNames ...string,
) *testCertificate {
	t.Helper()

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: subject},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  isCA,
		DNSNames:              dnsNames,
	}
	if isCA {
		template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	}

	parentCert := template
	parentKey := key
	if parent != nil {
		parentCert = parent.cert
		parentKey = parent.key
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parentCert, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatalf("create certificate %q: %v", subject, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate %q: %v", subject, err)
	}
	return &testCertificate{cert: cert, key: key}
}

func TestValidateDistinctSubjectPath(t *testing.T) {
	root := issueCertificate(t, "Root CA", newTestKey(t), nil, true)
	intermediateOne := issueCertificate(t, "Intermediate One", newTestKey(t), root, true)
	intermediateTwo := issueCertificate(t, "Intermediate Two", newTestKey(t), intermediateOne, true)
	leaf := issueCertificate(t, "Leaf", newTestKey(t), intermediateTwo, false, "leaf.example")

	path := []*x509.Certificate{
		leaf.cert,
		intermediateTwo.cert,
		intermediateOne.cert,
		root.cert,
	}

	if err := ValidatePath(path); err != nil {
		t.Fatalf("distinct-subject path rejected: %v", err)
	}
	if err := ValidatePath(path, WithSignatureVerification()); err != nil {
		t.Fatalf("distinct-subject path failed signature validation: %v", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(root.cert)
	intermediates := x509.NewCertPool()
	intermediates.AddCert(intermediateOne.cert)
	intermediates.AddCert(intermediateTwo.cert)
	if err := ValidatePath(
		path,
		WithTrustVerification(roots),
		WithIntermediates(intermediates),
		WithDNSName("leaf.example"),
	); err != nil {
		t.Fatalf("distinct-subject path failed trust verification: %v", err)
	}

	reversed := []*x509.Certificate{
		root.cert,
		intermediateOne.cert,
		intermediateTwo.cert,
		leaf.cert,
	}
	if err := ValidatePath(reversed, WithSignatureVerification()); err != nil {
		t.Fatalf("reversed distinct-subject path failed signature validation: %v", err)
	}
}

func TestValidateRejectsFirstIntermediateRepeatedAtEnd(t *testing.T) {
	root := issueCertificate(t, "Root CA", newTestKey(t), nil, true)
	intermediateOne := issueCertificate(t, "Intermediate One", newTestKey(t), root, true)
	intermediateTwo := issueCertificate(t, "Intermediate Two", newTestKey(t), intermediateOne, true)
	leaf := issueCertificate(t, "Leaf", newTestKey(t), intermediateTwo, false, "leaf.example")

	path := []*x509.Certificate{
		leaf.cert,
		intermediateTwo.cert,
		intermediateOne.cert,
		root.cert,
	}
	attackedPath := append(append([]*x509.Certificate(nil), path...), path[1])

	err := ValidatePath(attackedPath)
	if err == nil {
		t.Fatal("path with a repeated first intermediate was accepted")
	}
	if !errors.Is(err, ErrDuplicateSubjectPublicKey) {
		t.Fatalf("expected duplicate error, got %v", err)
	}

	first, second, found := FindDuplicateSubjectPublicKey(attackedPath)
	if !found || first != 1 || second != 4 {
		t.Fatalf("duplicate positions = (%d, %d, %v), want (1, 4, true)", first, second, found)
	}
}

func TestRepeatedSubjectAndKeyIgnoresSubjectAltNames(t *testing.T) {
	root := issueCertificate(t, "Root CA", newTestKey(t), nil, true)
	intermediateOne := issueCertificate(t, "Intermediate One", newTestKey(t), root, true)
	intermediateTwo := issueCertificate(t, "Intermediate Two", newTestKey(t), intermediateOne, true, "first.example")

	sameSubjectAndKey := issueCertificate(
		t,
		"Intermediate Two",
		intermediateTwo.key,
		intermediateOne,
		true,
		"second.example",
	)

	if !SubjectPublicKeyEqual(intermediateTwo.cert, sameSubjectAndKey.cert) {
		t.Fatal("certificates with the same subject and public key were treated as distinct")
	}

	path := []*x509.Certificate{
		intermediateTwo.cert,
		intermediateOne.cert,
		root.cert,
		sameSubjectAndKey.cert,
	}
	if err := ValidatePath(path); !errors.Is(err, ErrDuplicateSubjectPublicKey) {
		t.Fatalf("expected duplicate subject/public-key error, got %v", err)
	}
	if !AlreadyInChain(sameSubjectAndKey.cert, path[:3]) {
		t.Fatal("AlreadyInChain did not detect the repeated subject and public key")
	}
}

func TestRepeatedKeyWithDistinctSubjectsIsAccepted(t *testing.T) {
	root := issueCertificate(t, "Root CA", newTestKey(t), nil, true)
	sharedKey := newTestKey(t)
	first := issueCertificate(t, "First", sharedKey, root, true)
	second := issueCertificate(t, "Second", sharedKey, first, true)

	if err := ValidatePath([]*x509.Certificate{second.cert, first.cert, root.cert}); err != nil {
		t.Fatalf("distinct-subject path with a shared public key was rejected: %v", err)
	}
}

func TestRepeatedSubjectWithDistinctKeysIsAccepted(t *testing.T) {
	first := issueCertificate(t, "Shared Name", newTestKey(t), nil, true)
	second := issueCertificate(t, "Shared Name", newTestKey(t), first, true)

	if err := ValidatePath([]*x509.Certificate{second.cert, first.cert}); err != nil {
		t.Fatalf("same-subject path with distinct public keys was rejected: %v", err)
	}
}

func TestValidateRejectsNilAndEmptyPaths(t *testing.T) {
	if err := ValidatePath([]*x509.Certificate(nil)); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("empty path error = %v, want ErrEmptyPath", err)
	}
	if err := ValidatePath([]*x509.Certificate{nil}); !errors.Is(err, ErrNilCertificate) {
		t.Fatalf("nil certificate error = %v, want ErrNilCertificate", err)
	}
}
