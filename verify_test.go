package netsec

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

var testIdentityOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 9}

const (
	testArtifactName   = "artifact.tar.gz"
	testArtifactDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testRepository     = "github.com/acme/app"
	testCommit         = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	testBuilder        = "https://builders.example/acme"
)

type testPKI struct {
	root    *x509.Certificate
	leaf    *x509.Certificate
	leafKey *ecdsa.PrivateKey
	roots   *x509.CertPool
	now     time.Time
}

func newTestPKI(t *testing.T, identity string) *testPKI {
	t.Helper()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identityValue, err := asn1.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "certificate subject is not the signer identity"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtraExtensions: []pkix.Extension{{
			Id:    testIdentityOID,
			Value: identityValue,
		}},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, root, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(root)
	return &testPKI{root: root, leaf: leaf, leafKey: leafKey, roots: roots, now: now}
}

func (p *testPKI) statement(t *testing.T, subjects []Subject) []byte {
	t.Helper()
	statement := Statement{
		Type:          statementTypeV1,
		Subject:       subjects,
		PredicateType: "https://slsa.dev/provenance/v1",
		Predicate: mustJSON(t, map[string]any{
			"repository": testRepository,
			"commit":     testCommit,
			"builder":    testBuilder,
		}),
	}
	return mustJSON(t, statement)
}

func (p *testPKI) envelope(t *testing.T, payload []byte, signatureCount int) Envelope {
	t.Helper()
	digest := sha256.Sum256(PAE(InTotoPayloadType, payload))
	rawSignature, err := ecdsa.SignASN1(rand.Reader, p.leafKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	encodedSignature := base64.StdEncoding.EncodeToString(rawSignature)
	signatures := make([]Signature, 0, signatureCount)
	for i := 0; i < signatureCount; i++ {
		signatures = append(signatures, Signature{
			KeyID: "test-key",
			Sig:   encodedSignature,
		})
	}
	return Envelope{
		PayloadType: InTotoPayloadType,
		Payload:     base64.StdEncoding.EncodeToString(payload),
		Signatures:  signatures,
	}
}

func validPolicy(p *testPKI, publish func(*VerifiedStatement) error) Policy {
	return Policy{
		TargetName:   testArtifactName,
		TargetDigest: map[string]string{"sha256": testArtifactDigest},
		Repository:   testRepository,
		Commit:       testCommit,
		Builder:      testBuilder,
		Identity:     testBuilder,
		IdentityOID:  testIdentityOID,
		Roots:        p.roots,
		Time:         p.now,
		Publish:      publish,
	}
}

func testSubject() Subject {
	return Subject{
		Name:   testArtifactName,
		Digest: map[string]string{"sha256": testArtifactDigest},
	}
}

func TestPAEFormat(t *testing.T) {
	payload := []byte("payload")
	want := "DSSEv1 " + "28" + " " + InTotoPayloadType + " " + "7" + " " + string(payload)
	if got := string(PAE(InTotoPayloadType, payload)); got != want {
		t.Fatalf("PAE mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestCompleteBindingPublishes(t *testing.T) {
	pki := newTestPKI(t, testBuilder)
	envelope := pki.envelope(t, pki.statement(t, []Subject{testSubject()}), 1)

	published := 0
	result, err := Verify(context.Background(), envelope, []*x509.Certificate{pki.leaf, pki.root}, validPolicy(pki, func(statement *VerifiedStatement) error {
		published++
		if statement.SignerIdentity != testBuilder {
			t.Errorf("unexpected signer identity %q", statement.SignerIdentity)
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("valid attestation rejected: %v", err)
	}
	if result == nil || result.Signer == nil {
		t.Fatal("verified result has no signer")
	}
	if published != 1 {
		t.Fatalf("publish callback called %d times, want 1", published)
	}
}

func TestPayloadTypeSwapRejected(t *testing.T) {
	pki := newTestPKI(t, testBuilder)
	envelope := pki.envelope(t, pki.statement(t, []Subject{testSubject()}), 1)
	envelope.PayloadType = "application/vnd.in-toto+json.evil"

	published := 0
	_, err := Verify(context.Background(), envelope, []*x509.Certificate{pki.leaf, pki.root}, validPolicy(pki, func(*VerifiedStatement) error {
		published++
		return nil
	}))
	if !errors.Is(err, ErrUnknownPayloadType) {
		t.Fatalf("got error %v, want ErrUnknownPayloadType", err)
	}
	if published != 0 {
		t.Fatalf("publish callback called %d times after rejected payload type", published)
	}
}

func TestExtraSubjectRejected(t *testing.T) {
	pki := newTestPKI(t, testBuilder)
	extra := Subject{
		Name:   "other.tar.gz",
		Digest: map[string]string{"sha256": strings.Repeat("b", 64)},
	}
	envelope := pki.envelope(t, pki.statement(t, []Subject{testSubject(), extra}), 1)

	published := 0
	_, err := Verify(context.Background(), envelope, []*x509.Certificate{pki.leaf, pki.root}, validPolicy(pki, func(*VerifiedStatement) error {
		published++
		return nil
	}))
	if err == nil || !strings.Contains(err.Error(), "exactly one subject") {
		t.Fatalf("extra subject was not rejected: %v", err)
	}
	if published != 0 {
		t.Fatalf("publish callback called %d times after extra subject", published)
	}
}

func TestSameRepositoryNameDifferentIdentityRejected(t *testing.T) {
	trusted := newTestPKI(t, "https://evil.example/acme")
	envelope := trusted.envelope(t, trusted.statement(t, []Subject{testSubject()}), 1)

	published := 0
	policy := validPolicy(trusted, func(*VerifiedStatement) error {
		published++
		return nil
	})
	_, err := Verify(context.Background(), envelope, []*x509.Certificate{trusted.leaf, trusted.root}, policy)
	if err == nil || !strings.Contains(err.Error(), "certificate identity") {
		t.Fatalf("different certificate identity was not rejected: %v", err)
	}
	if published != 0 {
		t.Fatalf("publish callback called %d times after identity mismatch", published)
	}
}

func TestDifferentRepositoryOwnerRejected(t *testing.T) {
	pki := newTestPKI(t, testBuilder)
	statement := pki.statement(t, []Subject{testSubject()})
	statement = []byte(strings.Replace(string(statement), `"github.com/acme/app"`, `"github.com/evil/app"`, 1))
	envelope := pki.envelope(t, statement, 1)

	_, err := Verify(context.Background(), envelope, []*x509.Certificate{pki.leaf, pki.root}, validPolicy(pki, nil))
	if err == nil || !strings.Contains(err.Error(), "repository") {
		t.Fatalf("different repository owner was not rejected: %v", err)
	}
}

func TestWrongArtifactDigestRejected(t *testing.T) {
	pki := newTestPKI(t, testBuilder)
	subject := testSubject()
	subject.Digest["sha256"] = strings.Repeat("f", 64)
	envelope := pki.envelope(t, pki.statement(t, []Subject{subject}), 1)

	_, err := Verify(context.Background(), envelope, []*x509.Certificate{pki.leaf, pki.root}, validPolicy(pki, nil))
	if err == nil || !strings.Contains(err.Error(), "does not match target artifact") {
		t.Fatalf("wrong artifact digest was not rejected: %v", err)
	}
}

func TestPredicateBuilderMismatchRejected(t *testing.T) {
	pki := newTestPKI(t, testBuilder)
	statement := pki.statement(t, []Subject{testSubject()})
	statement = []byte(strings.Replace(string(statement), testBuilder, "https://builders.example/evil", 1))
	envelope := pki.envelope(t, statement, 1)

	_, err := Verify(context.Background(), envelope, []*x509.Certificate{pki.leaf, pki.root}, validPolicy(pki, nil))
	if err == nil || !strings.Contains(err.Error(), "predicate builder") {
		t.Fatalf("wrong predicate builder was not rejected: %v", err)
	}
}

func TestDuplicateSignerRejected(t *testing.T) {
	pki := newTestPKI(t, testBuilder)
	envelope := pki.envelope(t, pki.statement(t, []Subject{testSubject()}), 2)

	published := 0
	_, err := Verify(context.Background(), envelope, []*x509.Certificate{pki.leaf, pki.root}, validPolicy(pki, func(*VerifiedStatement) error {
		published++
		return nil
	}))
	if !errors.Is(err, ErrDuplicateSigner) {
		t.Fatalf("got error %v, want ErrDuplicateSigner", err)
	}
	if published != 0 {
		t.Fatalf("publish callback called %d times after duplicate signer", published)
	}
}

func TestSignatureTamperRejectedWithoutPublish(t *testing.T) {
	pki := newTestPKI(t, testBuilder)
	envelope := pki.envelope(t, pki.statement(t, []Subject{testSubject()}), 1)
	envelope.Signatures[0].Sig = base64.StdEncoding.EncodeToString([]byte("not a signature"))

	published := 0
	_, err := Verify(context.Background(), envelope, []*x509.Certificate{pki.leaf, pki.root}, validPolicy(pki, func(*VerifiedStatement) error {
		published++
		return nil
	}))
	if err == nil {
		t.Fatal("tampered signature was accepted")
	}
	if published != 0 {
		t.Fatalf("publish callback called %d times after invalid signature", published)
	}
}

func TestCertificateMustBeValidAtPolicyTime(t *testing.T) {
	pki := newTestPKI(t, testBuilder)
	envelope := pki.envelope(t, pki.statement(t, []Subject{testSubject()}), 1)
	policy := validPolicy(pki, nil)
	policy.Time = pki.now.Add(2 * time.Hour)

	_, err := Verify(context.Background(), envelope, []*x509.Certificate{pki.leaf, pki.root}, policy)
	if err == nil || !strings.Contains(err.Error(), "not valid at policy time") {
		t.Fatalf("certificate outside policy time was not rejected: %v", err)
	}
}

func TestIdentityIsNotReadFromSubject(t *testing.T) {
	pki := newTestPKI(t, "https://actual.example/builder")
	pki.leaf.Subject.CommonName = testBuilder
	envelope := pki.envelope(t, pki.statement(t, []Subject{testSubject()}), 1)

	_, err := Verify(context.Background(), envelope, []*x509.Certificate{pki.leaf, pki.root}, validPolicy(pki, nil))
	if err == nil || !strings.Contains(err.Error(), "certificate identity") {
		t.Fatalf("certificate Subject was accepted as identity: %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
