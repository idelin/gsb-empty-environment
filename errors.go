package netsec

import "errors"

// Sentinel errors returned by the credential, STUN, and peer validators.
// Callers should use errors.Is to distinguish the failure classes.
var (
	// ErrConfig marks an unusable Validator configuration.
	ErrConfig = errors.New("netsec: invalid configuration")

	// ErrUsernameFormat marks a malformed expiry:tenant:user username.
	ErrUsernameFormat = errors.New("netsec: malformed TURN username")

	// ErrUsernameExpired marks a correctly formatted credential whose expiry
	// has passed.
	ErrUsernameExpired = errors.New("netsec: TURN credential expired")

	// ErrTenantNotAllowed marks a tenant rejected by the configured policy.
	ErrTenantNotAllowed = errors.New("netsec: tenant is not allowed")

	// ErrRealmMismatch marks a STUN REALM attribute that differs from the
	// realm configured on the Validator.
	ErrRealmMismatch = errors.New("netsec: realm does not match")

	// ErrSTUNMessage marks a structurally invalid STUN message.
	ErrSTUNMessage = errors.New("netsec: invalid STUN message")

	// ErrMissingUsername marks a STUN message without a USERNAME attribute.
	ErrMissingUsername = errors.New("netsec: STUN message has no USERNAME")

	// ErrMissingIntegrity marks a STUN message without MESSAGE-INTEGRITY.
	ErrMissingIntegrity = errors.New("netsec: STUN message has no MESSAGE-INTEGRITY")

	// ErrIntegrityMismatch marks a MESSAGE-INTEGRITY value that did not
	// authenticate with the derived key.
	ErrIntegrityMismatch = errors.New("netsec: MESSAGE-INTEGRITY check failed")

	// ErrIntegrityOrder marks unprotected attributes after MESSAGE-INTEGRITY.
	// Only FINGERPRINT is permitted there.
	ErrIntegrityOrder = errors.New("netsec: unprotected attributes follow MESSAGE-INTEGRITY")

	// ErrForbiddenPeer marks a peer address that is loopback, private, or
	// otherwise unsafe to relay to.
	ErrForbiddenPeer = errors.New("netsec: peer address is not permitted")

	// ErrSTUNAttribute marks an invalid or ambiguous STUN attribute.
	ErrSTUNAttribute = errors.New("netsec: invalid STUN attribute")
)
