package netsec

import (
	"context"
	"time"
)

// Destruction is a resumable handle to one volume's destruction transaction.
// Its methods are safe for concurrent use.
type Destruction struct {
	coordinator *Coordinator
	volumeID    VolumeID
}

// ID returns the volume generation being destroyed.
func (d *Destruction) ID() VolumeID { return d.volumeID }

// DestroyVolume opens (or resumes) the destruction transaction for a volume
// and runs the state machine until it completes, becomes incomplete, or hits
// a retriable storage/erasure error. Re-calling it after a crash resumes the
// same transaction rather than starting a new one.
func (c *Coordinator) DestroyVolume(ctx context.Context, v *Volume) (*Destruction, error) {
	if v == nil {
		return nil, ErrVolumeNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	record, err := c.beginDestructionLocked(ctx, v.manifest.ID)
	if err != nil {
		return nil, err
	}
	d := &Destruction{coordinator: c, volumeID: v.manifest.ID}
	if _, err := c.stepLocked(ctx, record); err != nil {
		return d, err
	}
	return d, nil
}

// Destruction returns the active transaction handle for a volume generation.
// It returns ErrDestructionUnknown if destruction never started.
func (c *Coordinator) Destruction(ctx context.Context, id VolumeID) (*Destruction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	record, err := c.loadRecordLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, ErrDestructionUnknown
	}
	return &Destruction{coordinator: c, volumeID: id}, nil
}

// Step re-runs the state machine, for example after a late erasure
// confirmation or after a transient storage fault. It returns the current
// status snapshot.
func (d *Destruction) Step(ctx context.Context) (DestructionStatus, error) {
	c := d.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()

	record, err := c.loadRecordLocked(ctx, d.volumeID)
	if err != nil {
		return DestructionStatus{}, err
	}
	if record == nil {
		return DestructionStatus{}, ErrDestructionUnknown
	}
	return c.stepLocked(ctx, record)
}

// Status returns a point-in-time snapshot without advancing the transaction.
func (d *Destruction) Status(ctx context.Context) (DestructionStatus, error) {
	c := d.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()

	record, err := c.loadRecordLocked(ctx, d.volumeID)
	if err != nil {
		return DestructionStatus{}, err
	}
	if record == nil {
		return DestructionStatus{}, ErrDestructionUnknown
	}
	manifest, err := getJSON[volumeManifest](ctx, c.store, manifestKey(record.VolumeID))
	if err != nil {
		return DestructionStatus{}, err
	}
	return c.statusLocked(record, manifest), nil
}

// ReportErasure records an externally collected erasure confirmation. It is
// the entry point for asynchronous and late confirmations: a confirmation can
// arrive after process restart as long as it names the original generation
// and replica. A confirmation for a copy outside the manifest is accepted as
// proof that the unknown copy is gone; a later scan that still observes it
// reopens the blocker.
func (d *Destruction) ReportErasure(ctx context.Context, replicaID ReplicaID) (DestructionStatus, error) {
	if replicaID == "" {
		return DestructionStatus{}, ErrReplicaNotFound
	}
	c := d.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()

	record, err := c.loadRecordLocked(ctx, d.volumeID)
	if err != nil {
		return DestructionStatus{}, err
	}
	if record == nil {
		return DestructionStatus{}, ErrDestructionUnknown
	}
	manifest, err := getJSON[volumeManifest](ctx, c.store, manifestKey(record.VolumeID))
	if err != nil {
		return DestructionStatus{}, err
	}
	if record.Phase == PhaseComplete {
		return c.statusLocked(record, manifest), ErrVolumeDestroyed
	}

	if !manifestHasReplica(manifest, replicaID) {
		// A confirmation arriving before any scan both records the copy and
		// immediately clears its blocker.
		delete(record.UnknownReplicas, replicaID)
		if !record.UnknownErased[replicaID] {
			record.UnknownErased[replicaID] = true
			record.mark(c.clock.Now())
			if err := c.saveRecordLocked(ctx, record); err != nil {
				return DestructionStatus{}, err
			}
		}
		return c.stepLocked(ctx, record)
	}
	if record.Erased[replicaID] || record.ObservedAbsent[replicaID] {
		return c.statusLocked(record, manifest), ErrAlreadyErased
	}
	record.Erased[replicaID] = true
	record.mark(c.clock.Now())
	if err := c.saveRecordLocked(ctx, record); err != nil {
		return DestructionStatus{}, err
	}
	return c.stepLocked(ctx, record)
}

// ReportAbsent records that an external scan proved a manifest replica's
// location empty. An absent copy counts as an erasure confirmation.
func (d *Destruction) ReportAbsent(ctx context.Context, replicaID ReplicaID) (DestructionStatus, error) {
	if replicaID == "" {
		return DestructionStatus{}, ErrReplicaNotFound
	}
	c := d.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()

	record, err := c.loadRecordLocked(ctx, d.volumeID)
	if err != nil {
		return DestructionStatus{}, err
	}
	if record == nil {
		return DestructionStatus{}, ErrDestructionUnknown
	}
	manifest, err := getJSON[volumeManifest](ctx, c.store, manifestKey(record.VolumeID))
	if err != nil {
		return DestructionStatus{}, err
	}
	if !manifestHasReplica(manifest, replicaID) {
		return c.statusLocked(record, manifest), ErrReplicaNotFound
	}
	if record.Erased[replicaID] || record.ObservedAbsent[replicaID] {
		return c.statusLocked(record, manifest), ErrAlreadyErased
	}
	record.ObservedAbsent[replicaID] = true
	record.mark(c.clock.Now())
	if err := c.saveRecordLocked(ctx, record); err != nil {
		return DestructionStatus{}, err
	}
	return c.stepLocked(ctx, record)
}

func (r *destroyRecord) mark(now time.Time) {
	r.Epoch++
	r.UpdatedAt = now
}
