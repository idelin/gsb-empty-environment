package netsec

import "errors"

var (
	// ErrTrustRootsRequired is returned when a registry is created without a
	// trust anchor pool. The registry never falls back to the host trust store.
	ErrTrustRootsRequired = errors.New("netsec: trust roots are required")

	// ErrInvalidTenant is returned when a tenant identifier is empty or
	// malformed.
	ErrInvalidTenant = errors.New("netsec: invalid tenant identifier")

	// ErrCertificateRequired is returned when a nil certificate is supplied.
	ErrCertificateRequired = errors.New("netsec: certificate is required")

	// ErrInvalidCertificate is returned for a certificate that cannot be
	// re-parsed from its DER encoding or that is unsuitable as a client
	// identity.
	ErrInvalidCertificate = errors.New("netsec: invalid certificate")

	// ErrUnregisteredCertificate means the leaf certificate fingerprint is not
	// present in the active mapping generation.
	ErrUnregisteredCertificate = errors.New("netsec: certificate is not registered")

	// ErrFingerprintConflict means one certificate fingerprint is being bound
	// to two different tenants or registration policies.
	ErrFingerprintConflict = errors.New("netsec: certificate fingerprint conflict")

	// ErrRegistrationConflict is returned when an attempt is made to replace a
	// registration with incompatible metadata.
	ErrRegistrationConflict = errors.New("netsec: registration conflict")

	// ErrUntrustedIssuer means the leaf could not be verified to a configured
	// trust anchor. Matching the Issuer name alone is never sufficient.
	ErrUntrustedIssuer = errors.New("netsec: certificate issuer is not trusted")

	// ErrCertificateExpired means the certificate or one of its issuers was
	// outside its validity period at the verification time.
	ErrCertificateExpired = errors.New("netsec: certificate is expired or not yet valid")

	// ErrCertificateVerification wraps other path validation failures.
	ErrCertificateVerification = errors.New("netsec: certificate verification failed")

	// ErrPolicyMissing means a required certificate policy OID was absent.
	ErrPolicyMissing = errors.New("netsec: required certificate policy is missing")

	// ErrPolicyNotAllowed means none of the certificate's policy OIDs is in the
	// configured allow-list.
	ErrPolicyNotAllowed = errors.New("netsec: certificate policy is not allowed")

	// ErrRevocationUnavailable means no revocation checker was configured. The
	// registry fails closed instead of assuming a certificate is not revoked.
	ErrRevocationUnavailable = errors.New("netsec: revocation checker is not configured")

	// ErrRevocationCheckFailed wraps a revocation checker failure. A failed
	// check is never treated as a successful one.
	ErrRevocationCheckFailed = errors.New("netsec: revocation check failed")

	// ErrRevoked means the certificate is known to be revoked.
	ErrRevoked = errors.New("netsec: certificate is revoked")

	// ErrNoUsableIdentity means the certificate has no registered unique
	// identifier, SAN, or CN that can be used for a mapping.
	ErrNoUsableIdentity = errors.New("netsec: certificate has no usable subject identity")

	// ErrUnsupportedSAN means a subjectAltName extension is present but none
	// of its entries can be represented as an identity. Falling back to CN in
	// that situation would be unsafe.
	ErrUnsupportedSAN = errors.New("netsec: certificate contains no supported SAN identity")

	// ErrCNNotUnique means a CN fallback was requested but the CN is registered
	// to more than one tenant.
	ErrCNNotUnique = errors.New("netsec: common name is not uniquely registered")

	// ErrAmbiguousIdentity means two strong identities (unique identifiers or
	// SANs) resolve to different tenants.
	ErrAmbiguousIdentity = errors.New("netsec: certificate identity is ambiguous")
)
