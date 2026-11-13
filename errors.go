package netsec

import "errors"

// Security-relevant errors returned by the sender-key state machine.
// Callers should treat every failure as terminal for the offending message:
// no chain state, skipped-key cache, or epoch bookkeeping is advanced.
var (
	// ErrGroupID is returned when a message or distribution targets another group.
	ErrGroupID = errors.New("netsec: group id mismatch")

	// ErrEpoch is returned when a distribution does not target the current epoch.
	ErrEpoch = errors.New("netsec: distribution epoch is not current")

	// ErrOldEpoch is returned for any data message whose epoch is not the
	// current epoch. Old-epoch messages are rejected permanently.
	ErrOldEpoch = errors.New("netsec: message from a revoked epoch")

	// ErrSignature is returned when the administrator Ed25519 signature over a
	// distribution cannot be verified.
	ErrSignature = errors.New("netsec: invalid administrator signature")

	// ErrDigestMismatch is returned when the signed member-set digest of a
	// distribution does not match the digest of the current member set.
	ErrDigestMismatch = errors.New("netsec: signed member-set digest mismatch")

	// ErrNotMember is returned when a sender device is not part of the signed
	// member set of the relevant epoch.
	ErrNotMember = errors.New("netsec: sender device is not a group member")

	// ErrChainReused is returned when a chainID has already been used in any
	// epoch of the group.
	ErrChainReused = errors.New("netsec: chain id reused within group")

	// ErrChainNotFound is returned when the sender has no installed chain in
	// the current epoch, for example after member removal.
	ErrChainNotFound = errors.New("netsec: no sender chain installed")

	// ErrChainMismatch is returned when the header chainID does not match the
	// chain currently installed for the sender device.
	ErrChainMismatch = errors.New("netsec: message chain id does not match installed chain")

	// ErrDuplicate is returned when a message index has already been consumed.
	ErrDuplicate = errors.New("netsec: message index already processed")

	// ErrSkippedLimit is returned when filling the skipped-message-key cache
	// would exceed MaxSkippedKeys entries.
	ErrSkippedLimit = errors.New("netsec: skipped message key cache limit exceeded")

	// ErrIndexOverflow is returned when an index equals math.MaxUint64 and
	// cannot be derived or advanced.
	ErrIndexOverflow = errors.New("netsec: chain index overflow")

	// ErrAuth is returned when AEAD authentication of a data message fails.
	ErrAuth = errors.New("netsec: message authentication failed")

	// ErrInvalidChainKey is returned when a stored chain key is not 32 bytes.
	ErrInvalidChainKey = errors.New("netsec: chain key must be 32 bytes")
)
