package waldb

import (
	"encoding/binary"
	"fmt"
)

// The checker is independent of the recovery code: it recomputes the
// expected database contents from the transaction outcomes recorded
// in the durable log plus the original page values, and validates
// structural invariants of the log itself.

// CommittedTxns parses the durable WAL and returns, per transaction,
// whether it committed (commit record persisted) and its per-page
// deltas. Uncommitted or crashed transactions contribute nothing.
func CommittedTxns(db *DB) (committed map[uint64]bool, deltas map[uint64]map[uint32]int64, err error) {
	size, err := db.wal.Size()
	if err != nil {
		return nil, nil, err
	}
	data := make([]byte, size)
	if size > 0 {
		if _, err := db.wal.ReadAt(data, 0); err != nil {
			return nil, nil, err
		}
	}
	recs, _ := scanLog(data)
	committed = map[uint64]bool{}
	deltas = map[uint64]map[uint32]int64{}
	for _, r := range recs {
		switch r.Type {
		case RecUpdate:
			d, ok := deltas[r.TxnID]
			if !ok {
				d = map[uint32]int64{}
				deltas[r.TxnID] = d
			}
			d[r.PageID] += r.After - r.Before
		case RecCommit:
			committed[r.TxnID] = true
		}
	}
	return committed, deltas, nil
}

// ExpectPages recomputes expected page contents: original values plus
// the deltas of committed transactions only.
func ExpectPages(original []int64, committed map[uint64]bool, deltas map[uint64]map[uint32]int64) []int64 {
	exp := append([]int64(nil), original...)
	for txn, d := range deltas {
		if !committed[txn] {
			continue
		}
		for pg, delta := range d {
			exp[pg] += delta
		}
	}
	return exp
}

// CheckPages compares every durable page against expected values.
func CheckPages(db *DB, expected []int64) error {
	for id := 0; id < NumPages; id++ {
		v, _, err := db.ReadPage(uint32(id))
		if err != nil {
			return err
		}
		if v != expected[id] {
			return fmt.Errorf("page %d: got %d, want %d", id, v, expected[id])
		}
	}
	return nil
}

// CheckRecovered is the full independent verification: committed
// effects preserved, uncommitted effects gone.
func CheckRecovered(db *DB, original []int64) error {
	committed, deltas, err := CommittedTxns(db)
	if err != nil {
		return err
	}
	if err := CheckPages(db, ExpectPages(original, committed, deltas)); err != nil {
		return err
	}
	return CheckLog(db)
}

// CheckLog validates structural invariants of the durable WAL and the
// WAL rule against the durable pages:
//   - the whole file parses (no trailing garbage / incomplete record)
//   - LSNs are strictly increasing
//   - every CLR carries an undoNextLSN pointing backwards
//   - every page's pageLSN refers to an existing update/CLR of that
//     page (i.e. the log was durable before the page was)
func CheckLog(db *DB) error {
	size, err := db.wal.Size()
	if err != nil {
		return err
	}
	data := make([]byte, size)
	if size > 0 {
		if _, err := db.wal.ReadAt(data, 0); err != nil {
			return err
		}
	}
	recs, validLen := scanLog(data)
	if validLen != len(data) {
		return fmt.Errorf("wal: %d trailing garbage bytes after LSN scan", len(data)-validLen)
	}
	byLSN := map[uint64]*LogRecord{}
	var prev uint64
	for i, r := range recs {
		if i > 0 && r.LSN <= prev {
			return fmt.Errorf("wal: LSN %d not strictly increasing after %d", r.LSN, prev)
		}
		prev = r.LSN
		byLSN[r.LSN] = r
		switch r.Type {
		case RecCLR:
			if r.UndoNextLSN >= r.LSN {
				return fmt.Errorf("wal: CLR %d has undoNextLSN %d not pointing backwards", r.LSN, r.UndoNextLSN)
			}
		case RecUpdate:
			if r.PageID >= NumPages {
				return fmt.Errorf("wal: update %d references bad page %d", r.LSN, r.PageID)
			}
		}
	}
	for id := 0; id < NumPages; id++ {
		buf := make([]byte, PageSize)
		if _, err := db.data.ReadAt(buf, int64(id)*PageSize); err != nil {
			return err
		}
		plsn := binary.LittleEndian.Uint64(buf[0:8])
		if plsn == 0 {
			continue
		}
		rec, ok := byLSN[plsn]
		if !ok {
			return fmt.Errorf("page %d: pageLSN %d missing from durable log (WAL rule violated)", id, plsn)
		}
		if rec.PageID != uint32(id) || (rec.Type != RecUpdate && rec.Type != RecCLR) {
			return fmt.Errorf("page %d: pageLSN %d refers to %s of page %d", id, plsn, rec.Type, rec.PageID)
		}
	}
	return nil
}
