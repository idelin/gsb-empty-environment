package sm

// EventKind is the type of a deterministic log record.
type EventKind uint8

const (
	KindPrepare EventKind = iota + 1
	KindCommit
	KindAbort
)

func (k EventKind) String() string {
	switch k {
	case KindPrepare:
		return "prepare"
	case KindCommit:
		return "commit"
	case KindAbort:
		return "abort"
	}
	return "unknown"
}

// Event is a single record of the deterministic local log.
//
// LSN is assigned by the single deterministic sequencer at log append time.
// Because every replica applies events strictly in LSN order (the reorder
// buffer absorbs out-of-order network delivery), all replicas converge to
// the same state regardless of packet interleaving.
type Event struct {
	LSN    uint64            `json:"lsn"`
	TxnID  string            `json:"txn_id"`
	Kind   EventKind         `json:"kind"`
	Writes map[string]string `json:"writes,omitempty"` // prepare: write intents
	Reads  []string          `json:"reads,omitempty"`  // prepare: read set (for dependency tracking)
}
