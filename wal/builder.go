package wal

// Builder simulates a running database system emitting WAL records:
// it maintains PrevLSN chains per transaction, tracks the in-memory
// DirtyPageTable and transaction table, and can emit checkpoints.
// It is used by tests and the demo to produce pre-crash log files.
type Builder struct {
	w       *Writer
	lastLSN map[uint64]LSN
	dpt     map[uint64]LSN
	txns    map[uint64]LSN
}

// NewBuilder wraps a Writer with transaction-aware record emission.
func NewBuilder(w *Writer) *Builder {
	return &Builder{
		w:       w,
		lastLSN: make(map[uint64]LSN),
		dpt:     make(map[uint64]LSN),
		txns:    make(map[uint64]LSN),
	}
}

func (b *Builder) append(rec *LogRecord) LSN {
	rec.PrevLSN = b.lastLSN[rec.TxnID]
	lsn, err := b.w.Append(rec)
	if err != nil {
		panic(err)
	}
	if rec.TxnID != 0 {
		b.lastLSN[rec.TxnID] = lsn
		b.txns[rec.TxnID] = lsn
	}
	return lsn
}

// Update logs a page modification with its before/after images and
// marks the page dirty in the simulated system.
func (b *Builder) Update(txnID, pageID uint64, before, after []byte) LSN {
	lsn := b.append(&LogRecord{
		TxnID:  txnID,
		Type:   TypeUpdate,
		PageID: pageID,
		Before: before,
		After:  after,
	})
	if _, ok := b.dpt[pageID]; !ok {
		b.dpt[pageID] = lsn
	}
	return lsn
}

// Commit logs a transaction commit.
func (b *Builder) Commit(txnID uint64) LSN {
	return b.append(&LogRecord{TxnID: txnID, Type: TypeCommit})
}

// Abort logs a transaction abort.
func (b *Builder) Abort(txnID uint64) LSN {
	return b.append(&LogRecord{TxnID: txnID, Type: TypeAbort})
}

// End logs the final END record of a transaction.
func (b *Builder) End(txnID uint64) LSN {
	lsn := b.append(&LogRecord{TxnID: txnID, Type: TypeEnd})
	delete(b.txns, txnID)
	return lsn
}

// Checkpoint writes a checkpoint record capturing the current
// DirtyPageTable and transaction table, then simulates a full flush
// of dirty pages (clearing the simulated DPT).
func (b *Builder) Checkpoint() LSN {
	dpt := make(map[uint64]LSN, len(b.dpt))
	for k, v := range b.dpt {
		dpt[k] = v
	}
	txns := make(map[uint64]LSN, len(b.txns))
	for k, v := range b.txns {
		txns[k] = v
	}
	lsn, err := b.w.Append(&LogRecord{Type: TypeCheckpoint, DPT: dpt, Txns: txns})
	if err != nil {
		panic(err)
	}
	b.dpt = make(map[uint64]LSN)
	return lsn
}
