package kvsm

// Kind is the type of a log event.
type Kind int

const (
	KindPrepare Kind = iota + 1
	KindCommit
	KindAbort
)

func (k Kind) String() string {
	switch k {
	case KindPrepare:
		return "PREPARE"
	case KindCommit:
		return "COMMIT"
	case KindAbort:
		return "ABORT"
	}
	return "UNKNOWN"
}

// Event is a single entry in the deterministic log. Seq is assigned by the
// log at append time and defines the global deterministic order.
type Event struct {
	Seq    uint64
	Kind   Kind
	TxnID  string
	Parts  []int                     // partitions involved
	Writes map[int]map[string]string // part -> key -> value (Prepare only)
}

// PendingTxn is a prepared-but-not-yet-committed transaction.
type PendingTxn struct {
	ID         string
	PrepareSeq uint64
	Parts      []int
	Writes     map[int]map[string]string
}

// conflictsWith reports whether two pending txns write the same key on the
// same partition, returning one conflicting (part, key) pair as evidence.
func (p *PendingTxn) conflictsWith(other *PendingTxn) (int, string, bool) {
	for part, kvs := range p.Writes {
		otherKVs, ok := other.Writes[part]
		if !ok {
			continue
		}
		for key := range kvs {
			if _, hit := otherKVs[key]; hit {
				return part, key, true
			}
		}
	}
	return 0, "", false
}

// Evidence is an auditable record emitted whenever the engine resolves a
// conflict between concurrent transactions. It captures the rule applied and
// the inputs to the decision so the outcome can be replayed and verified.
type Evidence struct {
	Rule      string // deterministic tie-break rule identifier
	Winner    string // txn whose writes take effect later (wins the key)
	Loser     string // txn whose writes are overwritten / discarded
	Part      int
	Key       string
	WinnerSeq uint64 // prepare seq of winner
	LoserSeq  uint64 // prepare seq of loser
	Action    string // "defer-commit", "apply-after-resolve", "abort-loser"
	Detail    string
}
