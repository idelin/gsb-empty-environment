package ocspcheck

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"
)

// testPKI bundles every certificate and key needed to build and sign OCSP
// responses for the leaf certificate.
type testPKI struct {
	now time.Time

	caKey  *ecdsa.PrivateKey
	caCert *x509.Certificate

	leafKey  *ecdsa.PrivateKey
	leafCert *x509.Certificate

	// otherLeaf is a second certificate from the same CA, used to prove that a
	// response naming a different serial is rejected.
	otherCert *x509.Certificate

	// delegated is a proper id-kp-OCSPSigning responder.
	delegatedKey  *ecdsa.PrivateKey
	delegatedCert *x509.Certificate

	// noEKUDelegation is otherwise identical to delegated but lacks the
	// OCSPSigning extended key usage (the "delegation over-privilege" case:
	// any ordinary end-entity cert trying to act as responder).
	noEKUKey  *ecdsa.PrivateKey
	noEKUCert *x509.Certificate

	// caDelegation is an OCSPSigning certificate with IsCA set, which RFC
	// 6960 forbids for delegated responders.
	caDelegationCert *x509.Certificate
	caDelegationKey  *ecdsa.PrivateKey

	// rsaKey/rsaCert exercise an RSA-based issuing CA and RSA signatures.
	rsaCAKey  *rsa.PrivateKey
	rsaCACert *x509.Certificate
}

// certValidity returns a wide validity window anchored to the real wall clock
// so that crypto/tls chain verification (which cannot be clock-injected)
// succeeds. OCSP freshness is still exercised against fixedNow.
func certValidity() (time.Time, time.Time) {
	base := time.Now()
	return base.Add(-2 * time.Hour), base.Add(24 * time.Hour)
}

func newTestPKI(now time.Time) *testPKI {
	p := &testPKI{now: now}

	p.caKey, p.caCert = p.mustECDSACA("Test OCSP Root CA")

	p.leafKey = mustECDSAKey()
	p.leafCert = p.mustLeaf(p.caCert, p.caKey, p.leafKey.Public(), "example.test", 1001)

	otherKey := mustECDSAKey()
	p.otherCert = p.mustLeaf(p.caCert, p.caKey, otherKey.Public(), "other.example.test", 1002)

	p.delegatedKey = mustECDSAKey()
	p.delegatedCert = p.mustResponder(p.caCert, p.caKey, p.delegatedKey.Public(), "OCSP Responder", 2001, responderOpts{
		isCA: false, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageOCSPSigning},
	})

	p.noEKUKey = mustECDSAKey()
	p.noEKUCert = p.mustResponder(p.caCert, p.caKey, p.noEKUKey.Public(), "Ordinary End Entity", 2002, responderOpts{
		isCA: false, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})

	p.caDelegationKey = mustECDSAKey()
	p.caDelegationCert = p.mustResponder(p.caCert, p.caKey, p.caDelegationKey.Public(), "Rogue CA Responder", 2003, responderOpts{
		isCA: true, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageOCSPSigning},
	})

	p.rsaCAKey = mustRSAKey()
	p.rsaCACert = p.mustRSACA("RSA OCSP Root CA", p.rsaCAKey)

	return p
}

type responderOpts struct {
	isCA bool
	eku  []x509.ExtKeyUsage
}

func mustECDSAKey() *ecdsa.PrivateKey {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return key
}

func mustRSAKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
}

func (p *testPKI) mustECDSACA(commonName string) (*ecdsa.PrivateKey, *x509.Certificate) {
	key := mustECDSAKey()
	notBefore, notAfter := certValidity()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return key, cert
}

func (p *testPKI) mustRSACA(commonName string, key *rsa.PrivateKey) *x509.Certificate {
	notBefore, notAfter := certValidity()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(9),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return cert
}

func (p *testPKI) mustLeaf(issuer *x509.Certificate, issuerKey, public any, commonName string, serial int64) *x509.Certificate {
	notBefore, notAfter := certValidity()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{commonName},
	}
	return p.mustIssue(tmpl, issuer, issuerKey, public)
}

func (p *testPKI) mustResponder(issuer *x509.Certificate, issuerKey, public any, commonName string, serial int64, opts responderOpts) *x509.Certificate {
	notBefore, notAfter := certValidity()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           opts.eku,
		BasicConstraintsValid: true,
		IsCA:                  opts.isCA,
	}
	return p.mustIssue(tmpl, issuer, issuerKey, public)
}

func (p *testPKI) mustIssue(tmpl, issuer *x509.Certificate, issuerKey, public any) *x509.Certificate {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, issuer, public, issuerKey)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return cert
}
