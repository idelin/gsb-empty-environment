package netsec

import "errors"

var (
	// ErrNilSnapshot is returned when an operation needs a device-list
	// snapshot but was given nil.
	ErrNilSnapshot = errors.New("netsec: nil device-list snapshot")

	// ErrInvalidAccountID is returned for an empty account identifier.
	ErrInvalidAccountID = errors.New("netsec: invalid account ID")

	// ErrDuplicateAccount is returned when the same account occurs twice in
	// a two-party operation.
	ErrDuplicateAccount = errors.New("netsec: duplicate account")

	// ErrAccountSetMismatch is returned when a QR payload does not describe
	// exactly the two accounts being verified.
	ErrAccountSetMismatch = errors.New("netsec: account set mismatch")

	// ErrInvalidDeviceID is returned for an empty device identifier.
	ErrInvalidDeviceID = errors.New("netsec: invalid device ID")

	// ErrDuplicateDeviceID is returned when two devices in one snapshot share
	// a device ID.
	ErrDuplicateDeviceID = errors.New("netsec: duplicate device ID")

	// ErrEmptyPublicKey is returned when a device has no identity public key.
	ErrEmptyPublicKey = errors.New("netsec: empty device identity public key")

	// ErrSnapshotDigestMismatch is returned when the digest fields of an
	// imported snapshot do not match its canonical contents.
	ErrSnapshotDigestMismatch = errors.New("netsec: snapshot digest mismatch")

	// ErrListVersionMismatch is returned when a payload and a current device
	// list have different list versions.
	ErrListVersionMismatch = errors.New("netsec: device-list version mismatch")

	// ErrListDigestMismatch is returned when a payload and a current device
	// list have different list digests.
	ErrListDigestMismatch = errors.New("netsec: device-list digest mismatch")

	// ErrIdentityDigestMismatch is returned when a payload and a current
	// device list have different account identity digests.
	ErrIdentityDigestMismatch = errors.New("netsec: account identity digest mismatch")

	// ErrChecksumMismatch is returned when a QR payload checksum does not
	// match the checksum recomputed from its bindings.
	ErrChecksumMismatch = errors.New("netsec: safety-number checksum mismatch")

	// ErrUnknownAccount is returned when no snapshot exists for an account.
	ErrUnknownAccount = errors.New("netsec: unknown account")

	// ErrNoConfirmation is returned when a confirmation is requested or
	// inspected but none exists.
	ErrNoConfirmation = errors.New("netsec: no user confirmation")

	// ErrViewsMismatch is returned when two clients do not hold the same
	// complete device-list view.
	ErrViewsMismatch = errors.New("netsec: client device-list views mismatch")

	// ErrInvalidQRPayload is returned for malformed or internally
	// inconsistent QR payloads.
	ErrInvalidQRPayload = errors.New("netsec: invalid QR payload")

	// ErrUnsupportedProtocolVersion is returned when a QR payload uses a
	// protocol version this package does not implement.
	ErrUnsupportedProtocolVersion = errors.New("netsec: unsupported protocol version")

	// ErrDeviceNotFound is returned when updating presence for an unknown
	// device.
	ErrDeviceNotFound = errors.New("netsec: device not found")
)
