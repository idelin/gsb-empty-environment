package mvsg

// maxVerifyTxns bounds the serial-execution enumeration: 9! = 362880
// orders, each simulated in linear time. Larger cycles are reported without
// the cross-check.
const maxVerifyTxns = 9

// verifyCycle cross-checks a dependency cycle by enumerating every serial
// execution of the involved transactions. It reports verified=true when no
// serial order reproduces the observed reads and the committed version
// order, confirming the cycle as a genuine non-serializable witness.
//
// A serial order is consistent when:
//
//   - every read observes the version the serial execution would produce
//     (reads of versions written outside the cycle are left unconstrained,
//     because external writers may be serialized anywhere), and
//   - for every key, the last in-cycle writer equals the last in-cycle
//     writer in committed version order.
func verifyCycle(g *Graph, txns map[TxnID]*Transaction, cycle []TxnID) (verified bool, checked int) {
	n := len(cycle)
	if n < 2 || n > maxVerifyTxns {
		return false, 0
	}
	inSet := map[TxnID]bool{}
	for _, id := range cycle {
		inSet[id] = true
	}
	rank := map[TxnID]int{}
	for i, id := range g.Txns {
		rank[id] = i
	}

	// Last in-cycle writer of every key in committed version order, and the
	// write set of every cycle member.
	topWriter := map[string]TxnID{}
	writesKey := map[TxnID]map[string]bool{}
	for _, id := range cycle {
		writesKey[id] = map[string]bool{}
		for _, op := range txns[id].Ops {
			if op.Kind != OpWrite {
				continue
			}
			writesKey[id][op.Key] = true
			if cur, ok := topWriter[op.Key]; !ok || rank[id] > rank[cur] {
				topWriter[op.Key] = id
			}
		}
	}

	consistent := func(perm []TxnID) bool {
		state := map[string]Version{}
		for _, id := range perm {
			for _, op := range txns[id].Ops {
				if op.Kind == OpWrite {
					state[op.Key] = Version{Key: op.Key, Txn: id}
					continue
				}
				v := op.Version
				if v.Key == "" {
					v.Key = op.Key
				}
				if v.Txn != "" && !inSet[v.Txn] {
					continue // external version: unconstrained
				}
				cur, ok := state[op.Key]
				if !ok {
					cur = Version{Key: op.Key}
				}
				if cur != v {
					return false
				}
			}
		}
		for key, top := range topWriter {
			var last TxnID
			for _, id := range perm {
				if writesKey[id][key] {
					last = id
				}
			}
			if last != top {
				return false
			}
		}
		return true
	}

	perm := make([]TxnID, n)
	copy(perm, cycle)
	var enumerate func(k int) bool
	enumerate = func(k int) bool {
		if k == n {
			checked++
			return consistent(perm)
		}
		for i := k; i < n; i++ {
			perm[k], perm[i] = perm[i], perm[k]
			if enumerate(k + 1) {
				return true
			}
			perm[k], perm[i] = perm[i], perm[k]
		}
		return false
	}
	found := enumerate(0)
	return !found, checked
}
