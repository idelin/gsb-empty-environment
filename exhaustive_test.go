package memtx

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// --- operation programs -------------------------------------------------

type opKind int

const (
	opGet opKind = iota
	opPut
	opDelete
)

type op struct {
	kind  opKind
	id    int
	email string
}

func (o op) String() string {
	switch o.kind {
	case opGet:
		return fmt.Sprintf("Get(%d)", o.id)
	case opPut:
		return fmt.Sprintf("Put(%d,%s)", o.id, o.email)
	default:
		return fmt.Sprintf("Delete(%d)", o.id)
	}
}

func progString(p []op) string {
	if len(p) == 0 {
		return "<empty>"
	}
	parts := make([]string, len(p))
	for i, o := range p {
		parts[i] = o.String()
	}
	return strings.Join(parts, "; ")
}

// allOps enumerates every single operation over 3 primary keys and 3
// unique-key values. The table always starts as {1:"a", 2:"b"}, so "c"
// is free and "a"/"b" are taken.
func allOps() []op {
	var ops []op
	for _, id := range []int{1, 2, 3} {
		ops = append(ops, op{kind: opGet, id: id})
	}
	for _, id := range []int{1, 2, 3} {
		for _, e := range []string{"a", "b", "c"} {
			ops = append(ops, op{kind: opPut, id: id, email: e})
		}
	}
	for _, id := range []int{1, 2, 3} {
		ops = append(ops, op{kind: opDelete, id: id})
	}
	return ops
}

// allPrograms enumerates every transaction of length 0, 1 and 2.
func allPrograms() [][]op {
	ops := allOps()
	progs := [][]op{nil}
	for _, o := range ops {
		progs = append(progs, []op{o})
	}
	for _, o1 := range ops {
		for _, o2 := range ops {
			progs = append(progs, []op{o1, o2})
		}
	}
	return progs
}

func initialRows() map[int]Row {
	return map[int]Row{1: {1, "a"}, 2: {2, "b"}}
}

// --- reference model ----------------------------------------------------

type getResult struct {
	row Row
	ok  bool
}

type modelTxn struct {
	writes map[int]*Row
	gets   []getResult
}

// runModelOps interprets a program against the fixed snapshot.
func runModelOps(snap map[int]Row, prog []op) *modelTxn {
	mt := &modelTxn{writes: make(map[int]*Row)}
	for _, o := range prog {
		switch o.kind {
		case opGet:
			if w, ok := mt.writes[o.id]; ok {
				if w == nil {
					mt.gets = append(mt.gets, getResult{})
				} else {
					mt.gets = append(mt.gets, getResult{*w, true})
				}
			} else if r, ok := snap[o.id]; ok {
				mt.gets = append(mt.gets, getResult{r, true})
			} else {
				mt.gets = append(mt.gets, getResult{})
			}
		case opPut:
			r := Row{o.id, o.email}
			mt.writes[o.id] = &r
		case opDelete:
			mt.writes[o.id] = nil
		}
	}
	return mt
}

// modelTable is the sequential reference: rows only, no index (the index
// is derived and checked separately).
type modelTable struct {
	rows map[int]Row
}

// commit validates mt against the current state. others holds the primary
// keys written by transactions committed after mt's snapshot.
func (m *modelTable) commit(mt *modelTxn, others map[int]bool) error {
	for id := range mt.writes {
		if others[id] {
			return ErrWriteConflict
		}
	}
	seen := make(map[string]int, len(mt.writes))
	for id, w := range mt.writes {
		if w == nil {
			continue
		}
		if prev, ok := seen[w.Email]; ok && prev != id {
			return ErrUniqueConflict
		}
		seen[w.Email] = id
	}
	for id, w := range mt.writes {
		if w == nil {
			continue
		}
		for owner, r := range m.rows {
			if r.Email != w.Email || owner == id {
				continue
			}
			if ow, moved := mt.writes[owner]; moved && (ow == nil || ow.Email != w.Email) {
				continue
			}
			return ErrUniqueConflict
		}
	}
	for id, w := range mt.writes {
		if w == nil {
			delete(m.rows, id)
		} else {
			m.rows[id] = *w
		}
	}
	return nil
}

// --- drivers ------------------------------------------------------------

func runRealOps(tx *Txn, prog []op) []getResult {
	var gets []getResult
	for _, o := range prog {
		switch o.kind {
		case opGet:
			r, ok := tx.Get(o.id)
			gets = append(gets, getResult{r, ok})
		case opPut:
			tx.Put(Row{o.id, o.email})
		case opDelete:
			tx.Delete(o.id)
		}
	}
	return gets
}

func checkState(t *testing.T, tbl *Table, m *modelTable, ctx string) {
	t.Helper()
	rows, idx := tbl.Dump()
	if !reflect.DeepEqual(rows, m.rows) {
		t.Fatalf("%s: rows = %v, model = %v", ctx, rows, m.rows)
	}
	wantIdx := make(map[string]int, len(m.rows))
	for id, r := range m.rows {
		wantIdx[r.Email] = id
	}
	if !reflect.DeepEqual(idx, wantIdx) {
		t.Fatalf("%s: index = %v, want %v (rows %v)", ctx, idx, wantIdx, rows)
	}
}

type step int

const (
	stepRunA step = iota
	stepRunB
	stepCommitA
	stepCommitB
)

var schedules = map[string][]step{
	"runAB_commitAB":            {stepRunA, stepRunB, stepCommitA, stepCommitB},
	"runAB_commitBA":            {stepRunA, stepRunB, stepCommitB, stepCommitA},
	"runA_commitA_runB_commitB": {stepRunA, stepCommitA, stepRunB, stepCommitB},
	"runB_commitB_runA_commitA": {stepRunB, stepCommitB, stepRunA, stepCommitA},
}

// runSchedule executes one pair of programs under one schedule against
// both the real table and the reference model, comparing reads, commit
// outcomes, final rows, the unique index, and failed-commit atomicity.
func runSchedule(t *testing.T, pa, pb []op, schedName string) {
	t.Helper()
	sched := schedules[schedName]
	ctx := fmt.Sprintf("A=[%s] B=[%s] sched=%s", progString(pa), progString(pb), schedName)

	tbl := NewTable(Row{1, "a"}, Row{2, "b"})
	m := &modelTable{rows: initialRows()}
	snap := initialRows()
	txA, txB := tbl.Begin(), tbl.Begin()
	mA := &modelTxn{writes: make(map[int]*Row)}
	mB := &modelTxn{writes: make(map[int]*Row)}
	committed := make(map[int]bool)

	commit := func(tx *Txn, mt *modelTxn, who string) {
		beforeRows, beforeIdx := tbl.Dump()
		err := tx.Commit()
		merr := m.commit(mt, committed)
		if err != merr {
			t.Fatalf("%s: commit %s = %v, model = %v", ctx, who, err, merr)
		}
		if err != nil {
			// A failed commit must not modify anything.
			afterRows, afterIdx := tbl.Dump()
			if !reflect.DeepEqual(afterRows, beforeRows) {
				t.Fatalf("%s: failed commit %s modified rows: %v -> %v", ctx, who, beforeRows, afterRows)
			}
			if !reflect.DeepEqual(afterIdx, beforeIdx) {
				t.Fatalf("%s: failed commit %s modified index: %v -> %v", ctx, who, beforeIdx, afterIdx)
			}
			return
		}
		for id := range mt.writes {
			committed[id] = true
		}
	}

	for _, s := range sched {
		switch s {
		case stepRunA:
			mA = runModelOps(snap, pa)
			if gets := runRealOps(txA, pa); !reflect.DeepEqual(gets, mA.gets) {
				t.Fatalf("%s: A reads = %v, model = %v", ctx, gets, mA.gets)
			}
		case stepRunB:
			mB = runModelOps(snap, pb)
			if gets := runRealOps(txB, pb); !reflect.DeepEqual(gets, mB.gets) {
				t.Fatalf("%s: B reads = %v, model = %v", ctx, gets, mB.gets)
			}
		case stepCommitA:
			commit(txA, mA, "A")
		case stepCommitB:
			commit(txB, mB, "B")
		}
	}
	checkState(t, tbl, m, ctx)
}

func TestExhaustiveTxnPairs(t *testing.T) {
	progs := allPrograms()
	for _, pa := range progs {
		for _, pb := range progs {
			for name := range schedules {
				runSchedule(t, pa, pb, name)
			}
		}
	}
}

// TestConcurrentTxnPairs runs sampled pairs with racing commits. The
// commit mutex serializes them, so the outcome must equal one of the two
// sequential orders; the unique index must stay consistent either way.
func TestConcurrentTxnPairs(t *testing.T) {
	progs := allPrograms()
	for i, pa := range progs {
		for j, pb := range progs {
			if (i*len(progs)+j)%7 != 0 {
				continue
			}
			ctx := fmt.Sprintf("A=[%s] B=[%s] concurrent", progString(pa), progString(pb))

			tbl := NewTable(Row{1, "a"}, Row{2, "b"})
			txA, txB := tbl.Begin(), tbl.Begin()
			snap := initialRows()
			mA := runModelOps(snap, pa)
			mB := runModelOps(snap, pb)
			if gets := runRealOps(txA, pa); !reflect.DeepEqual(gets, mA.gets) {
				t.Fatalf("%s: A reads = %v, model = %v", ctx, gets, mA.gets)
			}
			if gets := runRealOps(txB, pb); !reflect.DeepEqual(gets, mB.gets) {
				t.Fatalf("%s: B reads = %v, model = %v", ctx, gets, mB.gets)
			}

			var wg sync.WaitGroup
			start := make(chan struct{})
			var errA, errB error
			wg.Add(2)
			go func() { defer wg.Done(); <-start; errA = txA.Commit() }()
			go func() { defer wg.Done(); <-start; errB = txB.Commit() }()
			close(start)
			wg.Wait()

			// Model both sequential orders.
			mAB := &modelTable{rows: initialRows()}
			errAB_A := mAB.commit(mA, nil)
			afterA := map[int]bool{}
			if errAB_A == nil {
				for id := range mA.writes {
					afterA[id] = true
				}
			}
			errAB_B := mAB.commit(mB, afterA)

			mBA := &modelTable{rows: initialRows()}
			errBA_B := mBA.commit(mB, nil)
			afterB := map[int]bool{}
			if errBA_B == nil {
				for id := range mB.writes {
					afterB[id] = true
				}
			}
			errBA_A := mBA.commit(mA, afterB)

			rows, _ := tbl.Dump()
			matchAB := errA == errAB_A && errB == errAB_B && reflect.DeepEqual(rows, mAB.rows)
			matchBA := errA == errBA_A && errB == errBA_B && reflect.DeepEqual(rows, mBA.rows)
			if !matchAB && !matchBA {
				t.Fatalf("%s: outcome (errA=%v errB=%v rows=%v) matches neither order (AB: %v %v %v; BA: %v %v %v)",
					ctx, errA, errB, rows, errAB_A, errAB_B, mAB.rows, errBA_A, errBA_B, mBA.rows)
			}
			checkState(t, tbl, &modelTable{rows: rows}, ctx)
		}
	}
}
