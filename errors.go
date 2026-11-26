package spiffefed

import "errors"

// Sentinel errors are wrapped with context by the package. Callers should use
// errors.Is when classifying a failure.
var (
	ErrInvalidSPIFFEID       = errors.New("spiffefed: invalid SPIFFE ID")
	ErrInvalidTrustDomain    = errors.New("spiffefed: invalid trust domain")
	ErrInvalidTrustBundle    = errors.New("spiffefed: invalid trust bundle")
	ErrBundleNotFound        = errors.New("spiffefed: trust bundle not found")
	ErrUnknownService        = errors.New("spiffefed: target service is not configured")
	ErrTargetServiceRequired = errors.New("spiffefed: target service is required for federated identities")
	ErrFederationDenied      = errors.New("spiffefed: trust domain is not federated for target service")
	ErrInvalidCertificate    = errors.New("spiffefed: invalid X.509-SVID")
	ErrCertificateNotTrusted = errors.New("spiffefed: X.509-SVID is not trusted by its trust domain bundle")
	ErrInvalidJWKS           = errors.New("spiffefed: invalid JWKS")
	ErrInvalidJWT            = errors.New("spiffefed: invalid JWT-SVID")
	ErrUnknownKey            = errors.New("spiffefed: JWT-SVID key is not present in the subject trust domain")
	ErrSignature             = errors.New("spiffefed: JWT-SVID signature verification failed")
	ErrAudience              = errors.New("spiffefed: JWT-SVID audience mismatch")
	ErrTokenLifetime         = errors.New("spiffefed: JWT-SVID lifetime is invalid or too long")
	ErrInvalidSelector       = errors.New("spiffefed: invalid selector")
	ErrAttestationDenied     = errors.New("spiffefed: workload attestation denied")
	ErrAmbiguousRegistration = errors.New("spiffefed: workload attestation matched multiple registrations")
)
