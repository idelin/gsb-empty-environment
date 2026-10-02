package ss2pl

import "fmt"

// VerifyCommitOrderEquivalence checks that the executed schedule is
// equivalent to the serial schedule defined by the commit-time timestamps
// of the transactions: for every pair of conflicting operations on the
// same key (at least one of them a write) issued by different committed
// transactions, the operation order must agree with the commit order.
//
// Strict 2PL guarantees this by construction — a conflicting lock can only
// be obtained after the previous holder committed and released it — but
// this function verifies it empirically from the operation and commit logs.
func (tm *TxnManager) VerifyCommitOrderEquivalence() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	commitOf := make(map[int]int64, len(tm.commitLog))
	for _, c := range tm.commitLog {
		commitOf[c.TxnID] = c.Seq
	}

	type keyState struct {
		lastWriter int
		readers    map[int]bool // readers since the last write
	}
	states := make(map[string]*keyState)

	for _, op := range tm.opLog {
		committer, ok := commitOf[op.TxnID]
		if !ok {
			continue // aborted transactions are not part of the schedule
		}
		st := states[op.Key]
		if st == nil {
			st = &keyState{lastWriter: -1, readers: make(map[int]bool)}
			states[op.Key] = st
		}
		switch op.Op {
		case 'R':
			if st.lastWriter >= 0 && st.lastWriter != op.TxnID && commitOf[st.lastWriter] >= committer {
				return fmt.Errorf("ss2pl: txn %d read %q (commit seq %d) before writer txn %d committed (seq %d)",
					op.TxnID, op.Key, committer, st.lastWriter, commitOf[st.lastWriter])
			}
			st.readers[op.TxnID] = true
		case 'W':
			if st.lastWriter >= 0 && st.lastWriter != op.TxnID && commitOf[st.lastWriter] >= committer {
				return fmt.Errorf("ss2pl: txn %d wrote %q (commit seq %d) before earlier writer txn %d committed (seq %d)",
					op.TxnID, op.Key, committer, st.lastWriter, commitOf[st.lastWriter])
			}
			for r := range st.readers {
				if r != op.TxnID && commitOf[r] >= committer {
					return fmt.Errorf("ss2pl: txn %d wrote %q (commit seq %d) before earlier reader txn %d committed (seq %d)",
						op.TxnID, op.Key, committer, r, commitOf[r])
				}
			}
			st.lastWriter = op.TxnID
			st.readers = make(map[int]bool)
		}
	}
	return nil
}
