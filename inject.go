package kvdb

// Named fault-injection points. Tests (or embedders) install an Injector
// via Options to simulate crashes and I/O errors at exact boundaries.
const (
	// InjectBeforeAppendSync fires after a record has been appended to the
	// active segment but before it is fsynced (SyncWrites or Sync).
	InjectBeforeAppendSync = "append.before-sync"
	// InjectBeforeManifestWrite fires before a new MANIFEST is written and
	// atomically renamed into place.
	InjectBeforeManifestWrite = "manifest.before-write"
	// InjectCompactBeforeCommit fires during compaction after the merged
	// segment has been written, fsynced and renamed into place, but before
	// the in-memory index and MANIFEST are switched to it. Simulates a
	// crash before the compaction becomes visible.
	InjectCompactBeforeCommit = "compact.before-commit"
	// InjectCompactAfterCommit fires after the MANIFEST commit but before
	// obsolete segments are swept. Simulates a crash after the switch.
	InjectCompactAfterCommit = "compact.after-commit"
)

// Injector is a fault-injection hook. Returning a non-nil error from a
// point makes the corresponding operation fail as if the process had
// crashed or the disk had failed at that exact boundary.
type Injector interface {
	Inject(point string) error
}

// InjectorFunc adapts a plain function to the Injector interface.
type InjectorFunc func(point string) error

// Inject implements Injector.
func (f InjectorFunc) Inject(point string) error { return f(point) }

func (db *DB) inject(point string) error {
	if db.opts.Injector == nil {
		return nil
	}
	return db.opts.Injector.Inject(point)
}
