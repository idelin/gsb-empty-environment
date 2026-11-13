package netsec

import "errors"

var (
	// ErrEmptyChain is returned when no certificates are supplied.
	ErrEmptyChain = errors.New("netsec: empty certificate chain")

	// ErrInvalidCertificate is returned when a certificate cannot be parsed.
	ErrInvalidCertificate = errors.New("netsec: invalid certificate")

	// ErrDSAParametersMissing is returned when a DSA public key has no
	// parameters and there is no issuing certificate from which to inherit
	// them.
	ErrDSAParametersMissing = errors.New("netsec: DSA parameters are missing and cannot be inherited")

	// ErrDSAParameterMismatch is returned when DSA parameters carried by a
	// child certificate differ from the parameters of the issuing certificate.
	// DSA parameters may only be omitted (to inherit) or be identical.
	ErrDSAParameterMismatch = errors.New("netsec: DSA parameters differ from the issuing certificate")

	// ErrInvalidDSAParameters is returned for malformed Dss-Parms values.
	ErrInvalidDSAParameters = errors.New("netsec: invalid DSA parameters")

	// ErrUnsupportedAlgorithm is returned for signature or key algorithms that
	// this package cannot verify.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported algorithm")

	// ErrInvalidSignature is returned when a certificate signature does not
	// verify under the issuing certificate's public key and effective DSA
	// parameters.
	ErrInvalidSignature = errors.New("netsec: invalid certificate signature")
)
