package netsec

import "errors"

var (
	// ErrInvalidArgument is returned for malformed identifiers or key
	// material supplied to an operation.
	ErrInvalidArgument = errors.New("netsec: invalid argument")

	// ErrAccountNotFound indicates that the requested account does not exist.
	ErrAccountNotFound = errors.New("netsec: account not found")

	// ErrAccountAlreadyExists indicates that an account identifier is already
	// registered.
	ErrAccountAlreadyExists = errors.New("netsec: account already exists")

	// ErrDeviceNotFound indicates that the requested device is unknown.
	ErrDeviceNotFound = errors.New("netsec: device not found")

	// ErrDeviceAlreadyExists indicates that a device identifier is already
	// registered anywhere in this service.
	ErrDeviceAlreadyExists = errors.New("netsec: device already exists")

	// ErrDeviceNotApproved indicates that a device is still pending, was
	// rejected, or has otherwise never been approved.
	ErrDeviceNotApproved = errors.New("netsec: device not approved")

	// ErrDeviceRevoked indicates that a device has been revoked.
	ErrDeviceRevoked = errors.New("netsec: device revoked")

	// ErrCrossAccount indicates an attempt to use a device or wrapping
	// context against another account.
	ErrCrossAccount = errors.New("netsec: cross-account operation denied")

	// ErrEnrollmentNotFound indicates that an enrollment request is unknown.
	ErrEnrollmentNotFound = errors.New("netsec: enrollment not found")

	// ErrEnrollmentNotPending indicates that an enrollment was already
	// approved or rejected.
	ErrEnrollmentNotPending = errors.New("netsec: enrollment is not pending")

	// ErrApprovalRequired indicates that an operation needs an already
	// approved device from the same account.
	ErrApprovalRequired = errors.New("netsec: approval by an existing device required")

	// ErrRecoveryCodeInvalid indicates that a recovery code is unknown for
	// the supplied account.
	ErrRecoveryCodeInvalid = errors.New("netsec: recovery code invalid")

	// ErrRecoveryCodeUsed indicates that a recovery code was already consumed.
	ErrRecoveryCodeUsed = errors.New("netsec: recovery code already used")

	// ErrScopeDenied indicates that a requested entry is not in the device's
	// item scope.
	ErrScopeDenied = errors.New("netsec: entry outside device scope")

	// ErrEntryNotFound indicates that an entry does not exist in the account.
	ErrEntryNotFound = errors.New("netsec: entry not found")

	// ErrInvalidWrappedKey indicates that an account-key envelope is malformed
	// or was not produced by this package's wrapping format.
	ErrInvalidWrappedKey = errors.New("netsec: invalid wrapped account key")

	// ErrAccountKeyDecrypt indicates that an account-key envelope could not
	// be authenticated or decrypted with the supplied device key and binding.
	ErrAccountKeyDecrypt = errors.New("netsec: account key unwrap failed")
)
