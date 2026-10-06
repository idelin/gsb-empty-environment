// Package mvsg builds a multi-version serialization graph (MVSG) from an
// observed transaction history and reports serialization anomalies.
//
// The graph is built from the exact version each read observed and from the
// write sets of committed transactions only. Dependencies are version-precise:
// merely touching the same key is never treated as a conflict. Three edge
// kinds are distinguished:
//
//	WR  write-read:      a transaction read the version another wrote
//	RW  read-write anti-dependency: a transaction read a version that a
//	    later transaction overwrote (the immediately following version)
//	WW  write-write:     version order between two committed writers
//
// Cycles in the graph are reported together with the operations that witness
// every edge, and each cycle is cross-checked by enumerating all serial
// executions of the involved transactions.
package mvsg

// TxnID identifies a transaction.
type TxnID string

// OpKind is the kind of an Operation.
type OpKind int

const (
	OpRead OpKind = iota
	OpWrite
)

// Version identifies one version of a key. The zero Txn denotes the initial
// version that exists before any transaction runs.
type Version struct {
	Key string
	Txn TxnID // writer of the version; "" means the initial version
}

// InitialVersion returns the initial version of key.
func InitialVersion(key string) Version { return Version{Key: key} }

func (v Version) String() string {
	if v.Txn == "" {
		return v.Key + "@init"
	}
	return v.Key + "@" + string(v.Txn)
}

// Operation is a single read or write inside a transaction.
//
// For writes the produced version is derived from the key and the owning
// transaction, so Version may be left zero. For reads Version must name the
// exact version that was observed; use Read / ReadInitial to build read ops.
type Operation struct {
	Kind    OpKind
	Key     string
	Version Version
	Value   string
}

// Write returns a write operation. The produced version is inferred as
// key@<owning transaction>.
func Write(key, value string) Operation {
	return Operation{Kind: OpWrite, Key: key, Value: value}
}

// Read returns a read operation observing the version written by from.
func Read(key string, from TxnID, value string) Operation {
	return Operation{Kind: OpRead, Key: key, Version: Version{Key: key, Txn: from}, Value: value}
}

// ReadInitial returns a read operation observing the initial version.
func ReadInitial(key, value string) Operation {
	return Operation{Kind: OpRead, Key: key, Version: InitialVersion(key), Value: value}
}

// Transaction is a recorded transaction. Uncommitted transactions are
// ignored by the analysis.
type Transaction struct {
	ID        TxnID
	Ops       []Operation
	Committed bool
}

// History is the observed execution. Committed transactions must appear in
// commit order; it defines the version order of every key.
type History struct {
	Txns []*Transaction
}
