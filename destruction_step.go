package netsec

import (
	"context"
	"sort"
)

func (c *Coordinator) beginDestructionLocked(ctx context.Context, id VolumeID) (*destroyRecord, error) {
	manifest, err := getJSON[volumeManifest](ctx, c.store, manifestKey(id))
	if err != nil {
		if isNotFound(err) {
			return nil, ErrVolumeNotFound
		}
		return nil, err
	}

	record, err := c.loadRecordLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if record != nil {
		// Re-driving destruction must never unfreeze the volume.
		manifest.Frozen = true
		if err := putJSON(ctx, c.store, manifestKey(id), manifest); err != nil {
			return nil, err
		}
		return record, nil
	}

	now := c.clock.Now()
	record = &destroyRecord{
		VolumeID:        manifest.ID,
		Name:            manifest.Name,
		Epoch:           1,
		Phase:           PhaseFreezing,
		Frozen:          true,
		Erased:          map[ReplicaID]bool{},
		UnknownReplicas: map[ReplicaID]bool{},
		UnknownErased:   map[ReplicaID]bool{},
		ObservedAbsent:  map[ReplicaID]bool{},
		StartedAt:       now,
		UpdatedAt:       now,
	}
	// The lease prohibition is durable before any key copy is touched.
	manifest.Frozen = true
	if err := putJSON(ctx, c.store, manifestKey(id), manifest); err != nil {
		return nil, err
	}
	if err := c.saveRecordLocked(ctx, record); err != nil {
		return nil, err
	}
	return record, nil
}

// stepLocked drives the transaction forward. Every intermediate state is
// persisted, so a returned error always leaves a resumable record behind.
func (c *Coordinator) stepLocked(ctx context.Context, record *destroyRecord) (DestructionStatus, error) {
	for {
		manifest, err := getJSON[volumeManifest](ctx, c.store, manifestKey(record.VolumeID))
		if err != nil {
			return DestructionStatus{}, err
		}

		switch record.Phase {
		case PhaseFreezing:
			if !manifest.Frozen || !record.Frozen {
				manifest.Frozen = true
				record.Frozen = true
				record.mark(c.clock.Now())
				if err := putJSON(ctx, c.store, manifestKey(manifest.ID), manifest); err != nil {
					return DestructionStatus{}, err
				}
				if err := c.saveRecordLocked(ctx, record); err != nil {
					return DestructionStatus{}, err
				}
			}
			record.Phase = PhaseDraining
			record.mark(c.clock.Now())
			if err := c.saveRecordLocked(ctx, record); err != nil {
				return DestructionStatus{}, err
			}
		case PhaseDraining:
			active, err := c.activeLeasesLocked(ctx, record.VolumeID)
			if err != nil {
				return DestructionStatus{}, err
			}
			if len(active) > 0 {
				return c.statusLocked(record, manifest), nil
			}
			record.Phase = PhaseErasing
			record.mark(c.clock.Now())
			if err := c.saveRecordLocked(ctx, record); err != nil {
				return DestructionStatus{}, err
			}
		case PhaseErasing:
			progressed, err := c.eraseNextLocked(ctx, manifest, record)
			if err != nil {
				return DestructionStatus{}, err
			}
			if !progressed {
				record.Phase = PhaseReconciling
				record.mark(c.clock.Now())
				if err := c.saveRecordLocked(ctx, record); err != nil {
					return DestructionStatus{}, err
				}
			}
		case PhaseReconciling:
			blocked, err := c.reconcileLocked(ctx, manifest, record)
			if err != nil {
				return DestructionStatus{}, err
			}
			if blocked {
				return c.statusLocked(record, manifest), nil
			}
			now := c.clock.Now()
			record.Phase = PhaseComplete
			record.CompletedAt = &now
			record.mark(now)
			if err := c.saveRecordLocked(ctx, record); err != nil {
				return DestructionStatus{}, err
			}
			// The live generation pointer is replaced by a tombstone only
			// after completion; an in-flight generation keeps the name bound
			// so it cannot be reused, while a destroyed name stays
			// unopenable but can create a fresh generation.
			if err := c.store.Put(ctx, nameKey(manifest.Name), []byte(nameTombstone)); err != nil {
				return DestructionStatus{}, err
			}
		case PhaseComplete:
			// Destruction remains the permanent state, but a post-completion
			// verification scan can still surface a newly observed copy. Such
			// a copy reopens reconciliation and forces an incomplete result
			// until it is erased.
			if c.scanner == nil {
				return c.statusLocked(record, manifest), nil
			}
			scan, err := c.scanner.ScanReplicas(ctx, manifest.ID, manifest.Name)
			if err != nil {
				return DestructionStatus{}, err
			}
			newUnknown := false
			reopened := false
			for _, replica := range scan.Replicas {
				if manifestHasReplica(manifest, replica.ID) {
					if record.Erased[replica.ID] || record.ObservedAbsent[replica.ID] {
						// A confirmed copy reported by a stale scan is
						// ignored; only unconfirmed reachable material
						// reopens erasure.
						continue
					}
					reopened = true
					continue
				}
				if record.UnknownErased[replica.ID] || record.UnknownReplicas[replica.ID] {
					continue
				}
				record.UnknownReplicas[replica.ID] = true
				delete(record.UnknownErased, replica.ID)
				newUnknown = true
			}
			if !newUnknown && !reopened {
				return c.statusLocked(record, manifest), nil
			}
			record.Phase = PhaseReconciling
			if reopened {
				record.Phase = PhaseErasing
			}
			record.CompletedAt = nil
			record.mark(c.clock.Now())
			if err := c.saveRecordLocked(ctx, record); err != nil {
				return DestructionStatus{}, err
			}
			// Restore the live name binding semantics: the generation is no
			// longer fully destroyed, so name reuse must be blocked again.
			if err := c.store.Put(ctx, nameKey(manifest.Name), []byte(manifest.ID)); err != nil {
				return DestructionStatus{}, err
			}
			if newUnknown {
				return c.statusLocked(record, manifest), ErrUnknownReplica
			}
		default:
			return DestructionStatus{}, ErrDestructionUnknown
		}
	}
}

// eraseNextLocked erases the first manifest replica lacking a confirmation.
// It returns progressed=false when every manifest replica is accounted for.
func (c *Coordinator) eraseNextLocked(ctx context.Context, manifest *volumeManifest, record *destroyRecord) (bool, error) {
	for _, replica := range manifest.Replicas {
		if record.Erased[replica.ID] || record.ObservedAbsent[replica.ID] {
			continue
		}
		if c.eraser == nil {
			// Without a backend the host must drive erasure via
			// ReportErasure; the transaction waits rather than assuming
			// success.
			return false, nil
		}
		if err := c.eraser.Erase(ctx, manifest.ID, replica); err != nil {
			// A failed call has no confirmed effect; the record is untouched
			// so a later step retries the same replica.
			return false, err
		}
		record.Erased[replica.ID] = true
		record.mark(c.clock.Now())
		if err := c.saveRecordLocked(ctx, record); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// reconcileLocked compares reachable copies with the manifest. Any observed
// copy outside the manifest is recorded as an unknown replica and keeps
// destruction incomplete until its own erasure confirmation arrives.
func (c *Coordinator) reconcileLocked(ctx context.Context, manifest *volumeManifest, record *destroyRecord) (bool, error) {
	if c.scanner != nil {
		scan, err := c.scanner.ScanReplicas(ctx, manifest.ID, manifest.Name)
		if err != nil {
			return true, err
		}
		for _, replica := range scan.Replicas {
			if manifestHasReplica(manifest, replica.ID) {
				if !record.Erased[replica.ID] && !record.ObservedAbsent[replica.ID] {
					// Reachable material without a confirmation goes back
					// through erasure.
					record.Phase = PhaseErasing
					record.mark(c.clock.Now())
					if err := c.saveRecordLocked(ctx, record); err != nil {
						return true, err
					}
					return true, nil
				}
				continue
			}
			// A previously confirmed non-manifest copy that reappears becomes an
			// outstanding blocker again: its confirmation no longer matches
			// observed storage state.
			delete(record.UnknownErased, replica.ID)
			if !record.UnknownReplicas[replica.ID] {
				record.UnknownReplicas[replica.ID] = true
				record.mark(c.clock.Now())
				if err := c.saveRecordLocked(ctx, record); err != nil {
					return true, err
				}
				// Surface the discovery while the durable state already
				// reflects an incomplete transaction.
				return true, ErrUnknownReplica
			}
		}
	}

	if len(record.UnknownReplicas) > 0 || !manifestFullyErased(manifest, record) {
		return true, nil
	}
	return false, nil
}

func (c *Coordinator) loadRecordLocked(ctx context.Context, id VolumeID) (*destroyRecord, error) {
	record, err := getJSON[destroyRecord](ctx, c.store, destructionKey(id))
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if record.Erased == nil {
		record.Erased = map[ReplicaID]bool{}
	}
	if record.UnknownReplicas == nil {
		record.UnknownReplicas = map[ReplicaID]bool{}
	}
	if record.UnknownErased == nil {
		record.UnknownErased = map[ReplicaID]bool{}
	}
	if record.ObservedAbsent == nil {
		record.ObservedAbsent = map[ReplicaID]bool{}
	}
	return record, nil
}

func (c *Coordinator) saveRecordLocked(ctx context.Context, record *destroyRecord) error {
	return putJSON(ctx, c.store, destructionKey(record.VolumeID), record)
}

func (c *Coordinator) statusLocked(record *destroyRecord, manifest *volumeManifest) DestructionStatus {
	status := DestructionStatus{
		VolumeID: record.VolumeID,
		Name:     record.Name,
		Phase:    record.Phase,
		Epoch:    record.Epoch,
	}

	for id := range record.Erased {
		status.Erased = append(status.Erased, id)
	}
	for id := range record.ObservedAbsent {
		status.Erased = append(status.Erased, id)
	}
	sort.Slice(status.Erased, func(i, j int) bool { return status.Erased[i] < status.Erased[j] })

	for _, replica := range manifest.Replicas {
		if !record.Erased[replica.ID] && !record.ObservedAbsent[replica.ID] {
			status.PendingReplicas = append(status.PendingReplicas, replica.ID)
		}
	}
	for id := range record.UnknownReplicas {
		status.UnknownReplicas = append(status.UnknownReplicas, id)
	}
	sort.Slice(status.UnknownReplicas, func(i, j int) bool {
		return status.UnknownReplicas[i] < status.UnknownReplicas[j]
	})

	switch {
	case record.Phase == PhaseComplete:
		status.Outcome = StatusComplete
	case len(record.UnknownReplicas) > 0:
		status.Outcome = StatusIncomplete
	default:
		status.Outcome = StatusInProgress
	}
	return status
}

func manifestHasReplica(manifest *volumeManifest, id ReplicaID) bool {
	for _, replica := range manifest.Replicas {
		if replica.ID == id {
			return true
		}
	}
	return false
}

func manifestFullyErased(manifest *volumeManifest, record *destroyRecord) bool {
	for _, replica := range manifest.Replicas {
		if !record.Erased[replica.ID] && !record.ObservedAbsent[replica.ID] {
			return false
		}
	}
	return true
}
