package destruct

import "context"

// Store is the durable persistence interface used by the coordinator.
// Implementations must provide linearizable per-key CAS semantics. The
// in-memory implementation in this package and the FaultStore wrapper are
// suitable both for production embedding (with a real backend behind the same
// interface) and for fault injection tests.
type Store interface {
	CreateVolume(ctx context.Context, v *VolumeRecord) error
	GetVolume(ctx context.Context, id VolumeID) (*VolumeRecord, error)
	CompareSwapVolume(ctx context.Context, old, next *VolumeRecord) error

	CreateLease(ctx context.Context, l *LeaseRecord) error
	GetLease(ctx context.Context, id LeaseID) (*LeaseRecord, error)
	ListLeases(ctx context.Context, vol VolumeID) ([]*LeaseRecord, error)
	CompareSwapLease(ctx context.Context, old, next *LeaseRecord) error

	CreateTxn(ctx context.Context, t *DestroyTxn) error
	GetTxn(ctx context.Context, id TxnID) (*DestroyTxn, error)
	CompareSwapTxn(ctx context.Context, old, next *DestroyTxn) error
	ListPendingTxns(ctx context.Context) ([]*DestroyTxn, error)

	PutConfirmation(ctx context.Context, r *ConfirmationRecord) error
	GetConfirmation(ctx context.Context, txn TxnID, replica ReplicaID) (*ConfirmationRecord, error)
	ListConfirmations(ctx context.Context, txn TxnID) ([]*ConfirmationRecord, error)
}
