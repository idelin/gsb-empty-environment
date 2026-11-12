package netsec

import "errors"

// Sentinel errors surfaced by the destruction coordinator. Callers should use
// errors.Is to inspect them rather than matching message text.
var (
	// ErrNotFound indicates that no object exists at the requested key.
	ErrNotFound = errors.New("netsec: object not found")
	// ErrVolumeNotFound indicates that no volume is registered under the
	// requested name or identifier.
	ErrVolumeNotFound = errors.New("netsec: volume not found")
	// ErrLeasesProhibited indicates that a volume has entered destruction and
	// no further leases may be granted.
	ErrLeasesProhibited = errors.New("netsec: leases are prohibited for destroying volume")
	// ErrLeaseNotFound indicates that a lease was already released or never
	// existed.
	ErrLeaseNotFound = errors.New("netsec: lease not found")
	// ErrReplicaNotFound indicates that a confirmation does not reference a
	// known replica of the target volume generation.
	ErrReplicaNotFound = errors.New("netsec: replica not found")
	// ErrUnknownReplica indicates that a replica outside the volume manifest
	// was observed during destruction. The transaction must remain incomplete
	// until that replica is erased.
	ErrUnknownReplica = errors.New("netsec: unknown replica observed")
	// ErrVolumeDestroyed indicates that the volume generation is destroyed or
	// mid-destruction and can never return to a usable state.
	ErrVolumeDestroyed = errors.New("netsec: volume is destroyed")
	// ErrNameInUse indicates that a volume name is still bound to a live
	// generation and cannot be reused yet.
	ErrNameInUse = errors.New("netsec: volume name is in use")
	// ErrDestructionUnknown indicates that no destruction transaction exists
	// for the requested volume.
	ErrDestructionUnknown = errors.New("netsec: no destruction transaction")
	// ErrAlreadyErased indicates that an erasure confirmation was already
	// recorded for the replica.
	ErrAlreadyErased = errors.New("netsec: replica already erased")
	// ErrInvalidArgument indicates that a caller supplied an invalid
	// parameter.
	ErrInvalidArgument = errors.New("netsec: invalid argument")
)
