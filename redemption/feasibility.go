package redemption

import (
	"fmt"
	"sort"
)

// Feasible reports whether the candidate subset encoded by mask can be
// accepted together with the confirmed prefix, and returns the reason when
// it cannot. The check is self-contained and reused by both the brute-force
// oracle and the certificate verifier.
func (l *Ledger) Feasible(mask uint32) (bool, error) {
	n := l.N()
	// (1) No candidate conflicting with the confirmed prefix.
	for i := 0; i < n; i++ {
		if mask&(1<<uint(i)) != 0 && l.candidates[i].blocked {
			return false, fmt.Errorf("%w: candidate %q: %v",
				ErrSpentInPrefix, l.candidates[i].tx.ID, l.candidates[i].blockReason)
		}
	}

	// (2) Dependency closure: every selected change input needs its
	// creating transaction selected as well.
	for i := 0; i < n; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		if missing := l.candidates[i].predMask &^ mask; missing != 0 {
			return false, fmt.Errorf("%w: %q needs unselected predecessors",
				ErrMissingDependency, l.candidates[i].tx.ID)
		}
	}

	// (3) Acyclicity: a cycle would let transactions self-fund their change
	// without any rooted path back to issuer notes.
	if l.hasCycle(mask) {
		return false, fmt.Errorf("%w: selected dependency graph contains a cycle", ErrCycle)
	}

	// (4) Each nullifier and each change outpoint is spent at most once.
	seenNull := make(map[Nullifier]int)
	seenOut := make(map[Outpoint]int)
	for i := 0; i < n; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		for _, nf := range l.nullifierUse[i] {
			if l.prefixNullifiers[nf] {
				return false, fmt.Errorf("%w: nullifier %q already spent in prefix", ErrSpentInPrefix, nf)
			}
			if prev, ok := seenNull[nf]; ok {
				return false, fmt.Errorf("%w: nullifier %q shared by %q and %q",
					ErrDoubleSpend, nf, l.candidates[prev].tx.ID, l.candidates[i].tx.ID)
			}
			seenNull[nf] = i
		}
		for _, op := range l.outpointUse[i] {
			if l.prefixOutpoints[op] {
				return false, fmt.Errorf("%w: outpoint %v already spent in prefix", ErrSpentInPrefix, op)
			}
			if prev, ok := seenOut[op]; ok {
				return false, fmt.Errorf("%w: outpoint %v shared by %q and %q",
					ErrDoubleSpend, op, l.candidates[prev].tx.ID, l.candidates[i].tx.ID)
			}
			seenOut[op] = i
		}
	}

	// (5) Remaining per-issuer/activity quotas. Value never crosses keys.
	used := make(map[BalanceKey]int64)
	for i := 0; i < n; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		for key, cost := range l.spendByKey[i] {
			used[key] += cost
			if used[key] > l.quotas[key] {
				return false, fmt.Errorf("%w: %s/%s used %d > limit %d",
					ErrQuotaExceeded, key.Issuer, key.Activity, used[key], l.quotas[key])
			}
		}
	}
	return true, nil
}

// hasCycle runs Kahn's algorithm on the dependency graph induced by mask.
func (l *Ledger) hasCycle(mask uint32) bool {
	n := l.N()
	indeg := make([]int, n)
	alive := 0
	for i := 0; i < n; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		alive++
		for _, pred := range l.candidates[i].preds {
			if mask&(1<<uint(pred)) != 0 {
				indeg[i]++
			}
		}
	}
	var queue []int
	for i := 0; i < n; i++ {
		if mask&(1<<uint(i)) != 0 && indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for j := 0; j < n; j++ {
			if mask&(1<<uint(j)) == 0 {
				continue
			}
			for _, pred := range l.candidates[j].preds {
				if pred == cur {
					indeg[j]--
					if indeg[j] == 0 {
						queue = append(queue, j)
					}
				}
			}
		}
	}
	return visited != alive
}

// Entitlements sums the completed service units of the selected candidates.
func (l *Ledger) Entitlements(mask uint32) int64 {
	var total int64
	for i := 0; i < l.N(); i++ {
		if mask&(1<<uint(i)) != 0 {
			total += l.candidates[i].entitlements
		}
	}
	return total
}

// SelectedIDs returns the ascending transaction-ID sequence of the selected
// candidates.
func (l *Ledger) SelectedIDs(mask uint32) []TxID {
	var ids []TxID
	for i := 0; i < l.N(); i++ {
		if mask&(1<<uint(i)) != 0 {
			ids = append(ids, l.candidates[i].tx.ID)
		}
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	return ids
}

// MaskFromIDs converts a transaction-ID selection back into a bit mask.
func (l *Ledger) MaskFromIDs(ids []TxID) (uint32, error) {
	var mask uint32
	for _, id := range ids {
		idx, ok := l.byID[id]
		if !ok {
			return 0, fmt.Errorf("%w: unknown candidate %q", ErrMalformed, id)
		}
		mask |= 1 << uint(idx)
	}
	return mask, nil
}
