package netsec

import "errors"

// Errors returned by SyncScope. Callers should use errors.Is rather than
// comparing error strings.
var (
	// ErrInvalidAccountID is returned when an operation is attempted without an
	// account identifier.
	ErrInvalidAccountID = errors.New("sync scope: account id is required")

	// ErrInvalidDeviceID is returned when an operation is attempted without a
	// device identifier.
	ErrInvalidDeviceID = errors.New("sync scope: device id is required")

	// ErrInvalidContactID is returned when a contact has no identifier.
	ErrInvalidContactID = errors.New("sync scope: contact id is required")

	// ErrContactNotFound is returned when deleting a contact that is not visible
	// in the account.
	ErrContactNotFound = errors.New("sync scope: contact not found")

	// ErrDeviceNotRegistered is returned when a device has not been registered.
	ErrDeviceNotRegistered = errors.New("sync scope: device is not registered")

	// ErrDeviceNotApproved is returned until an operator approves the device.
	ErrDeviceNotApproved = errors.New("sync scope: device is not approved")

	// ErrDeviceRevoked is returned after a device has been revoked.
	ErrDeviceRevoked = errors.New("sync scope: device is revoked")

	// ErrDeviceScopeMismatch is returned when a device identifier is presented
	// for an account that does not own that device registration.
	ErrDeviceScopeMismatch = errors.New("sync scope: device does not belong to account")

	// ErrTokenInvalid is returned for malformed, unknown, or revoked tokens.
	ErrTokenInvalid = errors.New("sync scope: token is invalid")

	// ErrTokenUsed is returned when a token is replayed after a successful sync.
	ErrTokenUsed = errors.New("sync scope: token has already been used")

	// ErrTokenScopeMismatch is returned when a token is presented together with
	// an account or device other than the ones it was issued for.
	ErrTokenScopeMismatch = errors.New("sync scope: token does not match account and device")

	// ErrCursorStale is returned when the cursor does not exactly match the
	// cursor bound to the token. This rejects both old and invented cursors.
	ErrCursorStale = errors.New("sync scope: cursor is stale or invalid")

	// ErrTokenGeneration is returned if the operating system cannot supply
	// sufficient entropy for a new token.
	ErrTokenGeneration = errors.New("sync scope: cannot generate token")
)

// Aliases used by some callers.
var (
	ErrTokenReused = ErrTokenUsed
	ErrStaleCursor = ErrCursorStale
)
