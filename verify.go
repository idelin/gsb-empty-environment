package netsec

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
)

// TLSConfig returns a tls.Config for dialing an alternative endpoint of an
// origin. The peer certificate is always verified against the origin host
// name, never against the alternative host name from the advertisement:
// an Alt-Svc entry only authorizes an endpoint to serve the origin if it
// can present a certificate valid for the origin itself.
func TLSConfig(origin Origin, roots *x509.CertPool) *tls.Config {
	return &tls.Config{
		ServerName: origin.Host,
		RootCAs:    roots,
		MinVersion: tls.VersionTLS12,
	}
}

// VerifyPeerCertificates verifies that peerCerts form a chain, anchored in
// roots, whose leaf is valid for originHost. originHost must be the host of
// the original origin, not the alternative endpoint's host name.
func VerifyPeerCertificates(originHost string, peerCerts []*x509.Certificate, roots *x509.CertPool) error {
	if len(peerCerts) == 0 {
		return errors.New("netsec: alternative endpoint presented no certificates")
	}
	intermediates := x509.NewCertPool()
	for _, cert := range peerCerts[1:] {
		intermediates.AddCert(cert)
	}
	_, err := peerCerts[0].Verify(x509.VerifyOptions{
		DNSName:       originHost,
		Roots:         roots,
		Intermediates: intermediates,
	})
	return err
}
