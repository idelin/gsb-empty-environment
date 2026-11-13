package netsec

import "errors"

var (
	// ErrClosed is returned when a closed volume is used.
	ErrClosed = errors.New("netsec: volume is closed")

	// ErrInvalidSector is returned for a logical or physical sector that is
	// outside the volume's address space.
	ErrInvalidSector = errors.New("netsec: invalid sector")

	// ErrInvalidSize is returned when a caller does not supply exactly one
	// logical sector.
	ErrInvalidSize = errors.New("netsec: invalid logical sector size")

	// ErrSectorNotFound is returned when no committed mapping exists.
	ErrSectorNotFound = errors.New("netsec: logical sector not found")

	// ErrWriteEpochExhausted is returned rather than wrapping the 64-bit
	// write epoch.
	ErrWriteEpochExhausted = errors.New("netsec: write epoch exhausted")

	// ErrWriteEpochRollback is returned when durable or caller-supplied state
	// is older than the epoch already observed by this volume.
	ErrWriteEpochRollback = errors.New("netsec: write epoch rollback detected")

	// ErrKeyGenerationExhausted is returned rather than wrapping the key
	// generation.
	ErrKeyGenerationExhausted = errors.New("netsec: key generation exhausted")

	// ErrKeyGenerationRollback is returned when a key generation would move
	// backwards.
	ErrKeyGenerationRollback = errors.New("netsec: key generation rollback detected")

	// ErrRootRollback is returned when the durable mapping root is older than
	// the root already observed by this volume.
	ErrRootRollback = errors.New("netsec: mapping root rollback detected")

	// ErrRootRevisionExhausted is returned rather than wrapping the internal
	// monotonic root revision.
	ErrRootRevisionExhausted = errors.New("netsec: root revision exhausted")

	// ErrNoSpace is returned when no unreferenced physical record is available.
	ErrNoSpace = errors.New("netsec: no free physical sector")

	// ErrAuthentication is returned for a copied, swapped, torn, or otherwise
	// unauthentic ciphertext record or mapping node.
	ErrAuthentication = errors.New("netsec: authentication failed")

	// ErrPhysicalGeneration is returned when a mapped physical generation does
	// not match the record header.
	ErrPhysicalGeneration = errors.New("netsec: physical generation mismatch")

	// ErrCorrupt describes malformed persisted metadata.
	ErrCorrupt = errors.New("netsec: corrupt volume metadata")

	// ErrNotInitialized is returned when no formatted volume state is present.
	ErrNotInitialized = errors.New("netsec: volume is not initialized")

	// ErrKeyRequired is returned when formatting a volume without a key.
	ErrKeyRequired = errors.New("netsec: master key is required")

	// ErrReadbackMismatch is returned when a write cannot be read back exactly.
	ErrReadbackMismatch = errors.New("netsec: write readback mismatch")

	// ErrTornWrite is returned when a state-log write is only partially
	// durable.
	ErrTornWrite = errors.New("netsec: torn metadata write")

	// ErrStateUncertain indicates that a metadata write may have reached the
	// device but could not be verified. The volume refuses further I/O until
	// Reload succeeds.
	ErrStateUncertain = errors.New("netsec: metadata state is uncertain")

	// ErrInjected is the default error used by the memory device's fault
	// injector.
	ErrInjected = errors.New("netsec: injected device fault")
)
