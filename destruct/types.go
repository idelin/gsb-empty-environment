package destruct

import (
	"context"
	"errors"
	"strings"
	"time"
)

// VolumeID is the immutable identity of a volume. It is never reused, even when
// a new volume takes over an old volume's name.
type VolumeID string

// TxnID identifies a single destruction transaction.
type TxnID string

// LeaseID identifies a key-usage lease.
type LeaseID string

// ReplicaID identifies a persistent key replica location.
type ReplicaID string

// Location is the persistent location (path / URI) of a key replica.
type Location string

// VolumeState is the lifecycle state of a volume record.
type VolumeState string

const (
	// VolumeUsable means the volume may grant key leases.
	VolumeUsable VolumeState = "usable"
	// VolumeDestroying means a destruction transaction is open and the volume
	// can never return to VolumeUsable.
	VolumeDestroying VolumeState = "destroying"
	// VolumeDestroyed means every key replica has a valid erasure confirmation.
	VolumeDestroyed VolumeState = "destroyed"
)

// LeaseState is the lifecycle state of a lease record.
type LeaseState string

const (
	LeaseActive  LeaseState = "active"
	LeaseRevoked LeaseState = "revoked"
	LeaseExpired LeaseState = "expired"
)

// TxnState is the phase of a destruction transaction. Transitions are
// forward-only.
type TxnState string

const (
	TxnSealing   TxnState = "sealing"
	TxnErasing   TxnState = "erasing"
	TxnDestroyed TxnState = "destroyed"
)

// ReplicaSpec describes one key replica as captured in a volume record and in
// the immutable snapshot of a destruction transaction.
type ReplicaSpec struct {
	ID         ReplicaID
	Location   Location
	Generation uint64
	PublicKey  []byte
}

// VolumeRecord is the durable volume descriptor.
type VolumeRecord struct {
	ID           VolumeID
	Name         string
	Replicas     map[ReplicaID]ReplicaSpec
	State        VolumeState
	DestroyTxnID TxnID
	Version      int64
}

// LeaseRecord is a durable key-usage lease.
type LeaseRecord struct {
	ID        LeaseID
	Volume    VolumeID
	Replica   ReplicaID
	State     LeaseState
	ExpiresAt time.Time
	Version   int64
}

// DestroyTxn is the durable destruction transaction. Replicas is an immutable
// snapshot taken when the transaction is created.
type DestroyTxn struct {
	ID        TxnID
	Volume    VolumeID
	Replicas  map[ReplicaID]ReplicaSpec
	State     TxnState
	CreatedAt time.Time
	Version   int64
}

// Confirmation is a signed statement that a replica erased the key generation
// named below. The signature covers VolumeID, TxnID, ReplicaID, Location and
// Generation, so a confirmation cannot be replayed against another volume,
// transaction or generation.
type Confirmation struct {
	VolumeID   VolumeID
	TxnID      TxnID
	ReplicaID  ReplicaID
	Location   Location
	Generation uint64
	IssuedAt   time.Time
	Signature  []byte
}

// ConfirmationRecord wraps a confirmation with bookkeeping metadata.
type ConfirmationRecord struct {
	Confirmation Confirmation
	ReceivedAt   time.Time
}

// EraseRequest is handed to a replica's Eraser.
type EraseRequest struct {
	VolumeID VolumeID
	TxnID    TxnID
	Replica  ReplicaSpec
}

// Eraser is implemented by the key-replica backend. Erase must be idempotent:
// repeating a call with the same TxnID must return the same confirmation
// instead of failing or producing a different one.
type Eraser interface {
	Erase(ctx context.Context, req EraseRequest) (*Confirmation, error)
}

// Sentinel errors. Callers should use errors.Is.
var (
	ErrNotFound              = errors.New("destruct: record not found")
	ErrConflict              = errors.New("destruct: optimistic concurrency conflict")
	ErrVolumeSealed          = errors.New("destruct: volume is sealed for destruction")
	ErrVolumeDestroyed       = errors.New("destruct: volume already destroyed")
	ErrLeaseNotActive        = errors.New("destruct: lease is not active")
	ErrUnknownReplica        = errors.New("destruct: unknown key replica")
	ErrGenerationMismatch    = errors.New("destruct: replica generation mismatch")
	ErrLocationMismatch      = errors.New("destruct: replica location mismatch")
	ErrInvalidSignature      = errors.New("destruct: invalid erasure confirmation signature")
	ErrTxnMismatch           = errors.New("destruct: confirmation does not belong to this destruction transaction")
	ErrTxnClosed             = errors.New("destruct: destruction transaction is closed")
	ErrDestructionIncomplete = errors.New("destruct: destruction incomplete")
	ErrInjectedCrash         = errors.New("destruct: injected process crash")
	ErrInjectedFault         = errors.New("destruct: injected storage fault")
)

// IncompleteError explains why a transaction could not be marked destroyed.
// The transaction stays open and can be resumed.
type IncompleteError struct {
	Reasons []string
}

func (e *IncompleteError) Error() string {
	return ErrDestructionIncomplete.Error() + ": " + strings.Join(e.Reasons, "; ")
}

// Unwrap allows errors.Is(err, ErrDestructionIncomplete).
func (e *IncompleteError) Unwrap() error { return ErrDestructionIncomplete }

// DestroyResult reports the outcome of a Destroy or Recover call.
type DestroyResult struct {
	TxnID    TxnID
	State    TxnState
	Complete bool
	Reasons  []string
}
