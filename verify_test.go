package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"
)

var serialCounter int64

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type certSpec struct {
	subject   string
	dns       []string
	ca        bool
	permitted []string
	excluded  []string
	key       *ecdsa.PrivateKey // subject key; nil generates a fresh one
}

// issue creates a certificate signed by parent/parentKey. A nil parent
// produces a self-signed certificate.
func issue(t *testing.T, spec certSpec, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := spec.key
	if key == nil {
		key = newKey(t)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(atomic.AddInt64(&serialCounter, 1)),
		Subject:               pkix.Name{CommonName: spec.subject},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		DNSNames:              spec.dns,
		PermittedDNSDomains:   spec.permitted,
		ExcludedDNSDomains:    spec.excluded,
		BasicConstraintsValid: true,
		IsCA:                  spec.ca,
	}
	if spec.ca {
		tmpl.KeyUsage = x509.KeyUsageCertSign
	}
	if parent == nil {
		parent = tmpl
		parentKey = key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, key.Public(), parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func constrainedRoot(t *testing.T, permitted []string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	return issue(t, certSpec{subject: "Root CA", ca: true, permitted: permitted}, nil, nil)
}

// A self-issued cross-certificate (same subject, same key) skips the
// name-constraint check for itself, so its own out-of-subtree name does
// not sink an otherwise legitimate path.
func TestVerifyPathSelfIssuedCrossCertificateAccepted(t *testing.T) {
	root, rootKey := constrainedRoot(t, []string{"example.com"})
	ca, caKey := issue(t, certSpec{subject: "Cross CA", ca: true, dns: []string{"ca.example.com"}}, root, rootKey)
	cross, _ := issue(t, certSpec{subject: "Cross CA", ca: true, dns: []string{"cross.example.net"}, key: caKey}, ca, caKey)
	leaf, _ := issue(t, certSpec{subject: "www.example.com", dns: []string{"www.example.com"}}, cross, caKey)

	if err := VerifyPath([]*x509.Certificate{leaf, cross, ca, root}); err != nil {
		t.Fatalf("VerifyPath rejected legitimate cross-certificate path: %v", err)
	}
}

// Two consecutive self-issued certificates must not wash constraints
// away: each skips the check only for itself, and the violating name
// placed after the second one is still caught.
func TestVerifyPathConsecutiveSelfIssuedKeepsConstraints(t *testing.T) {
	root, rootKey := constrainedRoot(t, []string{"example.com"})
	ca, caKey := issue(t, certSpec{subject: "Bridge CA", ca: true, dns: []string{"bridge.example.com"}}, root, rootKey)
	self1, _ := issue(t, certSpec{subject: "Bridge CA", ca: true, key: caKey}, ca, caKey)
	self2, _ := issue(t, certSpec{subject: "Bridge CA", ca: true, key: caKey}, self1, caKey)
	leaf, _ := issue(t, certSpec{subject: "evil.example.net", dns: []string{"evil.example.net"}}, self2, caKey)

	err := VerifyPath([]*x509.Certificate{leaf, self2, self1, ca, root})
	if !errors.Is(err, ErrNameConstraint) {
		t.Fatalf("VerifyPath err = %v, want ErrNameConstraint", err)
	}
}

// A violating name on an ordinary (non-self-issued) certificate fails.
func TestVerifyPathNonSelfIssuedViolationFails(t *testing.T) {
	root, rootKey := constrainedRoot(t, []string{"example.com"})
	mid, midKey := issue(t, certSpec{subject: "Bad CA", ca: true, dns: []string{"bad.other.org"}}, root, rootKey)
	leaf, _ := issue(t, certSpec{subject: "www.example.com", dns: []string{"www.example.com"}}, mid, midKey)

	err := VerifyPath([]*x509.Certificate{leaf, mid, root})
	if !errors.Is(err, ErrNameConstraint) {
		t.Fatalf("VerifyPath err = %v, want ErrNameConstraint", err)
	}
}

// Reusing the issuer's name with a different key is not self-issued, so
// the constraint check still applies to it.
func TestVerifyPathSameNameDifferentKeyNotSelfIssued(t *testing.T) {
	root, rootKey := constrainedRoot(t, []string{"example.com"})
	ca1, ca1Key := issue(t, certSpec{subject: "Rollover CA", ca: true, dns: []string{"ca.example.com"}}, root, rootKey)
	ca2, _ := issue(t, certSpec{subject: "Rollover CA", ca: true, dns: []string{"rolled.example.net"}}, ca1, ca1Key)

	if IsSelfIssued(ca2) {
		t.Fatal("IsSelfIssued reported true for a certificate with a different key")
	}
	err := VerifyPath([]*x509.Certificate{ca2, ca1, root})
	if !errors.Is(err, ErrNameConstraint) {
		t.Fatalf("VerifyPath err = %v, want ErrNameConstraint", err)
	}
}

// The self-issued exemption never applies to the leaf certificate.
func TestVerifyPathSelfIssuedLeafStillChecked(t *testing.T) {
	root, rootKey := constrainedRoot(t, []string{"example.com"})
	ca, caKey := issue(t, certSpec{subject: "Leaf CA", ca: true, dns: []string{"ca.example.com"}}, root, rootKey)
	leaf, _ := issue(t, certSpec{subject: "Leaf CA", dns: []string{"evil.example.net"}, key: caKey}, ca, caKey)

	err := VerifyPath([]*x509.Certificate{leaf, ca, root})
	if !errors.Is(err, ErrNameConstraint) {
		t.Fatalf("VerifyPath err = %v, want ErrNameConstraint", err)
	}
}

func TestVerifyPathHappyPath(t *testing.T) {
	root, rootKey := constrainedRoot(t, []string{"example.com"})
	ca, caKey := issue(t, certSpec{subject: "Sub CA", ca: true, dns: []string{"ca.example.com"}}, root, rootKey)
	leaf, _ := issue(t, certSpec{subject: "www.example.com", dns: []string{"www.example.com"}}, ca, caKey)

	if err := VerifyPath([]*x509.Certificate{leaf, ca, root}); err != nil {
		t.Fatalf("VerifyPath err = %v, want nil", err)
	}
}

func TestVerifyPathExcludedSubtree(t *testing.T) {
	root, rootKey := issue(t, certSpec{subject: "Root CA", ca: true, excluded: []string{"evil.example.com"}}, nil, nil)
	ca, caKey := issue(t, certSpec{subject: "Sub CA", ca: true}, root, rootKey)
	leaf, _ := issue(t, certSpec{subject: "www.evil.example.com", dns: []string{"www.evil.example.com"}}, ca, caKey)

	err := VerifyPath([]*x509.Certificate{leaf, ca, root})
	if !errors.Is(err, ErrNameConstraint) {
		t.Fatalf("VerifyPath err = %v, want ErrNameConstraint", err)
	}
}

func TestVerifyPathRejectsBadInput(t *testing.T) {
	if err := VerifyPath(nil); err == nil {
		t.Fatal("VerifyPath(nil) err = nil, want error")
	}
	root, rootKey := constrainedRoot(t, nil)
	mid, midKey := issue(t, certSpec{subject: "Sub CA", ca: true}, root, rootKey)
	leaf, _ := issue(t, certSpec{subject: "www.example.com"}, mid, midKey)
	if err := VerifyPath([]*x509.Certificate{leaf, mid}); err == nil {
		t.Fatal("VerifyPath with non-self-signed anchor err = nil, want error")
	}
}
