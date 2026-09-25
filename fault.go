package btree

import "errors"

// FaultPoint identifies a stage in the commit protocol where an injected
// crash can be simulated. The hook installed via Options.FaultHook is called
// at each point; returning true simulates an immediate process crash
// (everything written so far may or may not have reached stable storage).
type FaultPoint int

const (
	// FPAfterDataPage fires after each individual B-tree node page has been
	// written (but not necessarily fsynced) during a commit.
	FPAfterDataPage FaultPoint = iota
	// FPAfterDataWrite fires after all node pages and the node map have been
	// written, before the data fsync.
	FPAfterDataWrite
	// FPAfterDataSync fires after the data fsync completed, before the
	// superblock (the commit record) is written.
	FPAfterDataSync
	// FPAfterSuperWrite fires after the superblock slot has been written,
	// before the final fsync.
	FPAfterSuperWrite
	// FPAfterSuperSync fires after the final fsync; the transaction is fully
	// durable at this point.
	FPAfterSuperSync
)

func (fp FaultPoint) String() string {
	switch fp {
	case FPAfterDataPage:
		return "AfterDataPage"
	case FPAfterDataWrite:
		return "AfterDataWrite"
	case FPAfterDataSync:
		return "AfterDataSync"
	case FPAfterSuperWrite:
		return "AfterSuperWrite"
	case FPAfterSuperSync:
		return "AfterSuperSync"
	}
	return "Unknown"
}

var (
	// ErrCrashed is returned by every operation on a DB after an injected
	// crash. The process is assumed dead; reopen the database from disk.
	ErrCrashed = errors.New("btree: injected crash, database handle is dead; reopen from disk")
	// ErrCorrupt is returned when the on-disk file fails consistency checks.
	ErrCorrupt = errors.New("btree: corrupt or invalid database file")
	// ErrKeyTooLarge is returned when a key cannot fit in an internal node.
	ErrKeyTooLarge = errors.New("btree: key too large for page size")
	// ErrValueTooLarge is returned when a key/value pair cannot fit in a leaf.
	ErrValueTooLarge = errors.New("btree: value too large for page size")
)
