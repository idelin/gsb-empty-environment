package netsec

import "sort"

// blockOp is one committed operation in a block's operation chain.
type blockOp struct {
	seq   SeqNum
	txnID uint64
	index uint16
	undo  bool
	data  []byte
}

// Recoverer incrementally rebuilds a filesystem snapshot from a journal
// stream. It is safe to feed entries in any order and to feed duplicates;
// only fully committed and validated transactions are replayed.
//
// Instead of folding the whole log on every snapshot, the Recoverer
// keeps one operation chain per touched block. Because transactions only
// interact through the blocks they share, folding each block's chain in
// modular sequence order yields the same state as a full replay.
type Recoverer struct {
	asm    *assembler
	blocks map[BlockID][]blockOp
}

// NewRecoverer returns an empty Recoverer.
func NewRecoverer() *Recoverer {
	r := &Recoverer{blocks: make(map[BlockID][]blockOp)}
	r.asm = newAssembler(func(t *transaction) {
		for _, op := range t.sortedOps() {
			r.blocks[op.block] = append(r.blocks[op.block], blockOp{
				seq:   t.seq,
				txnID: t.id,
				index: op.index,
				undo:  op.kind == KindUndo,
				data:  op.data,
			})
		}
	})
	return r
}

// Ingest feeds one journal entry. It returns ErrWindowExceeded if
// committing the entry's transaction would make the live sequence window
// reach HalfCircle; the offending transaction is not committed.
func (r *Recoverer) Ingest(e Entry) error {
	return r.asm.ingest(e)
}

// IngestAll feeds a batch of journal entries, stopping at the first
// error.
func (r *Recoverer) IngestAll(entries []Entry) error {
	for _, e := range entries {
		if err := r.Ingest(e); err != nil {
			return err
		}
	}
	return nil
}

// Committed reports how many transactions have been committed and
// replayed so far.
func (r *Recoverer) Committed() int {
	return r.asm.committed
}

// Invalid reports how many transactions have been dropped because of
// conflicting duplicates, descriptor/commit mismatch, or checksum
// failure.
func (r *Recoverer) Invalid() int {
	return r.asm.invalid
}

// Snapshot folds every block's operation chain in modular sequence
// order and returns the recovered block contents. An undo record clears
// every earlier update to its block; updates that arrive after the undo
// take effect again. Blocks whose latest committed operation is an undo
// (or that were never updated) are absent from the snapshot.
func (r *Recoverer) Snapshot() map[BlockID][]byte {
	origin, _ := windowSpan(r.asm.seqs)
	snap := make(map[BlockID][]byte)
	for block, ops := range r.blocks {
		sort.Slice(ops, func(i, j int) bool {
			ki, kj := seqKey(ops[i].seq, origin), seqKey(ops[j].seq, origin)
			if ki != kj {
				return ki < kj
			}
			if ops[i].txnID != ops[j].txnID {
				return ops[i].txnID < ops[j].txnID
			}
			return ops[i].index < ops[j].index
		})
		var data []byte
		for _, op := range ops {
			if op.undo {
				data = nil
			} else {
				data = op.data
			}
		}
		if data != nil {
			snap[block] = cloneBytes(data)
		}
	}
	return snap
}
