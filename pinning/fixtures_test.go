package pinning

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

const testServerName = "gw.example.net"

var testStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newCA(t *testing.T, name string, parent *testCA) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(t),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             testStart.Add(-time.Hour),
		NotAfter:              testStart.Add(3650 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	parentCert := tmpl
	var parentKey *ecdsa.PrivateKey = key
	if parent != nil {
		parentCert = parent.cert
		parentKey = parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parentCert, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key}
}

type testLeaf struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func issueLeaf(t *testing.T, issuer *testCA, dnsName string) *testLeaf {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(t),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    testStart.Add(-time.Hour),
		NotAfter:     testStart.Add(3650 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, issuer.cert, &key.PublicKey, issuer.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testLeaf{cert: cert, key: key}
}

// rawChain returns the presented wire chain: leaf followed by the given
// issuers in order. The root is included when it is not self-issued or
// when explicitly passed.
func rawChain(leaf *testLeaf, issuers ...*testCA) [][]byte {
	raw := [][]byte{leaf.cert.Raw}
	for _, ca := range issuers {
		raw = append(raw, ca.cert.Raw)
	}
	return raw
}

func tlsCert(leaf *testLeaf, issuers ...*testCA) tls.Certificate {
	return tls.Certificate{
		Certificate: rawChain(leaf, issuers...),
		PrivateKey:  leaf.key,
		Leaf:        leaf.cert,
	}
}

func rootPool(roots ...*testCA) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, r := range roots {
		pool.AddCert(r.cert)
	}
	return pool
}

func randomSerial(t *testing.T) *big.Int {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	return serial
}

// fixture builds the two declared chains of the gateway: a primary chain
// and a backup chain under independent roots.
type fixture struct {
	primaryRoot *testCA
	primaryInt  *testCA
	primaryLeaf *testLeaf

	backupRoot *testCA
	backupInt  *testCA
	backupLeaf *testLeaf
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	f.primaryRoot = newCA(t, "primary-root", nil)
	f.primaryInt = newCA(t, "primary-intermediate", f.primaryRoot)
	f.primaryLeaf = issueLeaf(t, f.primaryInt, testServerName)
	f.backupRoot = newCA(t, "backup-root", nil)
	f.backupInt = newCA(t, "backup-intermediate", f.backupRoot)
	f.backupLeaf = issueLeaf(t, f.backupInt, testServerName)
	return f
}

// pinSet declares both chains: every leaf key and every issuer key of
// the primary and the backup chain.
func (f *fixture) pinSet() *PinSet {
	pins := NewPinSet()
	pins.PinLeaf(f.primaryLeaf.cert)
	pins.PinLeaf(f.backupLeaf.cert)
	for _, ca := range []*testCA{f.primaryInt, f.primaryRoot, f.backupInt, f.backupRoot} {
		pins.PinIssuer(ca.cert)
	}
	return pins
}

func (f *fixture) roots() *x509.CertPool {
	return rootPool(f.primaryRoot, f.backupRoot)
}

const purposeGateway Purpose = "gateway-api"

// policy builds a policy valid in [notBefore, notAfter) carrying the
// full fixture pin set for the gateway purpose.
func (f *fixture) policy(version string, notBefore, notAfter time.Time) *Policy {
	return &Policy{
		Version:   version,
		NotBefore: notBefore,
		NotAfter:  notAfter,
		Roots:     f.roots(),
		Pins:      map[Purpose]*PinSet{purposeGateway: f.pinSet()},
	}
}
