// Package bigtxn implements a large-transaction sharding transfer pipeline
// with a memory-protection limiter. A single transaction containing
// millions of row changes is split into phase-marked chunks that are
// streamed to a receiver; when the in-memory budget is exhausted, chunks
// spill to temporary disk files so the receiver never OOMs.
package bigtxn

import "io"

// Op is the kind of row change.
type Op byte

const (
	OpInsert Op = 'I'
	OpUpdate Op = 'U'
	OpDelete Op = 'D'
)

// Change is a single row-level change inside a transaction.
type Change struct {
	TxnID uint64
	Seq   uint64
	Op    Op
	Table string
	Key   int64
	Data  []byte
}

// Size returns an in-memory size estimate in bytes, including the
// overhead of slice headers and struct fields.
func (c *Change) Size() int64 {
	return int64(96 + len(c.Table) + len(c.Data))
}

// ChangeSource yields changes of one transaction in order.
// It returns io.EOF when the transaction is drained.
type ChangeSource interface {
	Next() (*Change, error)
}

// GeneratorSource synthesizes total changes on the fly. It is used by
// tests and the demo to emulate a source database producing a huge
// transaction without materializing it in memory.
type GeneratorSource struct {
	TxnID       uint64
	PayloadSize int

	total int
	seq   int
}

func NewGeneratorSource(txnID uint64, total, payloadSize int) *GeneratorSource {
	return &GeneratorSource{TxnID: txnID, PayloadSize: payloadSize, total: total}
}

func (g *GeneratorSource) Next() (*Change, error) {
	if g.seq >= g.total {
		return nil, io.EOF
	}
	g.seq++
	return &Change{
		TxnID: g.TxnID,
		Seq:   uint64(g.seq),
		Op:    OpUpdate,
		Table: "orders",
		Key:   int64(g.seq),
		Data:  make([]byte, g.PayloadSize),
	}, nil
}
