package sm

// Evidence is an auditable tie-break record emitted when a pending
// (unresolved) write-write conflict between concurrent transactions is
// decided. It is JSON-serializable so it can be shipped to an audit sink.
type Evidence struct {
	Key       string `json:"key"`
	Rule      string `json:"rule"`
	WinnerTxn string `json:"winner_txn"`
	LoserTxn  string `json:"loser_txn"`
	WinnerLSN uint64 `json:"winner_commit_lsn"`
	LoserLSN  uint64 `json:"loser_commit_lsn"`
	Value     string `json:"winning_value"`
	Reason    string `json:"reason"`
}

// RuleCommitLSNOrder is the deterministic tie-break rule: among conflicting
// concurrent transactions the one with the higher commit LSN wins the key.
const RuleCommitLSNOrder = "commit-lsn-order"
