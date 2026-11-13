package netsec

import "crypto/x509"

// Certificate is an alias for crypto/x509.Certificate.
type Certificate = x509.Certificate

// CertPool is an alias for crypto/x509.CertPool.
type CertPool = x509.CertPool

// VerifyOptions is an alias for crypto/x509.VerifyOptions. It can be passed
// directly to ValidateCertificatePath or VerifyCertificatePath.
type VerifyOptions = x509.VerifyOptions

// Options is an alias for PathOptions.
type Options = PathOptions

// PathValidator is an alias for Validator.
type PathValidator = Validator

// CertificatePathValidator is an alias for Validator.
type CertificatePathValidator = Validator

// NewCertPool returns a new empty certificate pool.
func NewCertPool() *x509.CertPool {
	return x509.NewCertPool()
}

// NewPathValidator is an alias for NewValidator.
func NewPathValidator(roots ...*x509.Certificate) *Validator {
	return NewValidator(roots...)
}

// NewCertificatePathValidator is an alias for NewValidator.
func NewCertificatePathValidator(roots ...*x509.Certificate) *Validator {
	return NewValidator(roots...)
}

// ParseCertificateChain parses one or more DER certificates.
func ParseCertificateChain(der []byte) ([]*x509.Certificate, error) {
	return ParseCertificates(der)
}

// ParseChain is an alias for ParseCertificates.
func ParseChain(der []byte) ([]*x509.Certificate, error) {
	return ParseCertificates(der)
}

// ParsePEMCertificateChain parses every CERTIFICATE block.
func ParsePEMCertificateChain(data []byte) ([]*x509.Certificate, error) {
	return ParsePEMCertificates(data)
}
