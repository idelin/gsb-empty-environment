package occ

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
)

func TestBasicCommitAndRead(t *testing.T) {
	s := NewStore()
	s.Put("a", "1")

	txn := s.Begin()
	if got := txn.Read("a"); got != "1" {
		t.Fatalf("Read(a) = %q, want %q", got, "1")
	}
	txn.Write("b", "2")
	if !txn.Commit() {
		t.Fatal("Commit aborted, want success")
	}
	if got := s.Get("b"); got != "2" {
		t.Fatalf("Get(b) = %q, want %q", got, "2")
	}
	st := s.Stats()
	if st.Commits != 1 || st.Aborts != 0 {
		t.Fatalf("Stats: got %+v, want {Commits:1 Aborts:0}", st)
	}
}

func TestReadYourOwnWrites(t *testing.T) {
	s := NewStore()
	txn := s.Begin()
	txn.Write("x", "42")
	if got := txn.Read("x"); got != "42" {
		t.Fatalf("Read after Write = %q, want %q", got, "42")
	}
	if !txn.Commit() {
		t.Fatal("Commit aborted, want success")
	}
}

// TestStaleReadSetAborts: a transaction whose ReadSet version goes
// stale (another txn commits a write to a read key) must abort, and
// its WriteSet must not be partially applied.
func TestStaleReadSetAborts(t *testing.T) {
	s := NewStore()
	s.Put("hot", "0")
	s.Put("other", "untouched")

	txn := s.Begin()
	txn.Read("hot") // record version of "hot"

	// Concurrent committed modification of "hot".
	s.Put("hot", "1")

	txn.Write("other", "corrupted")
	if txn.Commit() {
		t.Fatal("Commit succeeded with stale ReadSet, want abort")
	}
	if got := s.Get("other"); got != "untouched" {
		t.Fatalf("aborted txn leaked write: Get(other) = %q, want %q", got, "untouched")
	}
	st := s.Stats()
	if st.Aborts != 1 {
		t.Fatalf("Aborts = %d, want 1", st.Aborts)
	}
}

// TestOverlappingWritesRollback: two txns read the same counter and
// both write; exactly one commits, the other aborts cleanly.
func TestOverlappingWritesRollback(t *testing.T) {
	s := NewStore()
	s.Put("counter", "0")
	s.Put("log", "clean")

	t1 := s.Begin()
	t2 := s.Begin()
	v1 := t1.Read("counter")
	v2 := t2.Read("counter")
	if v1 != "0" || v2 != "0" {
		t.Fatalf("both txns should read 0, got %q and %q", v1, v2)
	}
	t1.Write("counter", "1")
	t1.Write("log", "t1-was-here")
	t2.Write("counter", "1")
	t2.Write("log", "t2-was-here")

	if !t1.Commit() {
		t.Fatal("t1 should commit (no conflict at its validation time)")
	}
	if t2.Commit() {
		t.Fatal("t2 must abort: its ReadSet version of counter is stale")
	}
	if got := s.Get("log"); got != "t1-was-here" {
		t.Fatalf("aborted t2 leaked write: Get(log) = %q, want %q", got, "t1-was-here")
	}
	if got := s.Get("counter"); got != "1" {
		t.Fatalf("Get(counter) = %q, want %q", got, "1")
	}
}

// TestConcurrentHotKeys: many goroutines hammer a few hot keys with
// read-modify-write transactions. Final state must be exactly
// consistent, and contention must show up in the abort stats.
func TestConcurrentHotKeys(t *testing.T) {
	s := NewStore()
	const keys = 8
	const workers = 32
	const iters = 500

	for i := 0; i < keys; i++ {
		s.Put(fmt.Sprintf("k%d", i), "0")
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				key := fmt.Sprintf("k%d", (w+i)%keys)
				s.Transact(func(txn *Txn) {
					n, _ := strconv.Atoi(txn.Read(key))
					txn.Write(key, strconv.Itoa(n+1))
				})
			}
		}(w)
	}
	wg.Wait()

	total := 0
	for i := 0; i < keys; i++ {
		n, _ := strconv.Atoi(s.Get(fmt.Sprintf("k%d", i)))
		total += n
	}
	if want := workers * iters; total != want {
		t.Fatalf("total increments = %d, want %d (lost update!)", total, want)
	}
	st := s.Stats()
	t.Logf("commits=%d aborts=%d conflictRate=%.2f%%",
		st.Commits, st.Aborts, 100*st.ConflictRate())
	if st.Commits != uint64(workers*iters) {
		t.Fatalf("Commits = %d, want %d", st.Commits, workers*iters)
	}
	if st.Aborts == 0 {
		t.Fatal("expected aborts under hot-key contention, got 0")
	}
}

// TestConflictRateStats verifies the conflict-rate accounting directly.
func TestConflictRateStats(t *testing.T) {
	s := NewStore()
	s.Put("a", "0")

	winner := s.Begin()
	loser := s.Begin()
	winner.Read("a")
	loser.Read("a")
	winner.Write("a", "1")
	loser.Write("a", "2")
	if !winner.Commit() || loser.Commit() {
		t.Fatal("want winner commit + loser abort")
	}

	st := s.Stats()
	if st.Commits != 1 || st.Aborts != 1 {
		t.Fatalf("Stats = %+v, want {Commits:1 Aborts:1}", st)
	}
	if rate := st.ConflictRate(); rate != 0.5 {
		t.Fatalf("ConflictRate = %v, want 0.5", rate)
	}
}

// TestReadCommittedValuesConcurrent: readers never block and never
// observe torn values while writers commit concurrently.
func TestReadCommittedValuesConcurrent(t *testing.T) {
	s := NewStore()
	s.Put("v", "0")

	var writers, readers sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < 8; w++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 1; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				s.Transact(func(txn *Txn) {
					txn.Read("v")
					txn.Write("v", strconv.Itoa(i))
				})
			}
		}()
	}
	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 50000; i++ {
				txn := s.Begin()
				v := txn.Read("v")
				txn.Commit() // may abort under writer churn; that is legal OCC
				if _, err := strconv.Atoi(v); err != nil {
					t.Errorf("torn read: %q", v)
					return
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	writers.Wait()
}
