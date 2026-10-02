package aries

import "fmt"

// Txn is a handle to one running transaction.
type Txn struct {
	db   *DB
	id   TxID
	done bool
}

// ID returns the transaction identifier.
func (tx *Txn) ID() TxID { return tx.id }

// Write applies data to a page at offset (relative to the usable
// payload area). WAL protocol: the update record is appended to the
// log BEFORE the in-memory page is modified.
func (tx *Txn) Write(pageID PageID, offset uint32, data []byte) error {
	if tx.done {
		return fmt.Errorf("transaction %d is already finished", tx.id)
	}
	if len(data) == 0 {
		return fmt.Errorf("empty write")
	}
	db := tx.db
	page, err := db.bp.Fetch(pageID)
	if err != nil {
		return err
	}
	if int(offset)+len(data) > len(page.Data()) {
		return fmt.Errorf("write of %d bytes at offset %d exceeds page payload", len(data), offset)
	}
	before := make([]byte, len(data))
	copy(before, page.Data()[offset:int(offset)+len(data)])

	db.mu.Lock()
	prev := db.txnTable[tx.id].LastLSN
	db.mu.Unlock()

	// 1. Log first.
	rec := &LogRecord{
		Type:    RecUpdate,
		TxID:    tx.id,
		PrevLSN: prev,
		PageID:  pageID,
		Offset:  offset,
		Before:  before,
		After:   append([]byte(nil), data...),
	}
	lsn := db.lm.Append(rec)

	// 2. Then update memory: txn table, DPT, and the page itself.
	db.mu.Lock()
	db.txnTable[tx.id].LastLSN = lsn
	if _, ok := db.dpt[pageID]; !ok {
		db.dpt[pageID] = lsn
	}
	db.mu.Unlock()
	copy(page.Data()[offset:], data)
	page.SetLSN(lsn)
	page.dirty = true
	return nil
}

// Commit makes the transaction durable: the commit record is forced to
// stable storage before Commit returns.
func (tx *Txn) Commit() error {
	if tx.done {
		return fmt.Errorf("transaction %d is already finished", tx.id)
	}
	db := tx.db
	db.mu.Lock()
	prev := db.txnTable[tx.id].LastLSN
	db.mu.Unlock()
	commitLSN := db.lm.Append(&LogRecord{Type: RecCommit, TxID: tx.id, PrevLSN: prev})
	if err := db.lm.Flush(commitLSN); err != nil {
		return err
	}
	db.lm.Append(&LogRecord{Type: RecEnd, TxID: tx.id, PrevLSN: commitLSN})
	db.mu.Lock()
	delete(db.txnTable, tx.id)
	db.mu.Unlock()
	tx.done = true
	return nil
}

// Abort rolls the transaction back using compensation log records.
func (tx *Txn) Abort() error {
	if tx.done {
		return fmt.Errorf("transaction %d is already finished", tx.id)
	}
	db := tx.db
	db.mu.Lock()
	prev := db.txnTable[tx.id].LastLSN
	db.txnTable[tx.id].Status = StatusAborting
	db.mu.Unlock()
	abortLSN := db.lm.Append(&LogRecord{Type: RecAbort, TxID: tx.id, PrevLSN: prev})
	db.mu.Lock()
	db.txnTable[tx.id].LastLSN = abortLSN
	db.mu.Unlock()

	db.undo(tx.id)

	db.mu.Lock()
	last := db.txnTable[tx.id].LastLSN
	db.mu.Unlock()
	db.lm.Append(&LogRecord{Type: RecEnd, TxID: tx.id, PrevLSN: last})
	db.mu.Lock()
	delete(db.txnTable, tx.id)
	db.mu.Unlock()
	tx.done = true
	return nil
}
