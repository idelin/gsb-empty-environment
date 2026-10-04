package aries

import (
	"fmt"
	"sort"
)

// ViolationKind classifies invariant violations found by the Inspector.
type ViolationKind string

const (
	KindLSNOrder        ViolationKind = "LSN_ORDER"        // LSNs not strictly increasing
	KindDuplicateLSN    ViolationKind = "DUPLICATE_LSN"    // two records share an LSN
	KindDanglingRef     ViolationKind = "DANGLING_REF"     // pointer to a non-existent LSN
	KindCrossTxnRef     ViolationKind = "CROSS_TXN_REF"    // chain pointer crosses transactions
	KindOrphanLoop      ViolationKind = "ORPHAN_LOOP"      // cycle in prev/undoNext chains
	KindOrphanRecord    ViolationKind = "ORPHAN_RECORD"    // record unreachable from txn tail
	KindCheckpointRef   ViolationKind = "CHECKPOINT_REF"   // ATT/DPT snapshot inconsistency
	KindUnterminatedTxn ViolationKind = "UNTERMINATED_TXN" // txn without END (post-recovery)
	KindDataSkew        ViolationKind = "DATA_SKEW"        // disk pageLSN diverges from WAL
)

// Violation is a single invariant breach.
type Violation struct {
	Kind   ViolationKind
	Detail string
}

func (v Violation) String() string { return fmt.Sprintf("[%s] %s", v.Kind, v.Detail) }

// Report is the outcome of an inspection run.
type Report struct {
	Violations []Violation
}

// OK reports whether every invariant held.
func (r *Report) OK() bool { return len(r.Violations) == 0 }

func (r *Report) add(kind ViolationKind, format string, args ...interface{}) {
	r.Violations = append(r.Violations, Violation{Kind: kind, Detail: fmt.Sprintf(format, args...)})
}

// Summary renders the report as text.
func (r *Report) Summary() string {
	if r.OK() {
		return "INSPECTION PASSED: all invariants hold, no leaks"
	}
	out := fmt.Sprintf("INSPECTION FAILED: %d violation(s)\n", len(r.Violations))
	for _, v := range r.Violations {
		out += "  " + v.String() + "\n"
	}
	return out
}

// Inspector checks formal ARIES invariants over a WAL snapshot and,
// optionally, the on-disk page state. It is stateless and safe for
// concurrent use.
type Inspector struct{}

// InspectStructural validates invariants that must hold at any time,
// including while the system is live:
//  1. LSNs are strictly increasing and unique.
//  2. PrevLSN chains: existing, same-transaction, strictly decreasing,
//     cycle-free, and covering every record of the transaction.
//  3. CLR undoNextLSN chains: existing, same-transaction, strictly
//     decreasing, cycle-free, pointing at UPDATE/CLR records.
//  4. Checkpoint payloads reference real, consistent LSNs.
func (Inspector) InspectStructural(records []*LogRecord) *Report {
	rep := &Report{}
	byLSN := checkLSNOrdering(records, rep)
	checkTxnChains(records, byLSN, rep)
	checkCLRChains(records, byLSN, rep)
	checkCheckpoints(records, byLSN, rep)
	return rep
}

// InspectPostRecovery additionally asserts invariants that must hold after a
// complete recovery run:
//  5. Every transaction is terminated by an END record (no leaks).
//  6. No data skew: each page's on-disk pageLSN equals the maximum WAL LSN
//     affecting that page.
func (in Inspector) InspectPostRecovery(records []*LogRecord, disk *DiskManager) *Report {
	rep := in.InspectStructural(records)
	checkTermination(records, rep)
	if disk != nil {
		checkDataSkew(records, disk, rep)
	}
	return rep
}

func checkLSNOrdering(records []*LogRecord, rep *Report) map[uint64]*LogRecord {
	byLSN := make(map[uint64]*LogRecord, len(records))
	var prev uint64
	for i, rec := range records {
		if i > 0 && rec.LSN <= prev {
			rep.add(KindLSNOrder, "record %d has LSN %d not greater than previous LSN %d", i, rec.LSN, prev)
		}
		if _, dup := byLSN[rec.LSN]; dup {
			rep.add(KindDuplicateLSN, "LSN %d appears more than once", rec.LSN)
		}
		byLSN[rec.LSN] = rec
		prev = rec.LSN
	}
	return byLSN
}

// checkTxnChains walks each transaction's PrevLSN chain from its tail
// record and asserts reachability, monotonic decrease and acyclicity.
func checkTxnChains(records []*LogRecord, byLSN map[uint64]*LogRecord, rep *Report) {
	byTxn := make(map[uint64][]*LogRecord)
	for _, rec := range records {
		if rec.Type == LogCheckpoint {
			continue // checkpoints carry no transaction
		}
		byTxn[rec.TxnID] = append(byTxn[rec.TxnID], rec)
	}
	for txnID, recs := range byTxn {
		tail := recs[0]
		for _, r := range recs {
			if r.LSN > tail.LSN {
				tail = r
			}
		}
		visited := make(map[uint64]bool)
		cur := tail
		for cur != nil {
			if visited[cur.LSN] {
				rep.add(KindOrphanLoop, "txn %d: prevLSN chain cycles back to LSN %d", txnID, cur.LSN)
				break
			}
			visited[cur.LSN] = true
			if cur.PrevLSN == 0 {
				break
			}
			next, ok := byLSN[cur.PrevLSN]
			if !ok {
				rep.add(KindDanglingRef, "txn %d: record LSN %d has dangling prevLSN %d", txnID, cur.LSN, cur.PrevLSN)
				break
			}
			if next.TxnID != txnID {
				rep.add(KindCrossTxnRef, "txn %d: record LSN %d prevLSN %d points into txn %d", txnID, cur.LSN, cur.PrevLSN, next.TxnID)
				break
			}
			if next.LSN >= cur.LSN {
				rep.add(KindOrphanLoop, "txn %d: prevLSN chain not decreasing at LSN %d -> %d", txnID, cur.LSN, next.LSN)
				break
			}
			cur = next
		}
		for _, r := range recs {
			if !visited[r.LSN] {
				rep.add(KindOrphanRecord, "txn %d: record LSN %d (%s) unreachable from txn tail LSN %d", txnID, r.LSN, r.Type, tail.LSN)
			}
		}
	}
}

// checkCLRChains validates undoNextLSN pointers and hunts for orphan loops.
func checkCLRChains(records []*LogRecord, byLSN map[uint64]*LogRecord, rep *Report) {
	for _, rec := range records {
		if rec.Type != LogCLR {
			continue
		}
		if rec.UndoNextLSN == 0 {
			continue
		}
		target, ok := byLSN[rec.UndoNextLSN]
		if !ok {
			rep.add(KindDanglingRef, "CLR LSN %d (txn %d) has dangling undoNextLSN %d", rec.LSN, rec.TxnID, rec.UndoNextLSN)
			continue
		}
		if target.TxnID != rec.TxnID {
			rep.add(KindCrossTxnRef, "CLR LSN %d (txn %d) undoNextLSN %d points into txn %d", rec.LSN, rec.TxnID, rec.UndoNextLSN, target.TxnID)
		}
		if target.LSN >= rec.LSN {
			rep.add(KindOrphanLoop, "CLR LSN %d undoNextLSN %d does not point backwards", rec.LSN, rec.UndoNextLSN)
		}
		if target.Type != LogUpdate && target.Type != LogCLR {
			rep.add(KindDanglingRef, "CLR LSN %d undoNextLSN %d points at a %s record", rec.LSN, rec.UndoNextLSN, target.Type)
		}
		// Cycle detection along the undoNext chain.
		seen := map[uint64]bool{rec.LSN: true}
		for cur := target; cur != nil && cur.Type == LogCLR && cur.UndoNextLSN != 0; {
			if seen[cur.UndoNextLSN] {
				rep.add(KindOrphanLoop, "undoNext chain starting at CLR LSN %d cycles at LSN %d", rec.LSN, cur.UndoNextLSN)
				break
			}
			seen[cur.UndoNextLSN] = true
			cur = byLSN[cur.UndoNextLSN]
		}
	}
}

// checkCheckpoints validates ATT/DPT snapshots embedded in checkpoints.
func checkCheckpoints(records []*LogRecord, byLSN map[uint64]*LogRecord, rep *Report) {
	for _, rec := range records {
		if rec.Type != LogCheckpoint || rec.Checkpoint == nil {
			continue
		}
		cp := rec.Checkpoint
		for txnID, tr := range cp.Transactions {
			if tr.TxnID != txnID {
				rep.add(KindCheckpointRef, "checkpoint LSN %d: ATT key %d holds entry for txn %d", rec.LSN, txnID, tr.TxnID)
			}
			if tr.LastLSN == 0 {
				continue
			}
			target, ok := byLSN[tr.LastLSN]
			if !ok {
				rep.add(KindDanglingRef, "checkpoint LSN %d: ATT txn %d lastLSN %d does not exist", rec.LSN, txnID, tr.LastLSN)
				continue
			}
			if target.TxnID != txnID {
				rep.add(KindCheckpointRef, "checkpoint LSN %d: ATT txn %d lastLSN %d belongs to txn %d", rec.LSN, txnID, tr.LastLSN, target.TxnID)
			}
			if target.LSN >= rec.LSN {
				rep.add(KindCheckpointRef, "checkpoint LSN %d: ATT txn %d lastLSN %d is not in the past", rec.LSN, txnID, tr.LastLSN)
			}
		}
		for pageID, recLSN := range cp.DirtyPages {
			target, ok := byLSN[recLSN]
			if !ok {
				rep.add(KindDanglingRef, "checkpoint LSN %d: DPT page %d recLSN %d does not exist", rec.LSN, pageID, recLSN)
				continue
			}
			if target.PageID != pageID || (target.Type != LogUpdate && target.Type != LogCLR) {
				rep.add(KindCheckpointRef, "checkpoint LSN %d: DPT page %d recLSN %d refers to %s on page %d", rec.LSN, pageID, recLSN, target.Type, target.PageID)
			}
			if target.LSN >= rec.LSN {
				rep.add(KindCheckpointRef, "checkpoint LSN %d: DPT page %d recLSN %d is not in the past", rec.LSN, pageID, recLSN)
			}
		}
	}
}

// checkTermination asserts every transaction ends with an END record.
func checkTermination(records []*LogRecord, rep *Report) {
	ended := make(map[uint64]bool)
	seen := make(map[uint64]bool)
	for _, rec := range records {
		switch rec.Type {
		case LogEnd:
			ended[rec.TxnID] = true
		case LogCheckpoint:
		default:
			seen[rec.TxnID] = true
		}
	}
	var dangling []uint64
	for txnID := range seen {
		if !ended[txnID] {
			dangling = append(dangling, txnID)
		}
	}
	sort.Slice(dangling, func(i, j int) bool { return dangling[i] < dangling[j] })
	for _, txnID := range dangling {
		rep.add(KindUnterminatedTxn, "txn %d has no END record (leaked transaction)", txnID)
	}
}

// checkDataSkew compares each page's on-disk pageLSN with the WAL frontier.
func checkDataSkew(records []*LogRecord, disk *DiskManager, rep *Report) {
	maxLSN := make(map[uint64]uint64)
	for _, rec := range records {
		if rec.Type != LogUpdate && rec.Type != LogCLR {
			continue
		}
		if rec.LSN > maxLSN[rec.PageID] {
			maxLSN[rec.PageID] = rec.LSN
		}
	}
	pages := make([]uint64, 0, len(maxLSN))
	for id := range maxLSN {
		pages = append(pages, id)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	for _, id := range pages {
		page, err := disk.ReadPage(id)
		if err != nil {
			rep.add(KindDataSkew, "page %d: unreadable: %v", id, err)
			continue
		}
		want := maxLSN[id]
		if page.LSN < want {
			rep.add(KindDataSkew, "page %d: stale on disk, pageLSN %d < WAL frontier %d (lost update)", id, page.LSN, want)
		} else if page.LSN > want {
			rep.add(KindDataSkew, "page %d: pageLSN %d beyond WAL frontier %d (phantom write)", id, page.LSN, want)
		}
	}
}
