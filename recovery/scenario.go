package recovery

import (
	"aries/wal"
)

// Scenario is the canonical crash used by the demo and the acceptance test:
// five transactions over three pages, a checkpoint in the middle, then a
// crash with two committed, two running, and one aborting transaction.
type Scenario struct {
	Winners   []uint64          // committed before the crash
	Losers    []uint64          // rolled back by recovery
	PageIDs   []uint64          // pages touched
	Expect    map[uint64][]byte // expected durable page prefix after recovery
	RecordCnt int               // records written before the crash
}

// BuildCrashScenario appends the sample WAL to w and returns the expectations.
//
// Timeline (T1,T2 commit; T3,T4 never finish; T5 aborts mid-flight):
//
//	T1 upd p1[0]="AAAA"   T2 upd p2[0]="BBBB"   CHECKPOINT(ATT{T1,T2}, DPT{p1,p2})
//	T3 upd p3[0]="CCCC"   T1 upd p1[4]="1111"   T1 commit
//	T2 upd p2[4]="2222"   T2 commit             T4 upd p1[8]="4444"
//	T5 upd p3[4]="5555"   T5 abort              T5 CLR(undo p3[4])
//	T3 upd p2[8]="3333"   --- CRASH ---
func BuildCrashScenario(w *wal.Writer) (*Scenario, error) {
	must := func(rec *wal.Record) (uint64, error) {
		return w.Append(rec)
	}
	upd := func(txn, page uint64, off uint16, before, after string) *wal.Record {
		return &wal.Record{Type: wal.RecUpdate, TxnID: txn, PageID: page, Offset: off,
			Before: []byte(before), After: []byte(after)}
	}

	var err error
	var lsn uint64
	prev := map[uint64]uint64{}
	put := func(rec *wal.Record) (uint64, error) {
		rec.PrevLSN = prev[rec.TxnID]
		lsn, err = must(rec)
		if err != nil {
			return 0, err
		}
		prev[rec.TxnID] = lsn
		return lsn, nil
	}

	t1u1, err := put(upd(1, 1, 0, "\x00\x00\x00\x00", "AAAA"))
	if err != nil {
		return nil, err
	}
	t2u1, err := put(upd(2, 2, 0, "\x00\x00\x00\x00", "BBBB"))
	if err != nil {
		return nil, err
	}
	if _, err := w.Append(&wal.Record{Type: wal.RecCheckpoint,
		Txns: []wal.CheckpointTxn{
			{TxnID: 1, LastLSN: t1u1, Status: wal.TxnRunning},
			{TxnID: 2, LastLSN: t2u1, Status: wal.TxnRunning},
		},
		Pages: []wal.CheckpointPage{{PageID: 1, RecLSN: t1u1}, {PageID: 2, RecLSN: t2u1}},
	}); err != nil {
		return nil, err
	}
	if _, err := put(upd(3, 3, 0, "\x00\x00\x00\x00", "CCCC")); err != nil {
		return nil, err
	}
	if _, err := put(upd(1, 1, 4, "\x00\x00\x00\x00", "1111")); err != nil {
		return nil, err
	}
	if _, err := put(&wal.Record{Type: wal.RecCommit, TxnID: 1}); err != nil {
		return nil, err
	}
	if _, err := put(upd(2, 2, 4, "\x00\x00\x00\x00", "2222")); err != nil {
		return nil, err
	}
	if _, err := put(&wal.Record{Type: wal.RecCommit, TxnID: 2}); err != nil {
		return nil, err
	}
	if _, err := put(upd(4, 1, 8, "\x00\x00\x00\x00", "4444")); err != nil {
		return nil, err
	}
	t5u, err := put(upd(5, 3, 4, "\x00\x00\x00\x00", "5555"))
	if err != nil {
		return nil, err
	}
	if _, err := put(&wal.Record{Type: wal.RecAbort, TxnID: 5}); err != nil {
		return nil, err
	}
	// T5 begins undoing before the crash: one CLR is already in the log.
	if _, err := put(&wal.Record{Type: wal.RecCLR, TxnID: 5, PageID: 3, Offset: 4,
		UndoNext: wal.NilLSN, Before: []byte("5555"), After: []byte("\x00\x00\x00\x00")}); err != nil {
		return nil, err
	}
	_ = t5u
	if _, err := put(upd(3, 2, 8, "\x00\x00\x00\x00", "3333")); err != nil {
		return nil, err
	}
	if err := w.Sync(); err != nil {
		return nil, err
	}

	return &Scenario{
		Winners: []uint64{1, 2},
		Losers:  []uint64{3, 4, 5},
		PageIDs: []uint64{1, 2, 3},
		Expect: map[uint64][]byte{
			1: []byte("AAAA1111\x00\x00\x00\x00"),
			2: []byte("BBBB2222\x00\x00\x00\x00"),
			3: []byte("\x00\x00\x00\x00\x00\x00\x00\x00"), // T3's "CCCC" and T5's "5555" both rolled back
		},
		RecordCnt: 13,
	}, nil
}
