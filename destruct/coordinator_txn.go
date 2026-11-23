package destruct

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// beginDestruction seals the volume and returns its authoritative transaction.
// It is a compare-and-swap loop: every conflict is resolved by re-reading
// state, so concurrent Destroy calls converge on one TxnID.
func (c *Coordinator) beginDestruction(ctx context.Context, vol VolumeID) (*DestroyTxn, error) {
	for attempt := 0; attempt < 10; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, err := c.cfg.Store.GetVolume(ctx, vol)
		if err != nil {
			return nil, err
		}
		switch record.State {
		case VolumeDestroyed:
			return nil, ErrVolumeDestroyed
		case VolumeDestroying:
			if record.DestroyTxnID == "" {
				return nil, fmt.Errorf("destruct: destroying volume %s without transaction id", vol)
			}
			return c.cfg.Store.GetTxn(ctx, record.DestroyTxnID)
		case VolumeUsable:
		default:
			return nil, fmt.Errorf("destruct: volume %s in unknown state %q", vol, record.State)
		}

		txn := &DestroyTxn{
			ID:        TxnID(c.newID()),
			Volume:    record.ID,
			Replicas:  cloneReplicaMap(record.Replicas),
			State:     TxnSealing,
			CreatedAt: c.now(),
		}
		// The transaction record is durable before the volume is sealed, so a
		// crash here cannot leave a sealed volume without a resumable txn.
		if err := c.cfg.Store.CreateTxn(ctx, txn); err != nil {
			return nil, err
		}
		next := cloneVolume(record)
		next.State = VolumeDestroying
		next.DestroyTxnID = txn.ID
		if err := c.cfg.Store.CompareSwapVolume(ctx, record, next); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return nil, err
		}
		return c.cfg.Store.GetTxn(ctx, txn.ID)
	}
	return nil, ErrConflict
}

// runTxn drives one transaction forward through its forward-only state
// machine. Every conflict is resolved by adopting the persisted state rather
// than fighting another coordinator.
func (c *Coordinator) runTxn(ctx context.Context, txn *DestroyTxn) (*DestroyResult, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch txn.State {
		case TxnSealing:
			if err := c.sealVolume(ctx, txn); err != nil {
				if !errors.Is(err, ErrConflict) {
					return nil, err
				}
				adopted, adoptErr := c.adoptAfterConflict(ctx, txn)
				if adoptErr != nil {
					return nil, adoptErr
				}
				txn = adopted
				continue
			}
			reloaded, err := c.cfg.Store.GetTxn(ctx, txn.ID)
			if err != nil {
				return nil, err
			}
			txn = reloaded

		case TxnErasing:
			reasons, err := c.eraseReplicas(ctx, txn)
			if err != nil {
				return nil, err
			}
			if len(reasons) > 0 {
				return &DestroyResult{TxnID: txn.ID, State: TxnErasing, Complete: false, Reasons: reasons}, nil
			}
			reloaded, err := c.cfg.Store.GetTxn(ctx, txn.ID)
			if err != nil {
				return nil, err
			}
			txn = reloaded

		case TxnDestroyed:
			// Align the volume record even if the process died between the
			// txn flip and the volume flip.
			if err := c.markVolumeDestroyed(ctx, txn); err != nil {
				return nil, err
			}
			return &DestroyResult{TxnID: txn.ID, State: TxnDestroyed, Complete: true}, nil

		default:
			return nil, fmt.Errorf("destruct: txn %s in unknown state %q", txn.ID, txn.State)
		}
	}
}

// adoptAfterConflict returns the transaction the volume now points at, or a
// fresh copy of our own transaction when we still own it.
func (c *Coordinator) adoptAfterConflict(ctx context.Context, txn *DestroyTxn) (*DestroyTxn, error) {
	vol, err := c.cfg.Store.GetVolume(ctx, txn.Volume)
	if err != nil {
		return nil, err
	}
	if vol.DestroyTxnID != "" && vol.DestroyTxnID != txn.ID {
		return c.cfg.Store.GetTxn(ctx, vol.DestroyTxnID)
	}
	return c.cfg.Store.GetTxn(ctx, txn.ID)
}

// sealVolume bans new leases (the volume is already VolumeDestroying at this
// point), drains every in-flight lease and flips the txn sealing -> erasing.
func (c *Coordinator) sealVolume(ctx context.Context, txn *DestroyTxn) error {
	if err := c.revokeAllLeases(ctx, txn.Volume); err != nil {
		return err
	}
	leases, err := c.cfg.Store.ListLeases(ctx, txn.Volume)
	if err != nil {
		return err
	}
	for _, lease := range leases {
		if lease.State == LeaseActive {
			return fmt.Errorf("%w: lease %s survived sealing", ErrDestructionIncomplete, lease.ID)
		}
	}
	next := cloneTxn(txn)
	next.State = TxnErasing
	return c.flipTxn(ctx, txn, next, TxnErasing)
}

// revokeAllLeases waits for active leases to expire (bounded by
// LeaseGracePeriod) and force-revokes every survivor. No usable state can be
// restored: the volume is already sealed, so GrantLease/RenewLease refuse to
// create replacements while this runs.
func (c *Coordinator) revokeAllLeases(ctx context.Context, vol VolumeID) error {
	deadline := c.now().Add(c.cfg.LeaseGracePeriod)
	for {
		leases, err := c.cfg.Store.ListLeases(ctx, vol)
		if err != nil {
			return err
		}
		now := c.now()
		var active []*LeaseRecord
		var firstExpiry time.Time
		for _, lease := range leases {
			if lease.State != LeaseActive {
				continue
			}
			if !lease.ExpiresAt.After(now) {
				if err := c.forceLeaseState(ctx, lease, LeaseExpired); err != nil {
					return err
				}
				continue
			}
			active = append(active, lease)
			if firstExpiry.IsZero() || lease.ExpiresAt.Before(firstExpiry) {
				firstExpiry = lease.ExpiresAt
			}
		}
		if len(active) == 0 {
			return nil
		}
		// Wait only if the earliest natural expiry lands inside the grace
		// window; otherwise force-revoke so destruction stays bounded.
		if firstExpiry.After(deadline) {
			for _, lease := range active {
				if err := c.forceLeaseState(ctx, lease, LeaseRevoked); err != nil {
					return err
				}
			}
			continue
		}
		if err := c.sleep(ctx, firstExpiry.Sub(now)); err != nil {
			return err
		}
	}
}
