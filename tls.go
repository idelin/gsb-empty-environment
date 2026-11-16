package netsec

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
)

// VerifyCertificateForOrigin checks the certificate hostname against the
// original origin hostname. It deliberately does not accept the alternative
// endpoint's hostname as a substitute. Certificate-chain validation remains the
// caller's responsibility; TLSConfigForOrigin and DialTLSContext perform both
// chain and hostname verification.
func VerifyCertificateForOrigin(origin string, cert *x509.Certificate) error {
	o, err := ParseOrigin(origin)
	if err != nil {
		return err
	}
	if cert == nil {
		return errors.New("altsvc: no certificate")
	}
	if err := cert.VerifyHostname(o.Host); err != nil {
		return fmt.Errorf("altsvc: certificate is not valid for origin host %q: %w", o.Host, err)
	}
	return nil
}

// VerifyPeerCertificatesForOrigin parses the leaf certificate from rawCerts and
// verifies its hostname against origin. It does not build or validate a
// certificate chain.
func VerifyPeerCertificatesForOrigin(origin string, rawCerts [][]byte) error {
	if len(rawCerts) == 0 {
		return errors.New("altsvc: no peer certificate")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("altsvc: parse peer certificate: %w", err)
	}
	return VerifyCertificateForOrigin(origin, cert)
}

// VerifyPeerCertificateForOrigin returns a tls.Config VerifyPeerCertificate
// callback that checks the leaf certificate hostname against the original
// origin. Prefer TLSConfigForOrigin, which also performs normal chain
// verification.
func VerifyPeerCertificateForOrigin(origin string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		return VerifyPeerCertificatesForOrigin(origin, rawCerts)
	}
}

// TLSConfigForOrigin returns a clone of base configured to verify the TLS peer
// as the original origin. In particular, ServerName is the origin hostname, not
// the hostname of the alternative endpoint.
func TLSConfigForOrigin(origin string, base *tls.Config) (*tls.Config, error) {
	o, err := ParseOrigin(origin)
	if err != nil {
		return nil, err
	}

	var config *tls.Config
	if base == nil {
		config = &tls.Config{}
	} else {
		config = base.Clone()
	}
	config.ServerName = o.Host
	config.InsecureSkipVerify = false
	return config, nil
}
