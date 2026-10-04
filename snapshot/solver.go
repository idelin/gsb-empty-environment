// Package snapshot solves the cross-partition consistent-cut problem.
//
// A transaction may commit in several partitions. A read snapshot (cut) picks
// a prefix of every partition's version log. The cut is *consistent* iff every
// transaction is either fully included (all of its commits are in the cut) or
// fully excluded. Given a per-partition watermark, Solve returns the greatest
// consistent cut not later than the watermark, together with the reason chain
// of every transaction that had to be rolled back.
//
// Transaction associations may form cycles (t1: A,B  t2: B,C  t3: C,A).
// Rollback still terminates — see the proof in README.md and Solve's comments.
package snapshot

import "sort"

// LogEntry is one commit record inside a partition log.
type LogEntry struct {
	Version int64 // commit version, strictly increasing inside one partition
	Txn     int   // id of the transaction that produced this commit
}

// Input is the solver input: per-partition version logs plus watermarks.
// Logs[p] must be sorted by Version ascending. Watermark[p] is the highest
// version the snapshot may include in partition p. A single global watermark
// is the special case where all entries of Watermark are equal.
type Input struct {
	Logs      [][]LogEntry
	Watermark []int64
}

// ReasonKind classifies why a transaction was rolled back.
type ReasonKind int

const (
	// Watermark means the transaction straddled the watermark in the initial
	// cut: some of its commits were beyond Watermark[p] while others were
	// already included. It is a root cause of a rollback chain.
	Watermark ReasonKind = iota
	// Cascade means the transaction lost one of its already-included commits
	// because another transaction's rollback lowered the cut of that
	// partition below the commit's version.
	Cascade
)

// Reason explains one rollback step.
type Reason struct {
	Kind ReasonKind
	// CauseTxn is the transaction whose rollback forced this one out.
	// Only meaningful when Kind == Cascade.
	CauseTxn int
	// Partition and Version locate the decisive commit:
	//  - Watermark: a commit of the transaction itself that lay beyond the
	//    watermark (the "missing half").
	//  - Cascade: the commit of this transaction that was evicted when
	//    CauseTxn's rollback lowered the cut of Partition.
	Partition int
	Version   int64
}

// Rollback records one transaction that was (partially) included by the
// watermark cut but is absent from the final consistent cut.
type Rollback struct {
	Txn    int
	Reason Reason
}

// Result is the solver output.
type Result struct {
	// Cut[p] is the highest included version of partition p
	// (0 if the partition contributes nothing).
	Cut []int64
	// Rollbacks lists every rolled-back transaction with its direct reason,
	// ordered by transaction id.
	Rollbacks []Rollback

	reasons map[int]Reason // external txn id -> direct reason
}

// Solve computes the greatest consistent cut not later than the watermark.
//
// Algorithm (worklist / greatest fixed point):
//  1. Start from the watermark cut: cut[p] = last entry with Version <= W[p].
//  2. Any transaction that is partially included must be fully excluded:
//     lower the cut of every partition where it still has an included commit
//     to just below that commit.
//  3. Lowering a cut evicts further entries; transactions that thereby become
//     partially included are enqueued and handled the same way.
//  4. Repeat until no partial transaction remains.
//
// Termination (cycles included): let mu = sum_p (cut[p] + 1), the number of
// included entries. Every productive iteration lowers some cut[p], so mu
// strictly decreases; mu >= 0 bounds it below. Equivalently the cut vector
// descends strictly in the well-founded product order on N^P, which admits no
// infinite descending chains. Cuts never rise, so a rollback cascade can
// travel around an association cycle at most once per entry — no oscillation.
//
// Complexity: N = total log entries, T = transactions, P = partitions.
// Every entry is evicted at most once (cuts only move down) and every
// transaction is enqueued and processed at most once (its included-count only
// decreases), giving O(N + P log N) time and O(N + T) space.
func Solve(in Input) Result {
	parts := len(in.Logs)

	// Derive transaction -> commits from the logs. commits[t] holds the
	// (partition, index) pairs of every commit of transaction t. t is an
	// internal dense index; ids[t] is the external transaction id.
	idSet := map[int]bool{}
	for p := 0; p < parts; p++ {
		for _, e := range in.Logs[p] {
			idSet[e.Txn] = true
		}
	}
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	internal := make(map[int]int, len(ids))
	for i, id := range ids {
		internal[id] = i
	}
	numTxns := len(ids)

	type commitRef struct{ part, idx int }
	commits := make([][]commitRef, numTxns)
	for p := 0; p < parts; p++ {
		for i, e := range in.Logs[p] {
			t := internal[e.Txn]
			commits[t] = append(commits[t], commitRef{p, i})
		}
	}

	// Step 1: initial cut from the watermark (binary search per partition).
	cut := make([]int, parts) // index of last included entry, -1 = none
	for p := 0; p < parts; p++ {
		var w int64
		if p < len(in.Watermark) {
			w = in.Watermark[p]
		}
		cut[p] = sort.Search(len(in.Logs[p]), func(i int) bool {
			return in.Logs[p][i].Version > w
		}) - 1
	}

	// included[t] = number of t's commits currently inside the cut.
	included := make([]int, numTxns)
	for t := 0; t < numTxns; t++ {
		for _, c := range commits[t] {
			if c.idx <= cut[c.part] {
				included[t]++
			}
		}
	}

	// Steps 2-4: worklist of partially included transactions.
	queued := make([]bool, numTxns)
	rolledBack := make([]bool, numTxns)
	reasons := make(map[int]Reason, numTxns)
	var queue []int

	enqueue := func(t int, r Reason) {
		if queued[t] {
			return // first reason wins; a txn turns partial at most once
		}
		queued[t] = true
		reasons[ids[t]] = r
		queue = append(queue, t)
	}
	for t := 0; t < numTxns; t++ {
		if included[t] > 0 && included[t] < len(commits[t]) {
			// Root cause: straddles the watermark. Point at a missing commit.
			r := Reason{Kind: Watermark, CauseTxn: -1, Partition: -1}
			for _, c := range commits[t] {
				if c.idx > cut[c.part] {
					r.Partition = c.part
					r.Version = in.Logs[c.part][c.idx].Version
					break
				}
			}
			enqueue(t, r)
		}
	}

	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		if included[t] == 0 {
			continue // already fully evicted by an earlier cascade
		}
		rolledBack[t] = true
		// Fully exclude t: drop every still-included commit of t.
		for _, c := range commits[t] {
			if c.idx > cut[c.part] {
				continue // already evicted
			}
			p := c.part
			// Evict entries c.idx..cut[p]. Each entry is evicted at most
			// once over the whole run, so the total cost here is O(N).
			for i := cut[p]; i >= c.idx; i-- {
				victim := internal[in.Logs[p][i].Txn]
				included[victim]--
				if included[victim] > 0 && included[victim] < len(commits[victim]) {
					enqueue(victim, Reason{
						Kind:      Cascade,
						CauseTxn:  ids[t],
						Partition: p,
						Version:   in.Logs[p][i].Version,
					})
				}
			}
			cut[p] = c.idx - 1
		}
	}

	res := Result{Cut: make([]int64, parts), reasons: reasons}
	for p := 0; p < parts; p++ {
		if cut[p] >= 0 {
			res.Cut[p] = in.Logs[p][cut[p]].Version
		}
	}
	// Report every transaction that lost commits it initially had,
	// in deterministic (transaction id) order.
	for t := 0; t < numTxns; t++ {
		if rolledBack[t] || (queued[t] && included[t] == 0) {
			res.Rollbacks = append(res.Rollbacks, Rollback{Txn: ids[t], Reason: reasons[ids[t]]})
		}
	}
	return res
}

// Chain returns the full reason chain for a rolled-back transaction, from the
// transaction itself back to the root cause (a transaction that straddled the
// watermark). It returns nil if the transaction was not rolled back.
//
// The chain is always finite — even when transaction associations contain
// cycles — because a transaction's reason is fixed when it first turns
// partial, which happens strictly after its cause was dequeued. Discovery
// order strictly decreases along the chain, so it cannot loop; the reason
// graph is a forest rooted at watermark-straddling transactions.
func (r *Result) Chain(txn int) []Reason {
	if _, ok := r.reasons[txn]; !ok {
		return nil
	}
	var chain []Reason
	for cur := txn; ; {
		reason, ok := r.reasons[cur]
		if !ok {
			break
		}
		chain = append(chain, reason)
		if reason.Kind == Watermark {
			break
		}
		cur = reason.CauseTxn
	}
	return chain
}
