package netsec

import (
	"crypto/x509"
	"errors"
	"fmt"
)

// allowedSignatureAlgorithms lists every signature algorithm whose hash is
// considered strong enough for certificates in a verified path.
var allowedSignatureAlgorithms = map[x509.SignatureAlgorithm]bool{
	x509.SHA256WithRSA:    true,
	x509.SHA384WithRSA:    true,
	x509.SHA512WithRSA:    true,
	x509.SHA256WithRSAPSS: true,
	x509.SHA384WithRSAPSS: true,
	x509.SHA512WithRSAPSS: true,
	x509.ECDSAWithSHA256:  true,
	x509.ECDSAWithSHA384:  true,
	x509.ECDSAWithSHA512:  true,
	x509.PureEd25519:      true,
}

// ErrEmptyPath is returned when a path without any certificates is checked.
var ErrEmptyPath = errors.New("netsec: certificate path is empty")

// WeakSignatureError describes a certificate in the path whose signature
// uses a hash algorithm that is not on the allowlist.
type WeakSignatureError struct {
	// Index is the certificate's position in the path (0 = leaf).
	Index int
	// Subject is the certificate's subject, for diagnostics.
	Subject string
	// Algorithm is the disallowed signature algorithm.
	Algorithm x509.SignatureAlgorithm
}

func (e *WeakSignatureError) Error() string {
	return fmt.Sprintf("netsec: certificate %d (%s) uses disallowed signature algorithm %s",
		e.Index, e.Subject, e.Algorithm)
}

// AllowedSignatureAlgorithm reports whether alg's hash is on the allowlist.
// SHA-1, MD5 and MD2 based algorithms are not allowed.
func AllowedSignatureAlgorithm(alg x509.SignatureAlgorithm) bool {
	return allowedSignatureAlgorithms[alg]
}

// VerifyPathSignatures checks the signature hash of every certificate in
// path. The path is ordered leaf-first, matching x509.Certificate.Verify
// chains. It returns nil only if every signature uses an allowed hash; a
// single SHA-1 or MD5 signature anywhere in the path fails the whole path.
func VerifyPathSignatures(path []*x509.Certificate) error {
	if len(path) == 0 {
		return ErrEmptyPath
	}
	for i, cert := range path {
		if cert == nil {
			return fmt.Errorf("netsec: certificate %d is nil", i)
		}
		if !AllowedSignatureAlgorithm(cert.SignatureAlgorithm) {
			return &WeakSignatureError{
				Index:     i,
				Subject:   cert.Subject.String(),
				Algorithm: cert.SignatureAlgorithm,
			}
		}
	}
	return nil
}
