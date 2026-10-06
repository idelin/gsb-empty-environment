// Package doublebuf implements a double-buffered store with versioned
// snapshots.
//
// A Store keeps at most two buffers: one active buffer that readers
// acquire, and one standby buffer that a writer prepares. Committing an
// update atomically swaps the standby buffer into the active slot. If a
// write fails or is aborted, the active buffer keeps the old version.
//
// A standby buffer still pinned by readers cannot be reused, so with only
// two buffers a third update blocks in Begin until the last reader of the
// retired buffer releases it, or the caller's context is canceled.
package doublebuf
