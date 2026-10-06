package kvdb

import (
	"errors"
	"reflect"
	"testing"
)

// program is a deterministic transaction body. Writes must not depend on
// read results so that a serial re-execution applies identical write sets.
type program struct {
	name string
	run  func(tx *Txn)
}

// The four programs use boundary-adjacent half-open intervals [0,10),
// [10,20) and overlapping ranges so that conflicts depend on commit order:
//
//	A: scan [0,10),  write key 3
//	B: scan [5,15),  write key 7   (7 is read by A; 3 is NOT read by B)
//	C: scan [10,20), write key 12  (12 is read by B; adjacent to A's range)
//	D: scan [50,60) (empty), write key 55 (fully independent)
var programs = []program{
	{"A", func(tx *Txn) { tx.Scan(0, 10); tx.Put(3, "A") }},
	{"B", func(tx *Txn) { tx.Scan(5, 15); tx.Put(7, "B") }},
	{"C", func(tx *Txn) { tx.Scan(10, 20); tx.Put(12, "C") }},
	{"D", func(tx *Txn) { tx.Scan(50, 60); tx.Put(55, "D") }},
}

func seedState() map[int]string {
	return map[int]string{1: "s1", 8: "s8", 14: "s14", 33: "s33"}
}

func newSeededDB(t *testing.T) *DB {
	t.Helper()
	db := Open()
	tx := db.Begin()
	for k, v := range seedState() {
		tx.Put(k, v)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// runConcurrent begins every transaction at the same snapshot, executes all
// bodies, then commits in the given order. It returns the names of the
// committed transactions in commit order plus the final state.
func runConcurrent(t *testing.T, order []int) ([]string, map[int]string) {
	t.Helper()
	db := newSeededDB(t)
	txns := make([]*Txn, len(programs))
	for i := range programs {
		txns[i] = db.Begin()
	}
	for i, p := range programs {
		p.run(txns[i])
	}
	var committed []string
	for _, idx := range order {
		err := txns[idx].Commit()
		if err == nil {
			committed = append(committed, programs[idx].name)
			continue
		}
		if !errors.Is(err, ErrTxnAborted) {
			t.Fatalf("commit %s: unexpected error %v", programs[idx].name, err)
		}
	}
	return committed, db.All()
}

// runSerial re-executes the named programs one transaction at a time in the
// given order on a fresh database; every commit must succeed.
func runSerial(t *testing.T, names []string) map[int]string {
	t.Helper()
	db := newSeededDB(t)
	for _, name := range names {
		for _, p := range programs {
			if p.name != name {
				continue
			}
			tx := db.Begin()
			p.run(tx)
			if err := tx.Commit(); err != nil {
				t.Fatalf("serial commit %s: %v", name, err)
			}
		}
	}
	return db.All()
}

// expectedCommitted predicts the committed set from first principles:
//   - C and D never abort (nobody writes into [10,20) or [50,60)).
//   - B aborts iff C committed before it (C writes 12 into B's [5,15)).
//   - A aborts iff B committed before it (B writes 7 into A's [0,10)).
func expectedCommitted(order []int) []string {
	pos := make(map[int]int)
	for p, idx := range order {
		pos[idx] = p
	}
	_, hasB := pos[1]
	_, hasC := pos[2]
	bCommitted := hasB && !(hasC && pos[2] < pos[1])
	var out []string
	for _, idx := range order {
		switch idx {
		case 0:
			if !(bCommitted && pos[1] < pos[0]) {
				out = append(out, "A")
			}
		case 1:
			if bCommitted {
				out = append(out, "B")
			}
		default:
			out = append(out, programs[idx].name)
		}
	}
	return out
}

func permutations(n int) [][]int {
	var out [][]int
	used := make([]bool, n)
	var rec func(cur []int)
	rec = func(cur []int) {
		if len(cur) == n {
			out = append(out, append([]int(nil), cur...))
			return
		}
		for i := 0; i < n; i++ {
			if used[i] {
				continue
			}
			used[i] = true
			rec(append(cur, i))
			used[i] = false
		}
	}
	rec(nil)
	return out
}

// TestSerializableEnumeration enumerates every commit order of the four
// transactions and checks, for each one, that (1) exactly the predicted
// transactions commit, and (2) the committed history is equivalent to the
// serial execution of the committed transactions in commit order.
func TestSerializableEnumeration(t *testing.T) {
	orders := permutations(len(programs))
	if len(orders) != 24 {
		t.Fatalf("got %d orders, want 24", len(orders))
	}
	for _, order := range orders {
		label := orderLabel(order)
		t.Run(label, func(t *testing.T) {
			committed, state := runConcurrent(t, order)

			want := expectedCommitted(order)
			if !reflect.DeepEqual(committed, want) {
				t.Fatalf("committed = %v, want %v", committed, want)
			}

			serialState := runSerial(t, committed)
			if !reflect.DeepEqual(state, serialState) {
				t.Fatalf("final state %v != serial re-execution %v", state, serialState)
			}
		})
	}
}

// TestSerializableEnumerationSubsets repeats the enumeration for every
// subset of three or fewer transactions.
func TestSerializableEnumerationSubsets(t *testing.T) {
	for n := 1; n <= 3; n++ {
		for _, subset := range combinations(len(programs), n) {
			for _, order := range permutations(n) {
				ord := make([]int, n)
				for i, p := range order {
					ord[i] = subset[p]
				}
				t.Run(orderLabel(ord), func(t *testing.T) {
					committed, state := runConcurrent(t, ord)
					want := expectedCommitted(ord)
					if !reflect.DeepEqual(committed, want) {
						t.Fatalf("committed = %v, want %v", committed, want)
					}
					if serialState := runSerial(t, committed); !reflect.DeepEqual(state, serialState) {
						t.Fatalf("final state %v != serial re-execution %v", state, serialState)
					}
				})
			}
		}
	}
}

func combinations(n, k int) [][]int {
	var out [][]int
	var rec func(start int, cur []int)
	rec = func(start int, cur []int) {
		if len(cur) == k {
			out = append(out, append([]int(nil), cur...))
			return
		}
		for i := start; i < n; i++ {
			rec(i+1, append(cur, i))
		}
	}
	rec(0, nil)
	return out
}

func orderLabel(order []int) string {
	label := ""
	for i, idx := range order {
		if i > 0 {
			label += "-"
		}
		label += programs[idx].name
	}
	return label
}
