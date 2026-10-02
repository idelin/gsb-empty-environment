package aries

// CrashScenario builds the acceptance-test workload: a WAL containing
// 2 committed transactions (T1, T2) and 3 uncommitted transactions
// (T3 running, T4 running, T5 aborting) with a checkpoint in the
// middle, followed by a crash. It returns the durable storage as it
// looked at crash time (only some dirty pages flushed).
//
// Log layout (LSNs are assigned sequentially from 1):
//
//	 1  T1 UPDATE P1 "v0" -> "T1"
//	 2  T3 UPDATE P3 "v0" -> "T3"
//	 3  T1 COMMIT
//	 4  T2 UPDATE P2 "v0" -> "T2"
//	 5  T4 UPDATE P4 "v0" -> "T4"
//	 6  CHECKPOINT (active T2,T3,T4; DPT P1..P4)
//	 7  T2 COMMIT
//	 8  T3 UPDATE P1 "T1" -> "T3b"   (loser overwrites committed data)
//	 9  T5 UPDATE P2 "T2" -> "T5"    (loser overwrites committed data)
//	10  T5 ABORT
//	11  T4 UPDATE P3 "T3" -> "T4"    (losers interleaved on one page)
//	--  CRASH --
//
// Pre-crash flushes: P1 after LSN 1, P2 after LSN 4. Everything else
// is only in the (lost) buffer pool, so redo must reconstruct it.
type CrashScenario struct {
	WAL       *WAL
	Disk      *MapStorage
	Expected  map[uint64]string // pageID -> expected value after recovery
	PageNames []uint64
}

// BuildCrashScenario creates the WAL at walPath and the crash-time
// disk image. The caller should Close the WAL when done.
func BuildCrashScenario(walPath string) (*CrashScenario, error) {
	wal, err := OpenWAL(walPath)
	if err != nil {
		return nil, err
	}
	disk := NewMapStorage()
	for _, id := range []uint64{1, 2, 3, 4} {
		disk.WritePage(&Page{ID: id, PageLSN: 0, Data: []byte("v0")})
	}

	lastLSN := map[uint64]uint64{} // txnID -> prevLSN chain
	appendRec := func(rec *LogRecord) uint64 {
		rec.PrevLSN = lastLSN[rec.TxnID]
		lsn, err := wal.Append(rec)
		if err != nil {
			panic(err)
		}
		lastLSN[rec.TxnID] = lsn
		return lsn
	}
	update := func(txn, page uint64, before, after string) uint64 {
		return appendRec(&LogRecord{Type: LogUpdate, TxnID: txn, PageID: page,
			Before: []byte(before), After: []byte(after)})
	}

	lsn1 := update(1, 1, "v0", "T1")
	lsn2 := update(3, 3, "v0", "T3")
	appendRec(&LogRecord{Type: LogCommit, TxnID: 1})
	lsn4 := update(2, 2, "v0", "T2")
	lsn5 := update(4, 4, "v0", "T4")

	// Checkpoint: active T2, T3, T4; all four pages dirty.
	ckpt := &LogRecord{Type: LogCheckpoint, After: EncodeCheckpointPayload(&CheckpointPayload{
		Txns:       map[uint64]uint64{2: lastLSN[2], 3: lastLSN[3], 4: lastLSN[4]},
		DirtyPages: map[uint64]uint64{1: lsn1, 3: lsn2, 2: lsn4, 4: lsn5},
	})}
	if _, err := wal.Append(ckpt); err != nil {
		return nil, err
	}

	appendRec(&LogRecord{Type: LogCommit, TxnID: 2})
	update(3, 1, "T1", "T3b")
	update(5, 2, "T2", "T5")
	appendRec(&LogRecord{Type: LogAbort, TxnID: 5})
	update(4, 3, "T3", "T4")
	// --- CRASH: buffer pool lost, only flushed pages survive ---

	// Pre-crash flushes: P1 as of LSN 1, P2 as of LSN 4.
	disk.WritePage(&Page{ID: 1, PageLSN: lsn1, Data: []byte("T1")})
	disk.WritePage(&Page{ID: 2, PageLSN: lsn4, Data: []byte("T2")})

	return &CrashScenario{
		WAL:  wal,
		Disk: disk,
		Expected: map[uint64]string{
			1: "T1", // T3's uncommitted overwrite rolled back
			2: "T2", // T5's uncommitted overwrite rolled back
			3: "v0", // T4 then T3 both rolled back
			4: "v0", // T4 rolled back
		},
		PageNames: []uint64{1, 2, 3, 4},
	}, nil
}
