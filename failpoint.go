package pagestore

// Fault injection points. Set one with SetFailpoint; the store calls the
// hook at the exact moment named. A hook returning an error simulates a
// crash at that point — discard the DB handle afterwards and reopen the
// directory to run recovery, exactly as after a real process crash.
const (
	// FailCommitBeforeAppend fires before any WAL byte is produced.
	FailCommitBeforeAppend = "commit.beforeAppend"
	// FailCommitBeforeSync fires with the txn's records buffered but
	// nothing written to the WAL file.
	FailCommitBeforeSync = "commit.beforeSync"
	// FailCommitAfterSync fires after the commit record is fsynced
	// (transaction durable) but before it becomes visible in memory.
	FailCommitAfterSync = "commit.afterSync"
	// FailCheckpointBeforeSync fires after dirty pages are written to the
	// data file but before it is fsynced.
	FailCheckpointBeforeSync = "checkpoint.beforeSync"
	// FailTornPageWrite, when active, makes the checkpoint write only half
	// of the first dirty page and then fail, simulating power loss in the
	// middle of a page write. The hook's own return value is ignored.
	FailTornPageWrite = "pages.tornWrite"
)

// SetFailpoint installs (or clears, with nil) a fault-injection hook.
func (db *DB) SetFailpoint(name string, fn func() error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if fn == nil {
		delete(db.failpts, name)
		return
	}
	db.failpts[name] = fn
}

func (db *DB) fail(name string) error {
	if fn, ok := db.failpts[name]; ok && fn != nil {
		return fn()
	}
	return nil
}

func (db *DB) failActive(name string) bool {
	_, ok := db.failpts[name]
	return ok
}
