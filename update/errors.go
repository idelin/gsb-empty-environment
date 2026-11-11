package update

import "errors"

// Protocol and storage errors returned by the update package.
var (
	// ErrNonCanonical is returned when metadata is not encoded in the
	// deterministic canonical form required by this package.
	ErrNonCanonical = errors.New("update: metadata is not canonically encoded")

	// ErrTrailingData is returned when an envelope carries bytes after
	// its top-level JSON value.
	ErrTrailingData = errors.New("update: trailing data after metadata envelope")

	// ErrMissingMetadata is returned when a required metadata role is
	// absent from an update bundle or from the trusted store.
	ErrMissingMetadata = errors.New("update: missing metadata")

	// ErrMissingRole is returned when a root does not define a role
	// needed to verify an envelope.
	ErrMissingRole = errors.New("update: role not defined in root")

	// ErrTypeMismatch is returned when the inner "_type" of an envelope
	// does not match the role it is presented as.
	ErrTypeMismatch = errors.New("update: metadata type mismatch")

	// ErrThresholdNotMet is returned when an envelope does not carry
	// enough valid signatures from distinct, role-authorized key aliases.
	ErrThresholdNotMet = errors.New("update: signature threshold not met")

	// ErrRollback is returned when metadata version is not strictly
	// greater than the trusted version, which also covers replay attacks.
	ErrRollback = errors.New("update: metadata version rollback or replay")

	// ErrExpired is returned when metadata has already expired.
	ErrExpired = errors.New("update: metadata is expired")

	// ErrDigestMismatch is returned when a snapshot/target digest does
	// not match the bytes referenced by its parent metadata.
	ErrDigestMismatch = errors.New("update: metadata digest mismatch")

	// ErrLengthMismatch is returned when serialized metadata or a
	// target file does not have the advertised length.
	ErrLengthMismatch = errors.New("update: length mismatch")

	// ErrRootRotation is returned when a proposed root cannot replace
	// the currently trusted root.
	ErrRootRotation = errors.New("update: root rotation rejected")

	// ErrNoState is returned when the store has not been bootstrapped
	// with a trusted root.
	ErrNoState = errors.New("update: store is not bootstrapped")

	// ErrAlreadyBootstrapped is returned when Bootstrap is called on a
	// store that already holds a trusted root.
	ErrAlreadyBootstrapped = errors.New("update: store already bootstrapped")

	// ErrSimulatedPowerLoss is returned by the interruptible store when
	// an injected crash point fires, simulating sudden power loss.
	ErrSimulatedPowerLoss = errors.New("update: simulated power loss")
)
