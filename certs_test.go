package netsec_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"
)

type testAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

type certOptions struct {
	isCA bool

	dnsNames   []string
	emails     []string
	ips        []net.IP
	uris       []*url.URL
	permitted  []string
	excluded   []string
	permittedE []string
	excludedE  []string
	permittedI []*net.IPNet
	excludedI  []*net.IPNet
	permittedU []string
	excludedU  []string

	notBefore time.Time
	notAfter  time.Time

	extraExtensions []pkix.Extension
	maxPathLen      int
	setMaxPathLen   bool
}

var serialNumber int64 = 1

func nextSerial() *big.Int {
	serialNumber++
	return big.NewInt(serialNumber)
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func defaultTemplate(cn string, opts certOptions) *x509.Certificate {
	now := time.Now()
	notBefore := opts.notBefore
	if notBefore.IsZero() {
		notBefore = now.Add(-time.Hour)
	}
	notAfter := opts.notAfter
	if notAfter.IsZero() {
		notAfter = now.Add(24 * time.Hour)
	}

	tmpl := &x509.Certificate{
		SerialNumber:            nextSerial(),
		Subject:                 pkix.Name{CommonName: cn},
		NotBefore:               notBefore,
		NotAfter:                notAfter,
		BasicConstraintsValid:   true,
		IsCA:                    opts.isCA,
		DNSNames:                opts.dnsNames,
		EmailAddresses:          opts.emails,
		IPAddresses:             opts.ips,
		URIs:                    opts.uris,
		PermittedDNSDomains:     opts.permitted,
		ExcludedDNSDomains:      opts.excluded,
		PermittedEmailAddresses: opts.permittedE,
		ExcludedEmailAddresses:  opts.excludedE,
		PermittedIPRanges:       opts.permittedI,
		ExcludedIPRanges:        opts.excludedI,
		PermittedURIDomains:     opts.permittedU,
		ExcludedURIDomains:      opts.excludedU,
		ExtraExtensions:         opts.extraExtensions,
	}

	if opts.isCA {
		tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature
		tmpl.MaxPathLen = -1
		if opts.setMaxPathLen {
			tmpl.MaxPathLen = opts.maxPathLen
			tmpl.MaxPathLenZero = opts.maxPathLen == 0
		}
	} else {
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}

	if len(tmpl.PermittedDNSDomains)+len(tmpl.ExcludedDNSDomains)+
		len(tmpl.PermittedEmailAddresses)+len(tmpl.ExcludedEmailAddresses)+
		len(tmpl.PermittedIPRanges)+len(tmpl.ExcludedIPRanges)+
		len(tmpl.PermittedURIDomains)+len(tmpl.ExcludedURIDomains) > 0 {
		tmpl.PermittedDNSDomainsCritical = true
	}

	return tmpl
}

func issue(t *testing.T, tmpl *x509.Certificate, parent *testAuthority) *x509.Certificate {
	t.Helper()
	key := newKey(t)
	tmpl.PublicKey = &key.PublicKey

	parentCert := tmpl
	signer := key
	if parent != nil {
		parentCert = parent.cert
		signer = parent.key
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, parentCert, &key.PublicKey, signer)
	if err != nil {
		t.Fatalf("create certificate %q: %v", tmpl.Subject.CommonName, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate %q: %v", tmpl.Subject.CommonName, err)
	}
	return cert
}

func newRoot(t *testing.T, cn string, opts certOptions) *testAuthority {
	t.Helper()
	opts.isCA = true
	tmpl := defaultTemplate(cn, opts)
	key := newKey(t)
	tmpl.PublicKey = &key.PublicKey

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create root %q: %v", cn, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse root %q: %v", cn, err)
	}
	return &testAuthority{cert: cert, key: key}
}

func newIntermediate(t *testing.T, cn string, parent *testAuthority, opts certOptions) *testAuthority {
	t.Helper()
	opts.isCA = true
	tmpl := defaultTemplate(cn, opts)
	key := newKey(t)
	tmpl.PublicKey = &key.PublicKey

	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent.cert, &key.PublicKey, parent.key)
	if err != nil {
		t.Fatalf("create intermediate %q: %v", cn, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse intermediate %q: %v", cn, err)
	}
	return &testAuthority{cert: cert, key: key}
}

func newLeaf(t *testing.T, cn string, parent *testAuthority, opts certOptions) *x509.Certificate {
	t.Helper()
	opts.isCA = false
	tmpl := defaultTemplate(cn, opts)
	return issue(t, tmpl, parent)
}

func ipNet(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("parse CIDR %q: %v", cidr, err)
	}
	return network
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL %q: %v", raw, err)
	}
	return u
}
