// Package inspect is an offline integrity inspector for ARIES state:
// it re-derives every invariant from the raw WAL, buffer pool and
// disk image independently of the recovery engine, and reports
// violations such as dangling references, orphan loops and
// buffer/disk skew.
package inspect

import (
	"fmt"
	"sort"

	"aries/recovery"
	"aries/storage"
	"aries/wal"
)

// Kind classifies an invariant violation.
type Kind string

const (
	KindDanglingRef   Kind = "dangling-reference"
	KindOrphanLoop    Kind = "orphan-loop"
	KindCLRLink       Kind = "clr-link-violation"
	KindDPTMismatch   Kind = "dpt-inconsistency"
	KindCommittedLost Kind = "committed-data-lost"
	KindUncompensated Kind = "uncompensated-update"
	KindSkew          Kind = "buffer-disk-skew"
)

// Violation describes one broken invariant.
type Violation struct {
	Kind   Kind
	Detail string
}

func (v Violation) String() string { return fmt.Sprintf("[%s] %s", v.Kind, v.Detail) }

// Inspector audits a log, buffer pool and disk store for consistency.
type Inspector struct {
	Log  *wal.LogManager
	Pool *storage.BufferPool
	Disk *storage.DiskStore
}

// NewInspector creates an Inspector.
func NewInspector(log *wal.LogManager, pool *storage.BufferPool, disk *storage.DiskStore) *Inspector {
	return &Inspector{Log: log, Pool: pool, Disk: disk}
}

func (in *Inspector) records() (map[uint64]*wal.Record, []*wal.Record, error) {
	byLSN := make(map[uint64]*wal.Record)
	var all []*wal.Record
	err := in.Log.ScanFrom(0, func(rec *wal.Record) error {
		byLSN[rec.LSN] = rec
		all = append(all, rec)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return byLSN, all, nil
}

// CheckAll runs every invariant check and aggregates the violations.
func (in *Inspector) CheckAll(a *recovery.Analysis) ([]Violation, error) {
	var out []Violation
	checks := []func() ([]Violation, error){
		in.CheckChains,
		in.CheckCLRLinks,
		in.CheckAcyclic,
		func() ([]Violation, error) { return in.CheckDPT(a) },
		in.CheckPersistence,
		in.CheckSkew,
	}
	for _, check := range checks {
		violations, err := check()
		if err != nil {
			return nil, err
		}
		out = append(out, violations...)
	}
	return out, nil
}

// AssertClean runs CheckAll and panics if any invariant is broken.
// It is the assertion instrument intended for tests and demos.
func (in *Inspector) AssertClean(a *recovery.Analysis) {
	violations, err := in.CheckAll(a)
	if err != nil {
		panic(fmt.Sprintf("inspect: check failed: %v", err))
	}
	if len(violations) > 0 {
		panic(fmt.Sprintf("inspect: %d invariant violation(s): %v", len(violations), violations))
	}
}

// CheckChains verifies referential integrity of the per-transaction
// PrevLSN chains and of the ATT/DPT tables embedded in checkpoints.
func (in *Inspector) CheckChains() ([]Violation, error) {
	byLSN, all, err := in.records()
	if err != nil {
		return nil, err
	}
	var out []Violation
	for _, rec := range all {
		if rec.PrevLSN != 0 {
			prev, ok := byLSN[rec.PrevLSN]
			switch {
			case !ok:
				out = append(out, Violation{KindDanglingRef,
					fmt.Sprintf("LSN %d (txn %d) references missing PrevLSN %d", rec.LSN, rec.TxnID, rec.PrevLSN)})
			case prev.TxnID != rec.TxnID:
				out = append(out, Violation{KindDanglingRef,
					fmt.Sprintf("LSN %d (txn %d) PrevLSN %d belongs to txn %d", rec.LSN, rec.TxnID, rec.PrevLSN, prev.TxnID)})
			case prev.LSN >= rec.LSN:
				out = append(out, Violation{KindDanglingRef,
					fmt.Sprintf("LSN %d PrevLSN %d is not strictly earlier", rec.LSN, rec.PrevLSN)})
			}
		}
		if rec.Type == wal.TypeCheckpoint {
			for txn, last := range rec.ATT {
				tgt, ok := byLSN[last]
				switch {
				case !ok:
					out = append(out, Violation{KindDanglingRef,
						fmt.Sprintf("checkpoint LSN %d: ATT txn %d lastLSN %d missing from log", rec.LSN, txn, last)})
				case tgt.TxnID != txn:
					out = append(out, Violation{KindDanglingRef,
						fmt.Sprintf("checkpoint LSN %d: ATT txn %d lastLSN %d belongs to txn %d", rec.LSN, txn, last, tgt.TxnID)})
				}
			}
			for page, recLSN := range rec.DPT {
				tgt, ok := byLSN[recLSN]
				switch {
				case !ok:
					out = append(out, Violation{KindDanglingRef,
						fmt.Sprintf("checkpoint LSN %d: DPT page %d recLSN %d missing from log", rec.LSN, page, recLSN)})
				case tgt.PageID != page || (tgt.Type != wal.TypeUpdate && tgt.Type != wal.TypeCLR):
					out = append(out, Violation{KindDanglingRef,
						fmt.Sprintf("checkpoint LSN %d: DPT page %d recLSN %d is %s on page %d", rec.LSN, page, recLSN, tgt.Type, tgt.PageID)})
				}
			}
		}
	}
	return out, nil
}

// CheckCLRLinks verifies that every CLR's undoNext pointer references
// an earlier record of the same transaction.
func (in *Inspector) CheckCLRLinks() ([]Violation, error) {
	byLSN, all, err := in.records()
	if err != nil {
		return nil, err
	}
	var out []Violation
	for _, rec := range all {
		if rec.Type != wal.TypeCLR || rec.UndoNext == 0 {
			continue
		}
		tgt, ok := byLSN[rec.UndoNext]
		switch {
		case !ok:
			out = append(out, Violation{KindCLRLink,
				fmt.Sprintf("CLR LSN %d (txn %d) undoNext %d missing from log", rec.LSN, rec.TxnID, rec.UndoNext)})
		case tgt.TxnID != rec.TxnID:
			out = append(out, Violation{KindCLRLink,
				fmt.Sprintf("CLR LSN %d (txn %d) undoNext %d belongs to txn %d", rec.LSN, rec.TxnID, rec.UndoNext, tgt.TxnID)})
		case tgt.LSN >= rec.LSN:
			out = append(out, Violation{KindCLRLink,
				fmt.Sprintf("CLR LSN %d undoNext %d is not strictly earlier", rec.LSN, rec.UndoNext)})
		}
	}
	return out, nil
}

// CheckAcyclic follows PrevLSN and undoNext edges and reports any
// cycle (an orphan loop that would hang undo).
func (in *Inspector) CheckAcyclic() ([]Violation, error) {
	byLSN, all, err := in.records()
	if err != nil {
		return nil, err
	}
	const (
		white = 0 // unvisited
		gray  = 1 // on current path
		black = 2 // done
	)
	color := make(map[uint64]int, len(all))
	var out []Violation
	for _, rec := range all {
		if color[rec.LSN] != white {
			continue
		}
		var stack []uint64
		color[rec.LSN] = gray
		stack = append(stack, rec.LSN)
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			advanced := false
			for _, next := range chainEdges(byLSN[cur]) {
				if _, ok := byLSN[next]; !ok {
					continue // dangling, reported by CheckChains
				}
				switch color[next] {
				case gray:
					out = append(out, Violation{KindOrphanLoop,
						fmt.Sprintf("cycle detected through LSN %d (path %v)", next, append(stack, next))})
				case white:
					color[next] = gray
					stack = append(stack, next)
					advanced = true
				}
				if advanced {
					break
				}
			}
			if !advanced {
				color[cur] = black
				stack = stack[:len(stack)-1]
			}
		}
	}
	return out, nil
}

func chainEdges(rec *wal.Record) []uint64 {
	var edges []uint64
	if rec.PrevLSN != 0 {
		edges = append(edges, rec.PrevLSN)
	}
	if rec.Type == wal.TypeCLR && rec.UndoNext != 0 {
		edges = append(edges, rec.UndoNext)
	}
	return edges
}

// CheckDPT independently recomputes the DPT from the last checkpoint
// in the log and compares it with the analysis result; it also
// validates that every recLSN references a real UPDATE/CLR on the
// matching page.
func (in *Inspector) CheckDPT(a *recovery.Analysis) ([]Violation, error) {
	byLSN, all, err := in.records()
	if err != nil {
		return nil, err
	}
	var chk *wal.Record
	for _, rec := range all {
		if rec.Type == wal.TypeCheckpoint && (chk == nil || rec.LSN > chk.LSN) {
			chk = rec
		}
	}
	expected := make(map[int]uint64)
	chkLSN := uint64(0)
	if chk != nil {
		chkLSN = chk.LSN
		for page, recLSN := range chk.DPT {
			expected[page] = recLSN
		}
	}
	for _, rec := range all {
		if rec.LSN <= chkLSN {
			continue
		}
		switch rec.Type {
		case wal.TypeUpdate, wal.TypeCLR:
			if _, ok := expected[rec.PageID]; !ok {
				expected[rec.PageID] = rec.LSN
			}
		}
	}
	var out []Violation
	for page, recLSN := range a.DPT {
		tgt, ok := byLSN[recLSN]
		switch {
		case !ok:
			out = append(out, Violation{KindDPTMismatch,
				fmt.Sprintf("analysis DPT page %d recLSN %d missing from log", page, recLSN)})
		case tgt.PageID != page || (tgt.Type != wal.TypeUpdate && tgt.Type != wal.TypeCLR):
			out = append(out, Violation{KindDPTMismatch,
				fmt.Sprintf("analysis DPT page %d recLSN %d is %s on page %d", page, recLSN, tgt.Type, tgt.PageID)})
		}
		want, ok := expected[page]
		switch {
		case !ok:
			out = append(out, Violation{KindDPTMismatch,
				fmt.Sprintf("analysis DPT has phantom page %d (recLSN %d)", page, recLSN)})
		case want != recLSN:
			out = append(out, Violation{KindDPTMismatch,
				fmt.Sprintf("analysis DPT page %d recLSN %d, recomputed %d", page, recLSN, want)})
		}
	}
	for page, recLSN := range expected {
		if _, ok := a.DPT[page]; !ok {
			out = append(out, Violation{KindDPTMismatch,
				fmt.Sprintf("analysis DPT missing page %d (recomputed recLSN %d)", page, recLSN)})
		}
	}
	return out, nil
}

// CheckPersistence verifies the durability contract: every record of
// a committed transaction is reflected on disk, and every update of
// an ended loser transaction has a compensating CLR.
func (in *Inspector) CheckPersistence() ([]Violation, error) {
	_, all, err := in.records()
	if err != nil {
		return nil, err
	}
	committed := make(map[int]bool)
	ended := make(map[int]bool)
	for _, rec := range all {
		switch rec.Type {
		case wal.TypeCommit:
			committed[rec.TxnID] = true
		case wal.TypeEnd:
			ended[rec.TxnID] = true
		}
	}
	var out []Violation
	for _, rec := range all {
		if (rec.Type == wal.TypeUpdate || rec.Type == wal.TypeCLR) && committed[rec.TxnID] {
			if diskLSN := in.Disk.PageLSN(rec.PageID); diskLSN < rec.LSN {
				out = append(out, Violation{KindCommittedLost,
					fmt.Sprintf("committed txn %d %s LSN %d on page %d not durable (disk pageLSN %d)",
						rec.TxnID, rec.Type, rec.LSN, rec.PageID, diskLSN)})
			}
		}
	}
	for _, rec := range all {
		if rec.Type != wal.TypeUpdate || !ended[rec.TxnID] || committed[rec.TxnID] {
			continue
		}
		if !compensated(all, rec) {
			out = append(out, Violation{KindUncompensated,
				fmt.Sprintf("ended loser txn %d update LSN %d on page %d has no compensating CLR",
					rec.TxnID, rec.LSN, rec.PageID)})
		}
	}
	return out, nil
}

func compensated(all []*wal.Record, update *wal.Record) bool {
	for _, rec := range all {
		if rec.Type == wal.TypeCLR &&
			rec.TxnID == update.TxnID &&
			rec.PageID == update.PageID &&
			rec.LSN > update.LSN &&
			rec.UndoNext == update.PrevLSN {
			return true
		}
	}
	return false
}

// CheckSkew detects divergence between the buffer pool and disk:
// buffered pages older than their disk image, dirty flags without a
// pending change, and clean frames that diverge from disk.
func (in *Inspector) CheckSkew() ([]Violation, error) {
	snap := in.Pool.Snapshot()
	pages := make([]int, 0, len(snap))
	for page := range snap {
		pages = append(pages, page)
	}
	sort.Ints(pages)
	var out []Violation
	for _, page := range pages {
		meta := snap[page]
		diskLSN := in.Disk.PageLSN(page)
		switch {
		case meta.LSN < diskLSN:
			out = append(out, Violation{KindSkew,
				fmt.Sprintf("page %d buffer LSN %d older than disk LSN %d (lost update)", page, meta.LSN, diskLSN)})
		case meta.Dirty && meta.LSN == diskLSN:
			out = append(out, Violation{KindSkew,
				fmt.Sprintf("page %d flagged dirty but buffer LSN %d matches disk (phantom dirty)", page, meta.LSN)})
		case !meta.Dirty && meta.LSN != diskLSN:
			out = append(out, Violation{KindSkew,
				fmt.Sprintf("page %d clean buffer LSN %d diverges from disk LSN %d (untracked dirt)", page, meta.LSN, diskLSN)})
		}
	}
	return out, nil
}
