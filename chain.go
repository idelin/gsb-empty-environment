package netsec

import (
	"crypto/x509"
	"fmt"
)

// verifyChain builds a path from cert to a trusted root. Self-signed
// certificates are only accepted when they themselves are in roots.
func verifyChain(cert *x509.Certificate, roots *x509.CertPool) error {
	opts := x509.VerifyOptions{
		Roots: roots,
		// KeyUsage is left at the default; CMS signatures use
		// digitalSignature/nonRepudiation which x509 accepts for EKU-less
		// certificates.
	}
	if _, err := cert.Verify(opts); err != nil {
		return fmt.Errorf("%w: %v", ErrSignature, err)
	}
	return nil
}
