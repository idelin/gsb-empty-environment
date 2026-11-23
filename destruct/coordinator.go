package destruct

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
)

// EraserProvider resolves the Eraser for a key replica. The coordinator only
// ever invokes it with replicas taken from a destruction transaction snapshot.
type EraserProvider func(ctx context.Context, spec ReplicaSpec) (Eraser, error)

// Config configures a Coordinator.
type Config struct {
	// Store is the durable backend.
	Store Store
	// Erasers resolves a replica's Eraser.
	Erasers EraserProvider
	// LeaseGracePeriod bounds how long sealing waits for in-flight leases
	// before force-revoking the survivors.
	LeaseGracePeriod time.Duration
	// Now is the clock. Defaults to time.Now.
	Now func() time.Time
}

// Coordinator is the destruction coordinator called by the Go volume host.
type Coordinator struct {
	cfg Config
}

// New builds a coordinator and validates the configuration.
func New(cfg Config) (*Coordinator, error) {
	if cfg.Store == nil {
		return nil, errors.New("destruct: missing Store")
	}
	if cfg.Erasers == nil {
		return nil, errors.New("destruct: missing EraserProvider")
	}
	if cfg.LeaseGracePeriod <= 0 {
		cfg.LeaseGracePeriod = 250 * time.Millisecond
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Coordinator{cfg: cfg}, nil
}

func (c *Coordinator) now() time.Time { return c.cfg.Now() }

func (c *Coordinator) sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Coordinator) newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("destruct: cannot generate id: %w", err))
	}
	return hex.EncodeToString(b[:])
}

func sortedReplicaIDs(m map[ReplicaID]ReplicaSpec) []ReplicaID {
	out := make([]ReplicaID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// CreateVolume registers a usable volume with its key replicas.
func (c *Coordinator) CreateVolume(ctx context.Context, id VolumeID, name string, replicas []ReplicaSpec) (*VolumeRecord, error) {
	if id == "" {
		return nil, errors.New("destruct: empty volume id")
	}
	if len(replicas) == 0 {
		return nil, errors.New("destruct: volume must have at least one key replica")
	}
	specs := make(map[ReplicaID]ReplicaSpec, len(replicas))
	for _, spec := range replicas {
		if spec.ID == "" {
			return nil, errors.New("destruct: replica with empty id")
		}
		if _, dup := specs[spec.ID]; dup {
			return nil, fmt.Errorf("destruct: duplicate replica id %s", spec.ID)
		}
		specs[spec.ID] = cloneReplicaSpec(spec)
	}
	vol := &VolumeRecord{
		ID:       id,
		Name:     name,
		Replicas: specs,
		State:    VolumeUsable,
	}
	if err := c.cfg.Store.CreateVolume(ctx, vol); err != nil {
		return nil, err
	}
	return c.cfg.Store.GetVolume(ctx, id)
}

// GrantLease issues an active key lease. It fails once the volume is sealed or
// destroyed. A lease racing with sealing is revoked before the call returns,
// and the sealing loop independently reaps every other in-flight lease.
func (c *Coordinator) GrantLease(ctx context.Context, vol VolumeID, replica ReplicaID, ttl time.Duration) (*LeaseRecord, error) {
	if ttl <= 0 {
		return nil, errors.New("destruct: lease ttl must be positive")
	}
	for attempt := 0; attempt < 10; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, err := c.cfg.Store.GetVolume(ctx, vol)
		if err != nil {
			return nil, err
		}
		if record.State == VolumeDestroyed {
			return nil, ErrVolumeDestroyed
		}
		if record.State != VolumeUsable {
			return nil, ErrVolumeSealed
		}
		if _, ok := record.Replicas[replica]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownReplica, replica)
		}
		lease := &LeaseRecord{
			ID:        LeaseID(c.newID()),
			Volume:    vol,
			Replica:   replica,
			State:     LeaseActive,
			ExpiresAt: c.now().Add(ttl),
		}
		if err := c.cfg.Store.CreateLease(ctx, lease); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return nil, err
		}
		// Re-check after the durable insert: sealing may have won the race.
		fresh, err := c.cfg.Store.GetVolume(ctx, vol)
		if err != nil {
			return nil, err
		}
		if fresh.State != VolumeUsable {
			if err := c.forceLeaseState(ctx, lease, LeaseRevoked); err != nil {
				return nil, err
			}
			if fresh.State == VolumeDestroyed {
				return nil, ErrVolumeDestroyed
			}
			return nil, ErrVolumeSealed
		}
		return lease, nil
	}
	return nil, ErrConflict
}

// RenewLease extends an active lease. Renewal is refused on a sealed volume
// and the lease is revoked on the spot so sealing cannot be held off forever.
func (c *Coordinator) RenewLease(ctx context.Context, id LeaseID, ttl time.Duration) (*LeaseRecord, error) {
	if ttl <= 0 {
		return nil, errors.New("destruct: lease ttl must be positive")
	}
	for attempt := 0; attempt < 10; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lease, err := c.cfg.Store.GetLease(ctx, id)
		if err != nil {
			return nil, err
		}
		if lease.State != LeaseActive {
			return nil, ErrLeaseNotActive
		}
		vol, err := c.cfg.Store.GetVolume(ctx, lease.Volume)
		if err != nil {
			return nil, err
		}
		next := *lease
		if vol.State != VolumeUsable {
			next.State = LeaseRevoked
			if err := c.cfg.Store.CompareSwapLease(ctx, lease, &next); err != nil && !errors.Is(err, ErrConflict) {
				return nil, err
			}
			if vol.State == VolumeDestroyed {
				return nil, ErrVolumeDestroyed
			}
			return nil, ErrVolumeSealed
		}
		next.ExpiresAt = c.now().Add(ttl)
		if err := c.cfg.Store.CompareSwapLease(ctx, lease, &next); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return nil, err
		}
		return &next, nil
	}
	return nil, ErrConflict
}

// RevokeLease force-revokes a lease on behalf of the host. Revoking an
// already-inactive lease is a no-op.
func (c *Coordinator) RevokeLease(ctx context.Context, id LeaseID) error {
	lease, err := c.cfg.Store.GetLease(ctx, id)
	if err != nil {
		return err
	}
	return c.forceLeaseState(ctx, lease, LeaseRevoked)
}

// Destroy starts (or adopts) the destruction transaction for vol and drives it
// to completion. Repeated calls are idempotent: an open transaction is resumed
// rather than recreated, and a destroyed volume returns ErrVolumeDestroyed.
func (c *Coordinator) Destroy(ctx context.Context, vol VolumeID) (*DestroyResult, error) {
	txn, err := c.beginDestruction(ctx, vol)
	if err != nil {
		return nil, err
	}
	return c.runTxn(ctx, txn)
}

// Recover resumes every open destruction transaction after a process restart.
// Orphan snapshots whose transaction is no longer referenced by the volume
// are skipped; the authoritative transaction appears in the pending list and
// is resumed.
func (c *Coordinator) Recover(ctx context.Context) ([]*DestroyResult, error) {
	pending, err := c.cfg.Store.ListPendingTxns(ctx)
	if err != nil {
		return nil, err
	}
	var results []*DestroyResult
	for _, txn := range pending {
		vol, err := c.cfg.Store.GetVolume(ctx, txn.Volume)
		if err != nil {
			return results, err
		}
		if vol.DestroyTxnID != txn.ID {
			continue
		}
		result, err := c.runTxn(ctx, txn)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

// SubmitConfirmation accepts an externally delivered erasure confirmation
// (for example a late-arriving replica callback). Unknown replicas and
// confirmations that fail snapshot/signature validation are rejected; so are
// confirmations for a closed transaction.
func (c *Coordinator) SubmitConfirmation(ctx context.Context, conf Confirmation) error {
	txn, err := c.cfg.Store.GetTxn(ctx, conf.TxnID)
	if err != nil {
		return err
	}
	if _, ok := txn.Replicas[conf.ReplicaID]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownReplica, conf.ReplicaID)
	}
	if txn.State == TxnDestroyed {
		return ErrTxnClosed
	}
	if err := c.validateConfirmation(txn, conf); err != nil {
		return err
	}
	record := &ConfirmationRecord{Confirmation: conf, ReceivedAt: c.now()}
	return c.cfg.Store.PutConfirmation(ctx, record)
}
