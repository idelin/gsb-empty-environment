package applog

// Hooks are fault-injection and crash-simulation points. They exist so tests
// (and chaos tooling) can deterministically exercise failure windows without
// changing production behavior. All hooks are called with the log mutex held;
// they must not call back into the Log.
//
// Error-returning hooks simulate I/O faults: the operation fails and all
// in-memory and on-disk state is rolled back to the pre-operation state.
//
// Notification hooks (no error return) mark crash-simulation points: at that
// instant the on-disk state is exactly what a crash would leave behind, so a
// test can abandon the Log instance and reopen the directory to verify
// recovery behavior.
type Hooks struct {
	// BeforeSegmentSync fires after an appended record is written to the
	// segment file but before it is fsynced. Returning an error fails the
	// Append and truncates the unflushed bytes.
	BeforeSegmentSync func() error
	// AfterSegmentSync fires after the segment fsync: the record is durable
	// but Append has not returned yet. Crash-simulation point.
	AfterSegmentSync func()
	// BeforeManifestSync fires after the new manifest (consumer cursors) is
	// written to the temp file but before it is fsynced. Returning an error
	// fails the cursor operation; durable state is unchanged.
	BeforeManifestSync func() error
	// BeforeManifestRename fires after the manifest temp file is fsynced but
	// before the atomic rename. Returning an error fails the cursor
	// operation; the previously committed manifest remains in effect.
	BeforeManifestRename func() error
	// AfterManifestRename fires after the atomic rename, before the
	// directory fsync. The new cursors are visible to a crash recovery on
	// any filesystem with atomic rename. Crash-simulation point.
	AfterManifestRename func()
}

func (h *Hooks) beforeSegmentSync() error {
	if h != nil && h.BeforeSegmentSync != nil {
		return h.BeforeSegmentSync()
	}
	return nil
}

func (h *Hooks) afterSegmentSync() {
	if h != nil && h.AfterSegmentSync != nil {
		h.AfterSegmentSync()
	}
}

func (h *Hooks) beforeManifestSync() error {
	if h != nil && h.BeforeManifestSync != nil {
		return h.BeforeManifestSync()
	}
	return nil
}

func (h *Hooks) beforeManifestRename() error {
	if h != nil && h.BeforeManifestRename != nil {
		return h.BeforeManifestRename()
	}
	return nil
}

func (h *Hooks) afterManifestRename() {
	if h != nil && h.AfterManifestRename != nil {
		h.AfterManifestRename()
	}
}
