package netsec

import (
	"context"
	"strings"
)

// recoverAll resumes every durable destruction transaction visible in the
// store. Recovery deliberately has no "rollback": once a transaction exists,
// its volume's frozen flag is reasserted and only forward progress is
// possible.
func (c *Coordinator) recoverAll(ctx context.Context) error {
	keys, err := c.store.List(ctx, keyPrefixDestroy)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if !strings.HasSuffix(key, keySuffixDestroyRec) {
			continue
		}
		record, err := getJSON[destroyRecord](ctx, c.store, key)
		if err != nil {
			return err
		}
		if record.Phase == PhaseComplete {
			continue
		}
		manifest, err := getJSON[volumeManifest](ctx, c.store, manifestKey(record.VolumeID))
		if err != nil {
			if isNotFound(err) {
				// Manifest gone while a transaction exists is treated as an
				// orphan; nothing usable can be reopened without a manifest.
				continue
			}
			return err
		}
		if !manifest.Frozen {
			manifest.Frozen = true
			if err := putJSON(ctx, c.store, manifestKey(manifest.ID), manifest); err != nil {
				return err
			}
		}
		if !record.Frozen {
			record.Frozen = true
			record.mark(c.clock.Now())
			if err := c.saveRecordLocked(ctx, record); err != nil {
				return err
			}
		}
	}
	return nil
}

// RecoverDestructions returns handles for every non-completed destruction
// transaction, allowing a host process to resume work after a restart.
func (c *Coordinator) RecoverDestructions(ctx context.Context) ([]*Destruction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	keys, err := c.store.List(ctx, keyPrefixDestroy)
	if err != nil {
		return nil, err
	}
	var out []*Destruction
	for _, key := range keys {
		if !strings.HasSuffix(key, keySuffixDestroyRec) {
			continue
		}
		record, err := getJSON[destroyRecord](ctx, c.store, key)
		if err != nil {
			return nil, err
		}
		if record.Phase == PhaseComplete {
			continue
		}
		out = append(out, &Destruction{coordinator: c, volumeID: record.VolumeID})
	}
	return out, nil
}
