package workloadid

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"net"
	"net/url"
	"sync/atomic"
	"time"

	"prescreen.local/ps-174fa6fd5f/internal/ocsp"
)

type fakeClock struct {
	t atomic.Value
}

func newFakeClock(t time.Time) *fakeClock {
	c := &fakeClock{}
	c.t.Store(t)
	return c
}

func (c *fakeClock) Now() Time { return c.t.Load().(time.Time) }

func (c *fakeClock) Advance(d Duration) { c.t.Store(c.Now().Add(d)) }

func (c *fakeClock) Set(t Time) { c.t.Store(t) }

// testNow is the time source used by all certificate/CRL/OCSP test helpers.
// Tests point it at a fakeClock so generated material shares the verifier time.
var testNow = time.Now

type ca struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

type leafBundle struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

type leafOptions struct {
	serial          int64
	dns             []string
	ips             []net.IP
	uris            []*url.URL
	ekus            []x509.ExtKeyUsage
	notBeforeOffset Duration
	ttl             Duration
	keyUsage        x509.KeyUsage
	isCA            bool
	permittedDNS    []string
	excludedDNS     []string
	permittedURI    []string
	excludedURI     []string
	permittedIP     []*net.IPNet
	excludedIP      []*net.IPNet
}

func newRoot(name string) (*ca, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name, Organization: []string{name}},
		NotBefore:             testNow().Add(-time.Hour),
		NotAfter:              testNow().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return &ca{cert: cert, key: key}, err
}

func newIntermediate(parent *ca, name string, opts leafOptions) (*ca, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(opts.serial),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             testNow().Add(opts.notBeforeOffset),
		NotAfter:              testNow().Add(opts.notBeforeOffset + opts.ttl),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		PermittedDNSDomains:   opts.permittedDNS,
		ExcludedDNSDomains:    opts.excludedDNS,
		PermittedURIDomains:   opts.permittedURI,
		ExcludedURIDomains:    opts.excludedURI,
		PermittedIPRanges:     opts.permittedIP,
		ExcludedIPRanges:      opts.excludedIP,
	}
	if len(opts.permittedDNS) > 0 || len(opts.excludedDNS) > 0 || len(opts.permittedURI) > 0 ||
		len(opts.excludedURI) > 0 || len(opts.permittedIP) > 0 || len(opts.excludedIP) > 0 {
		template.PermittedDNSDomainsCritical = true
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent.cert, &key.PublicKey, parent.key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return &ca{cert: cert, key: key}, err
}

func newLeaf(parent *ca, opts leafOptions) (*leafBundle, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	keyUsage := opts.keyUsage
	if keyUsage == 0 {
		keyUsage = x509.KeyUsageDigitalSignature
	}
	ekus := opts.ekus
	if len(ekus) == 0 {
		ekus = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}
	}
	ttl := opts.ttl
	if ttl == 0 {
		ttl = time.Hour
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(opts.serial),
		Subject:               pkix.Name{CommonName: "workload"},
		NotBefore:             testNow().Add(opts.notBeforeOffset),
		NotAfter:              testNow().Add(opts.notBeforeOffset + ttl),
		KeyUsage:              keyUsage,
		ExtKeyUsage:           ekus,
		DNSNames:              opts.dns,
		IPAddresses:           opts.ips,
		URIs:                  opts.uris,
		IsCA:                  opts.isCA,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent.cert, &key.PublicKey, parent.key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return &leafBundle{cert: cert, key: key, der: der}, err
}

func mustURI(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func makeCRL(signer *ca, revokedSerials []*big.Int, now, expiry Time) ([]byte, error) {
	template := &x509.RevocationList{
		Number:     big.NewInt(77),
		ThisUpdate: now,
		NextUpdate: expiry,
	}
	for _, serial := range revokedSerials {
		template.RevokedCertificateEntries = append(template.RevokedCertificateEntries,
			x509.RevocationListEntry{SerialNumber: serial, RevocationTime: now.Add(-time.Minute)})
	}
	return x509.CreateRevocationList(rand.Reader, template, signer.cert, signer.key)
}

func makeOCSP(issuer *ca, leaf *x509.Certificate, status ocsp.CertStatus, now, nextUpdate Time, revokedAt Time) ([]byte, error) {
	return ocsp.CreateResponse(ocsp.CreateInput{
		Issuer:     issuer.cert,
		Responder:  issuer.cert,
		Signer:     issuer.key,
		Leaf:       leaf,
		Status:     status,
		ThisUpdate: now,
		NextUpdate: nextUpdate,
		RevokedAt:  revokedAt,
	})
}

func parseOCSP(der []byte) (*ocsp.Response, error) {
	resp, _, err := ocsp.ParseResponse(der)
	return resp, err
}

func derChain(certs ...*x509.Certificate) [][]byte {
	out := make([][]byte, 0, len(certs))
	for _, cert := range certs {
		out = append(out, cert.Raw)
	}
	return out
}

var _ crypto.Signer = (*ecdsa.PrivateKey)(nil)
var _ asn1.RawValue
