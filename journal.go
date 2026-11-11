package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sort"
)

// ErrWindowExceeded is returned when committing a transaction would make
// the live sequence-number window reach or exceed HalfCircle, at which
// point modular ordering is no longer well defined.
var ErrWindowExceeded = errors.New("netsec: sequence window reaches half of the 32-bit space")

// BlockID identifies a filesystem block in the journal.
type BlockID uint64

// Kind identifies the type of a journal Entry.
type Kind uint8

const (
	// KindDescriptor opens a transaction and carries its validation sum.
	KindDescriptor Kind = iota + 1
	// KindUpdate records a block update within a transaction.
	KindUpdate
	// KindUndo records an undo record within a transaction.
	KindUndo
	// KindCommit is the commit marker of a transaction.
	KindCommit
)

// Entry is a single record of the journal stream. A transaction is
// described by one KindDescriptor entry, any number of KindUpdate and
// KindUndo entries, and one KindCommit entry. Entries may arrive out of
// order and may be delivered more than once; the commit marker must be
// the last entry of its transaction in the stream.
type Entry struct {
	Kind  Kind
	TxnID uint64
	Seq   SeqNum  // descriptor and commit entries
	Index uint16  // update and undo entries: position within the transaction
	Block BlockID // update and undo entries
	Data  []byte  // update entries only
	Sum   uint32  // descriptor entries only: checksum of the transaction
}

// txnOp is the normalized form of a KindUpdate or KindUndo entry.
type txnOp struct {
	kind  Kind
	index uint16
	block BlockID
	data  []byte
}

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// ChecksumEntries computes the validation checksum that the descriptor
// of a transaction must carry. ops are the transaction's KindUpdate and
// KindUndo entries; they are ordered by Index before hashing.
func ChecksumEntries(txnID uint64, seq SeqNum, ops []Entry) uint32 {
	norm := make([]txnOp, len(ops))
	for i, op := range ops {
		norm[i] = txnOp{kind: op.Kind, index: op.Index, block: op.Block, data: op.Data}
	}
	return checksumOps(txnID, seq, norm)
}

func checksumOps(txnID uint64, seq SeqNum, ops []txnOp) uint32 {
	sorted := make([]txnOp, len(ops))
	copy(sorted, ops)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].index < sorted[j].index })

	h := crc32.New(castagnoli)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], txnID)
	h.Write(buf[:])
	binary.LittleEndian.PutUint32(buf[:4], uint32(seq))
	h.Write(buf[:4])
	for _, op := range sorted {
		h.Write([]byte{byte(op.kind)})
		binary.LittleEndian.PutUint16(buf[:2], op.index)
		h.Write(buf[:2])
		binary.LittleEndian.PutUint64(buf[:], uint64(op.block))
		h.Write(buf[:])
		binary.LittleEndian.PutUint32(buf[:4], uint32(len(op.data)))
		h.Write(buf[:4])
		h.Write(op.data)
	}
	return h.Sum32()
}

// transaction is the assembly state of one journal transaction.
type transaction struct {
	id        uint64
	seq       SeqNum
	haveDesc  bool
	sum       uint32
	commitSeq *SeqNum
	committed bool
	invalid   bool
	ops       map[uint16]txnOp
}

func (t *transaction) sortedOps() []txnOp {
	ops := make([]txnOp, 0, len(t.ops))
	for _, op := range t.ops {
		ops = append(ops, op)
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].index < ops[j].index })
	return ops
}

// assembler turns a stream of journal entries into committed, validated
// transactions. It is shared by the incremental Recoverer and the
// full-replay reference so both agree on dedup, validation, commit
// rules, and the sequence-window check.
type assembler struct {
	txns      map[uint64]*transaction
	seqs      []SeqNum // sequence numbers of committed transactions
	committed int
	invalid   int
	onCommit  func(*transaction)
}

func newAssembler(onCommit func(*transaction)) *assembler {
	return &assembler{
		txns:     make(map[uint64]*transaction),
		onCommit: onCommit,
	}
}

func (a *assembler) txn(id uint64) *transaction {
	t, ok := a.txns[id]
	if !ok {
		t = &transaction{id: id, ops: make(map[uint16]txnOp)}
		a.txns[id] = t
	}
	return t
}

func (a *assembler) ingest(e Entry) error {
	switch e.Kind {
	case KindDescriptor:
		t := a.txn(e.TxnID)
		if t.invalid || t.committed {
			return nil
		}
		if t.haveDesc {
			// A repeated descriptor must be an exact duplicate.
			if t.seq != e.Seq || t.sum != e.Sum {
				t.invalid = true
				a.invalid++
			}
			return nil
		}
		t.haveDesc, t.seq, t.sum = true, e.Seq, e.Sum
		return a.maybeCommit(t)
	case KindUpdate, KindUndo:
		t := a.txn(e.TxnID)
		if t.invalid || t.committed {
			// Entries delivered after the commit marker are late
			// duplicates and have no effect.
			return nil
		}
		op := txnOp{kind: e.Kind, index: e.Index, block: e.Block, data: cloneBytes(e.Data)}
		if prev, ok := t.ops[e.Index]; ok {
			// A repeated operation must be an exact duplicate.
			if prev.kind != op.kind || prev.block != op.block || !bytes.Equal(prev.data, op.data) {
				t.invalid = true
				a.invalid++
			}
			return nil
		}
		t.ops[e.Index] = op
		return nil
	case KindCommit:
		t := a.txn(e.TxnID)
		if t.invalid || t.committed {
			return nil
		}
		if t.commitSeq != nil {
			// A repeated commit marker must be an exact duplicate.
			if *t.commitSeq != e.Seq {
				t.invalid = true
				a.invalid++
			}
			return nil
		}
		seq := e.Seq
		t.commitSeq = &seq
		return a.maybeCommit(t)
	}
	return nil
}

// maybeCommit finalizes a transaction once both its descriptor and its
// commit marker have arrived. Only transactions that are fully committed
// and pass validation are released to the replayer.
func (a *assembler) maybeCommit(t *transaction) error {
	if t.committed || t.invalid || !t.haveDesc || t.commitSeq == nil {
		return nil
	}
	if *t.commitSeq != t.seq {
		t.invalid = true
		a.invalid++
		return nil
	}
	if checksumOps(t.id, t.seq, t.sortedOps()) != t.sum {
		t.invalid = true
		a.invalid++
		return nil
	}
	candidate := make([]SeqNum, len(a.seqs), len(a.seqs)+1)
	copy(candidate, a.seqs)
	candidate = append(candidate, t.seq)
	if _, span := windowSpan(candidate); span >= HalfCircle {
		return ErrWindowExceeded
	}
	a.seqs = candidate
	t.committed = true
	a.committed++
	if a.onCommit != nil {
		a.onCommit(t)
	}
	return nil
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
