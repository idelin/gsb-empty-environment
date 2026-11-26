package sshcert

import "errors"

// Verification and parsing errors. Callers should use errors.Is rather than
// matching on message text.
var (
	// ErrCAKeyMismatch means the certificate's embedded signature key is not
	// the CA key the host elected to trust.
	ErrCAKeyMismatch = errors.New("sshcert: certificate signature key does not match trusted CA")
	// ErrSignatureInvalid means the Ed25519 signature did not verify.
	ErrSignatureInvalid = errors.New("sshcert: certificate signature verification failed")

	// ErrUnknownCriticalOption means the certificate carries a critical
	// option the host does not understand. Critical options must never be
	// silently ignored.
	ErrUnknownCriticalOption = errors.New("sshcert: unknown critical option")

	// ErrCertNotYetValid means now < valid_after.
	ErrCertNotYetValid = errors.New("sshcert: certificate not yet valid")
	// ErrCertExpired means now >= valid_before.
	ErrCertExpired = errors.New("sshcert: certificate has expired")

	// ErrPrincipalNotAllowed means the requested principal is not listed in
	// valid_principals, or the list is empty without explicit wildcard
	// permission.
	ErrPrincipalNotAllowed = errors.New("sshcert: principal not permitted by certificate")
	// ErrPrincipalRequired means the host did not supply a principal while
	// the certificate does not wildcard principals.
	ErrPrincipalRequired = errors.New("sshcert: principal required but not provided")

	// ErrSourceAddressDenied means the connection source IP is not covered
	// by any entry of the source-address critical option.
	ErrSourceAddressDenied = errors.New("sshcert: source address not permitted")
	// ErrSourceAddressRequired means the certificate pins source addresses
	// but the host supplied no connection IP.
	ErrSourceAddressRequired = errors.New("sshcert: source-address option present but connection IP unknown")
	// ErrBadSourceAddress means a source-address entry is not a valid CIDR
	// or IP literal.
	ErrBadSourceAddress = errors.New("sshcert: malformed source-address entry")
	// ErrBadForceCommand means the force-command option is not encoded as a
	// single SSH string.
	ErrBadForceCommand = errors.New("sshcert: malformed force-command option")

	// ErrCertTypeMismatch means a user certificate was presented to a host
	// check or vice versa.
	ErrCertTypeMismatch = errors.New("sshcert: certificate type does not match service")

	// ErrRevoked means the KRL explicitly revokes the presented credential.
	ErrRevoked = errors.New("sshcert: certificate revoked by KRL")
)

// KRL structural errors.
var (
	ErrKRLMagic          = errors.New("sshcert: bad KRL magic")
	ErrKRLVersion        = errors.New("sshcert: unsupported KRL version")
	ErrKRLSectionOrder   = errors.New("sshcert: KRL sections out of order or duplicated")
	ErrKRLUnknownSection = errors.New("sshcert: unknown KRL section type")
	ErrKRLBadCAKey       = errors.New("sshcert: malformed CA key in KRL")
	ErrKRLBadSerialList  = errors.New("sshcert: malformed or unsorted serial list")
	ErrKRLBadSerialRange = errors.New("sshcert: malformed or overlapping serial range")
	ErrKRLBadBitmap      = errors.New("sshcert: malformed or unsorted serial bitmap")
	ErrKRLBadKeyID       = errors.New("sshcert: malformed key-id revocation")
	ErrKRLBadExplicitKey = errors.New("sshcert: malformed explicit-key revocation")
	ErrKRLBadFingerprint = errors.New("sshcert: malformed fingerprint revocation")
)
