package netsec

import "context"

// InstallBatch applies a table update as a fail-closed batch:
//
//  1. all requested entries are made non-deliverable;
//  2. all requests are validated;
//  3. each entry then goes through its own drain barrier and is published
//     individually.
//
// If any request is invalid, every entry remains non-deliverable. If a later
// barrier is canceled or fails, entries already published stay published, the
// failed entry stays closed, and entries not yet processed stay closed. The
// remapper never rolls a safely closed entry back to its old target.
func (d *Device) InstallBatch(caller DomainID, reqs []InstallRequest) error {
	return d.InstallBatchContext(context.Background(), caller, reqs)
}

// InstallBatchContext is InstallBatch with cancellation support. Cancellation
// during a drain barrier leaves the current and remaining entries closed.
func (d *Device) InstallBatchContext(ctx context.Context, caller DomainID, reqs []InstallRequest) error {
	if d == nil || d.rm == nil {
		return fail("install-batch", ErrUnknownDevice, "nil device")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(reqs) == 0 {
		return nil
	}

	r := d.rm
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.deviceLocked(d); err != nil {
		return err
	}

	// Authorization and structural checks happen before any entry is touched.
	// A foreign domain must not be able to close another tenant's entries.
	if caller != d.owner {
		return fail("install-batch", ErrForeignDomain,
			"domain %d attempted to program requester %#04x owned by domain %d",
			caller, d.requester, d.owner)
	}
	seen := make(map[TableIndex]struct{}, len(reqs))
	for _, req := range reqs {
		if _, exists := seen[req.TableIndex]; exists {
			return fail("install-batch", ErrDuplicateTableIndex,
				"requester %#04x index %d", d.requester, req.TableIndex)
		}
		seen[req.TableIndex] = struct{}{}
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := r.waitDeviceIdleLocked(ctx, d); err != nil {
		return err
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	// The wait released r.mu. A concurrent re-tenancy may have revoked this
	// handle or changed the owner while we were waiting, so reauthorize before
	// touching any entry.
	if err := r.deviceLocked(d); err != nil {
		return err
	}
	if caller != d.owner {
		return fail("install-batch", ErrForeignDomain,
			"domain %d attempted to program requester %#04x owned by domain %d",
			caller, d.requester, d.owner)
	}

	d.busy = true
	defer r.releaseDeviceLocked(d)

	mappings := make([]*mapping, len(reqs))
	keys := make([]entryKey, len(reqs))
	// Phase 1: close every requested entry before validating any of them.
	for i, req := range reqs {
		key := entryKey{
			requester:  d.requester,
			generation: d.generation,
			tableIndex: req.TableIndex,
		}
		m := r.entries[key]
		if m == nil {
			m = &mapping{}
			r.entries[key] = m
		}
		m.present = false
		mappings[i] = m
		keys[i] = key
	}

	results := make([]BatchItemResult, len(reqs))
	for i, req := range reqs {
		results[i].TableIndex = req.TableIndex
	}

	// Phase 2: validate all requests. No item is republished if any fails.
	validationFailed := false
	for i, req := range reqs {
		if err := r.validateInstallLocked(d, caller, req); err != nil {
			results[i].Err = err
			validationFailed = true
		}
	}
	if validationFailed {
		for i := range results {
			if results[i].Err == nil {
				results[i].Err = fail("install-batch", ErrBatchAborted,
					"index %d left closed because another item failed validation",
					results[i].TableIndex)
			}
		}
		return &BatchError{Operation: "install-batch", Results: results}
	}

	// Phase 3: per-entry drain barrier, revalidation and publication.
	batchFailed := false
	aborted := false
	for i, req := range reqs {
		if aborted {
			results[i].Err = fail("install-batch", ErrBatchAborted,
				"index %d left closed after batch abort", req.TableIndex)
			batchFailed = true
			continue
		}
		if err := contextErr(ctx); err != nil {
			results[i].Err = fail("install-batch", err,
				"index %d left closed after cancellation", req.TableIndex)
			batchFailed = true
			aborted = true
			continue
		}
		if err := r.waitDrainedLocked(ctx, mappings[i]); err != nil {
			results[i].Err = fail("install-batch", err,
				"index %d left closed because its drain barrier did not complete",
				req.TableIndex)
			batchFailed = true
			aborted = true
			continue
		}
		if err := contextErr(ctx); err != nil {
			results[i].Err = fail("install-batch", err,
				"index %d left closed after cancellation", req.TableIndex)
			batchFailed = true
			aborted = true
			continue
		}
		// The barrier releases the lock. Re-check vCPU membership,
		// generation, range and ownership before publishing this item.
		if err := r.validateInstallLocked(d, caller, req); err != nil {
			results[i].Err = err
			batchFailed = true
			continue
		}
		if r.entries[keys[i]] != mappings[i] {
			results[i].Err = fail("install-batch", ErrBatchAborted,
				"index %d entry was revoked during update", req.TableIndex)
			batchFailed = true
			continue
		}
		mappings[i].target = targetFrom(req)
		mappings[i].target.domain = caller
		mappings[i].present = true
		results[i].Applied = true
	}

	if !batchFailed {
		return nil
	}
	return &BatchError{Operation: "install-batch", Results: results}
}
