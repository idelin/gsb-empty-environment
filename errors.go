package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidConfiguration reports invalid resolver options.
	ErrInvalidConfiguration = errors.New("netsec: invalid resolver configuration")

	// ErrAESUnavailable reports that no host AES callback was configured.
	ErrAESUnavailable = errors.New("netsec: AES callback is unavailable")

	// ErrInvalidAddress reports a malformed private address.
	ErrInvalidAddress = errors.New("netsec: invalid private address")

	// ErrInvalidPRand reports that the top two bits of prand are not 01.
	ErrInvalidPRand = errors.New("netsec: prand is not resolvable")

	// ErrInvalidRecord reports an invalid device record.
	ErrInvalidRecord = errors.New("netsec: invalid device record")

	// ErrDeviceExists reports an attempt to add a duplicate device ID.
	ErrDeviceExists = errors.New("netsec: device already exists")

	// ErrDeviceNotFound reports an unknown device ID.
	ErrDeviceNotFound = errors.New("netsec: device not found")

	// ErrDeviceRevoked reports that the resolved device is no longer active.
	ErrDeviceRevoked = errors.New("netsec: device is revoked")

	// ErrSameIRK reports a rotation to the current IRK.
	ErrSameIRK = errors.New("netsec: new IRK equals the current IRK")

	// ErrTableVersionOverflow reports that the table version cannot advance.
	ErrTableVersionOverflow = errors.New("netsec: table version overflow")

	// ErrRecordGenerationOverflow reports that a record generation cannot advance.
	ErrRecordGenerationOverflow = errors.New("netsec: record generation overflow")

	// ErrTicketInvalid reports a zero, modified, or foreign resolution ticket.
	ErrTicketInvalid = errors.New("netsec: invalid resolution ticket")

	// ErrStaleTable reports that the table changed after resolution.
	ErrStaleTable = errors.New("netsec: resolution table version is stale")

	// ErrStaleGeneration reports that the device record generation changed.
	ErrStaleGeneration = errors.New("netsec: device record generation is stale")

	// ErrStaleCredential reports that the ticket's matched credential is no longer present.
	ErrStaleCredential = errors.New("netsec: matched IRK credential is stale")

	// ErrLegacyIRKExpired reports that an old-IRK discovery grace period ended.
	ErrLegacyIRKExpired = errors.New("netsec: legacy IRK discovery grace has expired")

	// ErrAddressConflict reports that one address matched multiple active records.
	ErrAddressConflict = errors.New("netsec: private address matched multiple devices")

	// ErrInvalidChallenge reports a malformed link-layer identity challenge.
	ErrInvalidChallenge = errors.New("netsec: invalid identity challenge")

	// ErrIdentityPublicKey reports that the device has no long-term public key.
	ErrIdentityPublicKey = errors.New("netsec: device identity public key is unavailable")

	// ErrSignatureVerifier reports that no host signature verifier is configured.
	ErrSignatureVerifier = errors.New("netsec: identity signature verifier is unavailable")

	// ErrIdentityProof reports that the long-term key signature did not verify.
	ErrIdentityProof = errors.New("netsec: identity proof verification failed")
)

// ConflictError is returned when a private address resolves to more than one
// active device in the same table snapshot. DeviceIDs is sorted and stable.
type ConflictError struct {
	Address   Address
	DeviceIDs []DeviceID
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%v: address %s matched %v", ErrAddressConflict, e.Address, e.DeviceIDs)
}

func (e *ConflictError) Unwrap() error {
	return ErrAddressConflict
}
