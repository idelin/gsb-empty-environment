package aries

import "path/filepath"

// Scenario describes the generated crash scenario used by the demo and the
// recovery tests.
type Scenario struct {
	WALPath  string
	PagePath string
	// CommittedTxns are the 2 transactions committed before the crash.
	CommittedTxns []uint64
	// UncommittedTxns are the 3 transactions still active at the crash
	// (t5 additionally has an ABORT record in the log).
	UncommittedTxns []uint64
	CheckpointLSN   uint64
}

// Expected cell values after a correct recovery.
var ExpectedCells = []struct {
	PageID uint64
	Off    uint32
	Val    uint32
}{
	{1, 10, 100}, // t1 committed
	{1, 11, 0},   // t3 rolled back
	{2, 20, 200}, // t1 committed
	{2, 21, 0},   // t4 rolled back
	{3, 5, 55},   // t2 committed
	{3, 6, 0},    // t5 rolled back
	{4, 1, 0},    // t3 rolled back
	{4, 2, 0},    // t4 rolled back
}

// BuildCrashScenario generates a WAL + page file in dir with 5 transactions:
//
//	t1, t2 commit; t3, t4 stay running; t5 aborts (all uncommitted).
//
// A fuzzy checkpoint is taken mid-stream, pages 1 and 2 are flushed to disk
// (carrying uncommitted data), and the system then "crashes" without
// flushing pages 3 and 4.
func BuildCrashScenario(dir string) (*Scenario, error) {
	s := &Scenario{
		WALPath:  filepath.Join(dir, "wal.log"),
		PagePath: filepath.Join(dir, "pages.db"),
	}
	db, err := OpenDB(s.WALPath, s.PagePath)
	if err != nil {
		return nil, err
	}
	t1 := db.Begin()
	t2 := db.Begin()
	t3 := db.Begin()
	t4 := db.Begin()
	t5 := db.Begin()
	s.CommittedTxns = []uint64{t1, t2}
	s.UncommittedTxns = []uint64{t3, t4, t5}

	// --- pre-checkpoint activity ---
	if _, err := db.Update(t1, 1, 10, 100); err != nil {
		return nil, err
	}
	if _, err := db.Update(t1, 2, 20, 200); err != nil {
		return nil, err
	}
	if _, err := db.Update(t2, 3, 5, 55); err != nil {
		return nil, err
	}
	if _, err := db.Update(t3, 1, 11, 111); err != nil { // uncommitted, will be flushed
		return nil, err
	}
	db.Commit(t1)
	if _, err := db.Update(t4, 2, 21, 221); err != nil { // uncommitted, will be flushed
		return nil, err
	}

	// --- fuzzy checkpoint: snapshots DPT + TT ---
	s.CheckpointLSN = db.Checkpoint()

	// --- post-checkpoint activity ---
	db.Commit(t2)
	if _, err := db.Update(t5, 3, 6, 66); err != nil {
		return nil, err
	}
	db.Abort(t5) // aborting txn, still uncommitted at crash
	if _, err := db.Update(t3, 4, 1, 41); err != nil {
		return nil, err
	}
	if _, err := db.Update(t4, 4, 2, 42); err != nil {
		return nil, err
	}

	// Background flush writes pages 1 and 2 (including uncommitted data of
	// t3/t4) to disk; pages 3 and 4 remain only in the log.
	if err := db.FlushPages(1, 2); err != nil {
		return nil, err
	}

	// Crash: buffered pages are lost, WAL and flushed pages survive.
	db.Crash()
	return s, nil
}
