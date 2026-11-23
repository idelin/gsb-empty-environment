package destruct

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
)

// flipTxn performs one forward-only transaction state transition with a CAS.
func (c *Coordinator) flipTxn(ctx context.Context, old, next *DestroyTxn, want TxnState) error {
	if txnOrder(next.State) <= txnOrder(old.State) || next.State != want {
		return fmt.Errorf("destruct: illegal txn transition %s -> %s", old.State, next.State)
	}
	if err := c.cfg.Store.CompareSwapTxn(ctx, old, next); err != nil {
		return err
	}
	return nil
}

func txnOrder(s TxnState) int {
	switch s {
	case TxnSealing:
		return 1
	case TxnErasing:
		return 2
	case TxnDestroyed:
		return 3
	default:
		return 0
	}
}

// forceLeaseState drives one lease to a terminal state with bounded CAS
// retries. It is idempotent: a lease already in the target state succeeds.
func (c *Coordinator) forceLeaseState(ctx context.Context, lease *LeaseRecord, target LeaseState) error {
	for attempt := 0; attempt < 10; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := c.cfg.Store.GetLease(ctx, lease.ID)
		if err != nil {
			return err
		}
		if current.State == target {
			return nil
		}
		next := *current
		next.State = target
		if err := c.cfg.Store.CompareSwapLease(ctx, current, &next); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrConflict
}

// validateConfirmation checks a confirmation against the immutable txn
// snapshot: ownership tuple, replica existence, location, generation and the
// Ed25519 signature over the full identity tuple.
func (c *Coordinator) validateConfirmation(txn *DestroyTxn, conf Confirmation) error {
	if conf.TxnID != txn.ID || conf.VolumeID != txn.Volume {
		return fmt.Errorf("%w: txn=%s volume=%s", ErrTxnMismatch, conf.TxnID, conf.VolumeID)
	}
	spec, ok := txn.Replicas[conf.ReplicaID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownReplica, conf.ReplicaID)
	}
	if conf.Location != spec.Location {
		return fmt.Errorf("%w: replica %s location %s, expected %s", ErrLocationMismatch, spec.ID, conf.Location, spec.Location)
	}
	if conf.Generation != spec.Generation {
		return fmt.Errorf("%w: replica %s generation %d, expected %d", ErrGenerationMismatch, spec.ID, conf.Generation, spec.Generation)
	}
	if !VerifyConfirmation(ed25519.PublicKey(spec.PublicKey), conf.VolumeID, conf.TxnID, conf.ReplicaID, conf.Location, conf.Generation, conf.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

// eraseReplicas collects one valid confirmation per snapshot replica.
// Already-stored confirmations are re-validated (so foreign or stale writes
// are never trusted); missing ones trigger an idempotent Erase call. Any
// validation failure, unknown replica or missing confirmation is returned as
// an incomplete reason and the txn stays open.
func (c *Coordinator) eraseReplicas(ctx context.Context, txn *DestroyTxn) ([]string, error) {
	var reasons []string
	stored, err := c.cfg.Store.ListConfirmations(ctx, txn.ID)
	if err != nil {
		return nil, err
	}
	byReplica := make(map[ReplicaID]Confirmation, len(stored))
	for _, record := range stored {
		conf := record.Confirmation
		spec, known := txn.Replicas[conf.ReplicaID]
		if !known {
			reasons = append(reasons, fmt.Sprintf("unknown replica %s in confirmation", conf.ReplicaID))
			continue
		}
		if err := c.validateConfirmation(txn, conf); err != nil {
			reasons = append(reasons, fmt.Sprintf("replica %s: %v", spec.ID, err))
			continue
		}
		byReplica[conf.ReplicaID] = conf
	}

	for _, id := range sortedReplicaIDs(txn.Replicas) {
		if _, ok := byReplica[id]; ok {
			continue
		}
		spec := txn.Replicas[id]
		eraser, err := c.cfg.Erasers(ctx, spec)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("replica %s: eraser unavailable: %v", id, err))
			continue
		}
		conf, err := eraser.Erase(ctx, EraseRequest{VolumeID: txn.Volume, TxnID: txn.ID, Replica: spec})
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("replica %s: erase failed: %v", id, err))
			continue
		}
		if err := c.validateConfirmation(txn, *conf); err != nil {
			reasons = append(reasons, fmt.Sprintf("replica %s: %v", id, err))
			continue
		}
		record := &ConfirmationRecord{Confirmation: *conf, ReceivedAt: c.now()}
		if err := c.cfg.Store.PutConfirmation(ctx, record); err != nil {
			return nil, err
		}
		byReplica[id] = *conf
	}

	for _, id := range sortedReplicaIDs(txn.Replicas) {
		if _, ok := byReplica[id]; !ok {
			reasons = append(reasons, fmt.Sprintf("replica %s: no erasure confirmation", id))
		}
	}
	if len(reasons) > 0 {
		return reasons, nil
	}

	// All snapshot replicas have valid confirmations: flip txn and volume.
	nextTxn := cloneTxn(txn)
	nextTxn.State = TxnDestroyed
	if err := c.flipTxn(ctx, txn, nextTxn, TxnDestroyed); err != nil {
		if !errors.Is(err, ErrConflict) {
			return nil, err
		}
		return nil, nil
	}
	if err := c.markVolumeDestroyed(ctx, nextTxn); err != nil {
		return nil, err
	}
	return nil, nil
}

// markVolumeDestroyed flips the volume Destroying -> Destroyed. It tolerates
// a process that crashed between the txn flip and this call: the txn already
// says destroyed, and the volume CAS is retried against the persisted record.
func (c *Coordinator) markVolumeDestroyed(ctx context.Context, txn *DestroyTxn) error {
	for attempt := 0; attempt < 10; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		vol, err := c.cfg.Store.GetVolume(ctx, txn.Volume)
		if err != nil {
			return err
		}
		if vol.State == VolumeDestroyed {
			return nil
		}
		next := cloneVolume(vol)
		next.State = VolumeDestroyed
		next.DestroyTxnID = txn.ID
		if err := c.cfg.Store.CompareSwapVolume(ctx, vol, next); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrConflict
}
