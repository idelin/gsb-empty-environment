package netsec

import "errors"

// Errors returned when an elliptic-curve key does not use an approved,
// explicitly named curve.
var (
	// ErrExplicitECParameters indicates that an EC key was encoded with
	// explicit curve parameters. Such keys are always rejected, even when the
	// parameters describe a well-known curve.
	ErrExplicitECParameters = errors.New("netsec: explicit EC parameters are not allowed")

	// ErrMissingECParameters indicates that an ECDSA key omitted its curve
	// parameters. A named-curve OID is required.
	ErrMissingECParameters = errors.New("netsec: EC key is missing named curve parameters")

	// ErrInvalidECParameters indicates that the algorithm parameters are not a
	// well-formed named curve OBJECT IDENTIFIER.
	ErrInvalidECParameters = errors.New("netsec: invalid EC parameters")

	// ErrUnsupportedECCurve indicates that the parameters name a curve that is
	// not in the supported named-curve set.
	ErrUnsupportedECCurve = errors.New("netsec: unsupported named curve")

	// ErrInvalidECPublicKey indicates that the public point is not a valid
	// point on the named curve.
	ErrInvalidECPublicKey = errors.New("netsec: invalid EC public key")

	// ErrMalformedCertificate indicates that the certificate could not be
	// parsed far enough to inspect its subject public key information.
	ErrMalformedCertificate = errors.New("netsec: malformed certificate")
)
