package netsec

import "errors"

var (
	// ErrRegionNotFound is returned for an unknown region ID.
	ErrRegionNotFound = errors.New("region not found")
	// ErrRegionExists is returned when creating a duplicate region.
	ErrRegionExists = errors.New("region already exists")
	// ErrInvalidRegion indicates invalid region metadata.
	ErrInvalidRegion = errors.New("invalid region")
	// ErrInvalidCredential indicates a missing or malformed credential at
	// provisioning time.  It is distinct from a failed authentication.
	ErrInvalidCredential = errors.New("invalid region credential")
	// ErrInvalidState indicates an unknown persisted state.
	ErrInvalidState = errors.New("invalid region state")
	// ErrNotAllocatable is returned when allocating an in-use or erasing region.
	ErrNotAllocatable = errors.New("region is not allocatable")
	// ErrNotInUse is returned when releasing a region that is not leased.
	ErrNotInUse = errors.New("region is not in use")
	// ErrNotErasing is returned when an erase is requested for a region that
	// is not in the erasing state.
	ErrNotErasing = errors.New("region is not erasing")
	// ErrAuthenticationFailed means the supplied credential was wrong.
	ErrAuthenticationFailed = errors.New("region credential rejected")
	// ErrRegionLocked means consecutive authentication failures reached the
	// configured limit.  The lock is volatile and lasts until process restart.
	ErrRegionLocked = errors.New("region authentication locked until restart")
	// ErrVerificationFailed means a stripe did not read back as the expected
	// fixed pattern.  The region remains in the erasing state.
	ErrVerificationFailed = errors.New("erased stripe verification failed")
	// ErrIncompleteErase means the final clean gate found a stripe without a
	// verified checkpoint.  The region must not become clean.
	ErrIncompleteErase = errors.New("erase incomplete")
	// ErrCorruptJournal means the erase log has an invalid interior record or
	// an impossible state transition.
	ErrCorruptJournal = errors.New("erase journal is corrupt")
	// ErrRegionSizeMismatch means the backing device region does not match the
	// size recorded in the journal.
	ErrRegionSizeMismatch = errors.New("region size mismatch")
	// ErrClosed means the manager has been closed.
	ErrClosed = errors.New("region manager is closed")
	// ErrLeaseMismatch means a lease does not match the region's current
	// tenant allocation (including a stale re-allocation).
	ErrLeaseMismatch = errors.New("lease does not match region tenant")
	// ErrPowerLoss is the sentinel used by fault hooks to simulate a power cut.
	// Recovery deliberately stops at such a cut and leaves the region erasing.
	ErrPowerLoss = errors.New("simulated power loss")
)
