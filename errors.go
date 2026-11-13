package netsec

import "errors"

var (
	// ErrInvalidCertificate indicates that the certificate encoding is
	// malformed or incomplete.
	ErrInvalidCertificate = errors.New("netsec: invalid SSH certificate")
	// ErrInvalidPublicKey indicates that a public key could not be parsed.
	ErrInvalidPublicKey = errors.New("netsec: invalid SSH public key")
	// ErrInvalidSignature indicates malformed signature data.
	ErrInvalidSignature = errors.New("netsec: invalid signature")
	// ErrUnsupportedAlgorithm indicates an algorithm this package cannot
	// verify.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported algorithm")
	// ErrSignatureMismatch indicates a cryptographically invalid signature.
	ErrSignatureMismatch = errors.New("netsec: signature mismatch")
	// ErrCertificateFieldsModified indicates that exported certificate
	// fields no longer match the bytes that were signed.
	ErrCertificateFieldsModified = errors.New("netsec: certificate fields differ from signed data")
)
