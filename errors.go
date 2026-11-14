package netsec

import "errors"

var (
	// ErrInvalidConfig indicates that a Store was configured without a root,
	// workspace, account, or key provider.
	ErrInvalidConfig = errors.New("netsec: invalid configuration")

	// ErrUnauthenticated indicates that the caller was not accepted by the
	// configured CallerVerifier.
	ErrUnauthenticated = errors.New("netsec: caller is not authenticated")

	// ErrWorkspaceMismatch indicates that the caller or an artifact does not
	// belong to the workspace that owns the store.
	ErrWorkspaceMismatch = errors.New("netsec: workspace mismatch")

	// ErrAccountMismatch indicates that the caller or an artifact does not
	// belong to the account that owns the store.
	ErrAccountMismatch = errors.New("netsec: account mismatch")

	// ErrNotFound indicates that the requested state artifact does not exist.
	ErrNotFound = errors.New("netsec: state artifact not found")

	// ErrIntegrity indicates that an artifact failed authentication, was
	// truncated, or was mixed with another artifact.
	ErrIntegrity = errors.New("netsec: state integrity check failed")

	// ErrVersionMismatch indicates that the version record and state payload
	// do not describe the same revision.
	ErrVersionMismatch = errors.New("netsec: state version mismatch")

	// ErrExportDenied indicates that plaintext export was not explicitly
	// authorized.
	ErrExportDenied = errors.New("netsec: plaintext export denied")

	// ErrLocked indicates that another caller currently holds the state lock.
	ErrLocked = errors.New("netsec: state is locked")

	// ErrLockOwnership indicates that a release was attempted with a lock
	// handle that does not own the current lock.
	ErrLockOwnership = errors.New("netsec: lock is owned by another caller")

	// ErrKeyGeneration indicates that no key is available for an envelope's
	// key generation.
	ErrKeyGeneration = errors.New("netsec: key generation is unavailable")

	// ErrRotationUnsupported indicates that the configured key provider does
	// not implement RotatingKeyProvider.
	ErrRotationUnsupported = errors.New("netsec: key rotation is not supported")
)
