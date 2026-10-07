// Package sercheck builds version-precise dependency graphs from recorded
// transaction histories and checks conflict serializability two ways:
// by cycle detection on the dependency graph, and by finite enumeration
// of serial executions that reproduce every observed read.
package sercheck

import "fmt"

// TxnID identifies a transaction. TxnID 0 is reserved for the implicit
// initial transaction that wrote the initial version of every key.
type TxnID uint64

// InitialTxn is the implicit transaction that produced the initial
// version of every key.
const InitialTxn TxnID = 0

// Key is a database key.
type Key string

// Value is a database value.
type Value string

// OpKind distinguishes reads from writes.
type OpKind int

const (
	OpRead OpKind = iota
	OpWrite
)

func (k OpKind) String() string {
	if k == OpRead {
		return "R"
	}
	return "W"
}

// Op is a single recorded operation.
//
// For reads, ReadFrom records exactly which version was observed: the
// transaction that wrote it, or InitialTxn for the initial version.
// Conflicts are derived from this concrete version information, never
// from key equality alone.
type Op struct {
	Txn      TxnID
	Kind     OpKind
	Key      Key
	Value    Value
	ReadFrom TxnID // reads only: writer of the observed version
}

func (op Op) String() string {
	if op.Kind == OpRead {
		return fmt.Sprintf("T%d:R(%s)=%q<-T%d", op.Txn, op.Key, op.Value, op.ReadFrom)
	}
	return fmt.Sprintf("T%d:W(%s)=%q", op.Txn, op.Key, op.Value)
}

// History is a recorded execution. Operations are kept in recording
// order; commit order defines the version order of each key.
type History struct {
	ops       []Op
	committed map[TxnID]bool
	commitSeq map[TxnID]int // 1-based commit order
	initial   map[Key]Value
}

// NewHistory returns an empty history.
func NewHistory() *History {
	return &History{
		committed: make(map[TxnID]bool),
		commitSeq: make(map[TxnID]int),
		initial:   make(map[Key]Value),
	}
}

// SetInitial records the initial value of a key (version written by
// InitialTxn). Keys without an explicit initial value default to "".
func (h *History) SetInitial(k Key, v Value) { h.initial[k] = v }

// Read records that txn read key and observed the version written by
// from (InitialTxn for the initial version), seeing value.
func (h *History) Read(txn TxnID, k Key, value Value, from TxnID) {
	h.ops = append(h.ops, Op{Txn: txn, Kind: OpRead, Key: k, Value: value, ReadFrom: from})
}

// Write records that txn wrote value to key.
func (h *History) Write(txn TxnID, k Key, value Value) {
	h.ops = append(h.ops, Op{Txn: txn, Kind: OpWrite, Key: k, Value: value})
}

// Commit marks txn as committed. The order of Commit calls defines the
// per-key version order used to derive WW and RW edges. Transactions
// that never commit are excluded from all analysis.
func (h *History) Commit(txn TxnID) {
	if !h.committed[txn] {
		h.committed[txn] = true
		h.commitSeq[txn] = len(h.commitSeq) + 1
	}
}

// Ops returns all recorded operations in recording order.
func (h *History) Ops() []Op { return append([]Op(nil), h.ops...) }

// Committed reports whether txn committed.
func (h *History) Committed(txn TxnID) bool { return h.committed[txn] }

// CommittedTxns returns the committed transaction IDs in commit order.
func (h *History) CommittedTxns() []TxnID {
	txns := make([]TxnID, 0, len(h.committed))
	for t := range h.committed {
		txns = append(txns, t)
	}
	// commitSeq is 1-based and dense; sort by it.
	for i := 1; i < len(txns); i++ {
		for j := i; j > 0 && h.commitSeq[txns[j]] < h.commitSeq[txns[j-1]]; j-- {
			txns[j], txns[j-1] = txns[j-1], txns[j]
		}
	}
	return txns
}

// Initial returns the initial value of a key.
func (h *History) Initial(k Key) Value { return h.initial[k] }
