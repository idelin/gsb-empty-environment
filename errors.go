package netsec

import "errors"

var (
	// Configuration and generation errors.
	ErrInvalidConfig     = errors.New("netsec: invalid configuration")
	ErrInvalidGeneration = errors.New("netsec: invalid generation")
	ErrStaleGeneration   = errors.New("netsec: stale generation")

	// Certificate and identity errors.
	ErrNoCertificate       = errors.New("netsec: peer did not present a certificate")
	ErrInvalidCertificate  = errors.New("netsec: invalid certificate")
	ErrInvalidChain        = errors.New("netsec: certificate chain verification failed")
	ErrCertificateLifetime = errors.New("netsec: certificate lifetime outside policy")
	ErrPrincipalUse        = errors.New("netsec: certificate is not valid for the requested principal use")
	ErrNoTrustBundle       = errors.New("netsec: no trust bundle for trust domain")
	ErrForeignTrustDomain  = errors.New("netsec: trust domain is not accepted")

	// SPIFFE and SAN errors.
	ErrInvalidSPIFFEID     = errors.New("netsec: invalid SPIFFE identity")
	ErrInvalidSAN          = errors.New("netsec: invalid subject alternative name")
	ErrMissingSPIFFEID     = errors.New("netsec: certificate has no SPIFFE URI SAN")
	ErrMultipleIdentities  = errors.New("netsec: certificate contains multiple workload identities")
	ErrSANTypeSubstitution = errors.New("netsec: SAN types are not interchangeable")

	// Wildcard errors.
	ErrWildcardNotAllowed = errors.New("netsec: wildcard not allowed")
	ErrMultiLevelWildcard = errors.New("netsec: multi-level wildcard not allowed")
	ErrWildcardPrincipal  = errors.New("netsec: wildcard cannot be used as a principal")

	// Revocation and rotation errors.
	ErrRevoked          = errors.New("netsec: certificate is revoked")
	ErrNoRotationRecord = errors.New("netsec: no rotation record for workload")
	ErrRotated          = errors.New("netsec: certificate is not active in the current rotation generation")
	ErrRotationExpired  = errors.New("netsec: rotation record expired")

	// Attestation and authorization errors.
	ErrAttestationRequired = errors.New("netsec: node attestation is required")
	ErrAttestation         = errors.New("netsec: node attestation verification failed")
	ErrPolicyDenied        = errors.New("netsec: connection policy denied")
	ErrNoPolicy            = errors.New("netsec: connection policy not found")
	ErrActionDenied        = errors.New("netsec: action not permitted")
)
