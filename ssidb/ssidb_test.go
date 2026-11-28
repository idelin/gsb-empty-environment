package ssidb

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func seed(t *testing.T, e *Engine, pairs ...string) {
	t.Helper()
	txn := e.Begin()
	for i := 0; i+1 < len(pairs); i += 2 {
		if err := txn.Put(pairs[i], pairs[i+1]); err != nil {
			t.Fatalf("seed put %s: %v", pairs[i], err)
		}
	}
	if err := txn.Commit(); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
}

// mustGet asserts a point lookup result.
func mustGet(t *testing.T, txn *Txn, key, want string, wantOK bool) {
	t.Helper()
	got, ok, err := txn.Get(key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	if ok != wantOK || (wantOK && got != want) {
		t.Fatalf("get %s = (%q,%v), want (%q,%v)", key, got, ok, want, wantOK)
	}
}

// TestWriteSkew_TwoTransactions_ExactlyOneAborted covers the classic
// two-transaction write skew: two transactions each read the other's row and
// then write their own, closing a 2-cycle of rw-antidependencies. SSI must
// abort exactly one of them and let the other commit.
func TestWriteSkew_TwoTransactions_ExactlyOneAborted(t *testing.T) {
	e := NewEngine(DefaultConfig())
	seed(t, e, "alice", "on", "bob", "on")

	t1 := e.Begin()
	t2 := e.Begin()

	mustGet(t, t1, "alice", "on", true)
	mustGet(t, t1, "bob", "on", true)
	mustGet(t, t2, "alice", "on", true)
	mustGet(t, t2, "bob", "on", true)

	// Each transaction takes the other's observed row off-call, the canonical
	// on-call write-skew pattern.
	if err := t1.Put("alice", "off"); err != nil {
		t.Fatalf("t1 put: %v", err)
	}
	t2WriteErr := t2.Put("bob", "off")

	committed, aborted := 0, 0
	if err := t1.Commit(); errors.Is(err, ErrSerialization) {
		aborted++
	} else if err != nil {
		t.Fatalf("t1 unexpected err: %v", err)
	} else {
		committed++
	}
	if t2WriteErr != nil {
		if !errors.Is(t2WriteErr, ErrSerialization) {
			t.Fatalf("t2 write err = %v, want ErrSerialization", t2WriteErr)
		}
		aborted++
		_ = t2.Rollback()
	} else {
		if err := t2.Commit(); errors.Is(err, ErrSerialization) {
			aborted++
		} else if err != nil {
			t.Fatalf("t2 unexpected err: %v", err)
		} else {
			committed++
		}
	}
	if committed != 1 || aborted != 1 {
		t.Fatalf("write skew: committed=%d aborted=%d, want 1/1", committed, aborted)
	}
}

// TestReadOnlyAnomaly_ThreeTransactions covers the three-transaction
// read-only anomaly. The pivot T2 reads T3's early-committed write and writes
// a row that the read-only T1 reads afterwards; T1 -> T2 -> T3 is the
// dangerous structure and the still-uncommitted pivot T2 is the victim. T3
// commits first, exactly as PostgreSQL's read-only anomaly requires.
func TestReadOnlyAnomaly_ThreeTransactions(t *testing.T) {
	e := NewEngine(DefaultConfig())
	seed(t, e, "x", "10", "y", "10")

	t1 := e.BeginReadOnly() // the read-only observer (starts before pivot)
	t3 := e.Begin()         // commits first; out-conflict invisible to t1
	t2 := e.Begin()         // pivot

	// T3 writes a disjoint key and commits first, so it never takes a write
	// intent on the keys T2 will write; the anomaly is carried entirely by
	// rw-antidependencies rather than a first-updater-wins conflict.
	if err := t3.Put("z", "20"); err != nil {
		t.Fatalf("t3 put: %v", err)
	}
	if err := t3.Commit(); err != nil {
		t.Fatalf("t3 commit: %v", err)
	}

	// T2 reads T3's now-committed-but-invisible write, creating t2 -> t3, and
	// then writes y without committing yet.
	mustGet(t, t2, "z", "", false) // invisible to snapshot, records edge t2 -> t3
	if err := t2.Put("y", "99"); err != nil {
		t.Fatalf("t2 put: %v", err)
	}

	// The read-only T1 now reads the row the active pivot wrote; its read
	// builds t1 -> t2, closing the 3-transaction structure. T2 is aborted
	// immediately because it is the uncommitted pivot.
	if _, _, err := t1.Get("y"); err != nil {
		t.Fatalf("t1 get: %v", err)
	}
	if t2.status != statusAborted {
		t.Fatal("uncommitted pivot t2 must be aborted when the structure closes")
	}
	if err := t2.Commit(); !errors.Is(err, ErrSerialization) {
		t.Fatalf("t2 commit = %v, want ErrSerialization", err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatalf("read-only t1 commit = %v, want nil", err)
	}

}

// TestReadOnlyAnomaly_DelayedDetection reproduces the subtle timing where T3
// commits before T1 ever reads, yet the dangerous structure only becomes
// visible later, when T1 reads a key that the pivot T2 is writing. The
// retained SIREAD locks / edges must still catch it and must never abort a
// committed transaction.
func TestReadOnlyAnomaly_DelayedDetection(t *testing.T) {
	e := NewEngine(DefaultConfig())
	seed(t, e, "x", "1", "y", "1")

	t1 := e.BeginReadOnly() // snapshot fixed before T3 commits
	t3 := e.Begin()
	t2 := e.Begin()

	// Disjoint keys again: the edges must come from predicate dependencies,
	// not from first-updater-wins write conflicts.
	if err := t3.Put("z", "2"); err != nil {
		t.Fatalf("t3 put: %v", err)
	}
	if err := t3.Commit(); err != nil {
		t.Fatalf("t3 must commit first: %v", err)
	}

	// T2 cannot see T3's write and later writes x: edge t2 -> t3.
	mustGet(t, t2, "z", "", false)
	if err := t2.Put("x", "3"); err != nil {
		t.Fatalf("t2 put: %v", err)
	}

	// The read-only observer only reads now. Its late read of x is the single
	// edge that closes T1 -> T2 -> T3, and the retained t2 -> t3 edge plus
	// T2's SIREAD hit make the delayed detection possible.
	_, _, err := t1.Get("x")
	if err != nil {
		t.Fatalf("t1 get: %v", err)
	}
	if t2.status != statusAborted {
		t.Fatal("pivot t2 must be aborted when the late edge appears")
	}
	if err := t2.Commit(); !errors.Is(err, ErrSerialization) {
		t.Fatalf("t2 commit = %v, want ErrSerialization", err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatalf("read-only t1 commit = %v, want nil", err)
	}
}

// TestSIREADLocksRetainedAfterCommit proves that a committed reader's
// predicate locks and conflict edges are not dropped at commit time. They must
// survive until all transactions concurrent with it finish, otherwise a
// later writer would slip past and the late dangerous structure would be
// missed.
func TestSIREADLocksRetainedAfterCommit(t *testing.T) {
	e := NewEngine(DefaultConfig())
	seed(t, e, "k", "v")

	// `concurrent` is active across the reader's whole lifetime, so nothing
	// overlapping can be reclaimed at the reader's commit point.
	concurrent := e.Begin()
	reader := e.Begin()
	mustGet(t, reader, "k", "v", true)
	if err := reader.Commit(); err != nil {
		t.Fatalf("reader commit: %v", err)
	}

	// The committed transaction must still be tracked while a concurrent
	// transaction lives.
	rt, ok := e.txns[reader.id]
	if !ok {
		t.Fatal("committed txn garbage-collected while concurrent txn active")
	}
	if _, present := e.tupleReaders["k"][rt]; !present {
		t.Fatal("SIREAD tuple lock removed at commit time; late anomalies would be missed")
	}

	if _, ok := e.txns[reader.id]; !ok {
		t.Fatal("committed txn must be retained while a concurrent txn remains active")
	}

	// Once every overlapping transaction ends, retention is no longer needed.
	if err := concurrent.Rollback(); err != nil {
		t.Fatalf("concurrent rollback: %v", err)
	}
	if _, ok := e.txns[reader.id]; ok {
		t.Fatal("finished txn should be garbage-collected after all overlaps end")
	}
	if _, present := e.tupleReaders["k"]; present {
		t.Fatal("SIREAD lock index should be empty after GC")
	}
}

// TestLockEscalation_Completeness drives tuple -> page -> table promotion and
// proves escalation never loses conflict coverage: a write to a key that was
// never individually read still hits the covering page/table SIREAD lock and
// creates the rw edge. SIREAD locks also never block a writer.
func TestLockEscalation_Completeness(t *testing.T) {
	cfg := Config{MaxTupleLocks: 4, MaxPageLocks: 2, PagesPerTable: 8}
	e := NewEngine(cfg)

	keys := []string{"a", "b", "c", "d", "e", "f"}
	pairs := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		pairs = append(pairs, k, "0")
	}
	seed(t, e, pairs...)

	reader := e.Begin()
	for _, k := range keys {
		mustGet(t, reader, k, "0", true)
	}
	if !reader.locks.table {
		t.Fatalf("expected relation-level lock after escalation, got tuples=%d pages=%d table=%v",
			len(reader.locks.tuples), len(reader.locks.pages), reader.locks.table)
	}

	// A brand-new key was never read, but the table lock must cover it.
	writer := e.Begin()
	done := make(chan error, 1)
	go func() { done <- writer.Put("brand-new-key", "1") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SIREAD locks must not block writes: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer blocked by SIREAD lock")
	}

	if _, hasEdge := reader.outEdges[writer]; !hasEdge {
		t.Fatal("escalated table lock failed to capture the phantom write edge")
	}
	_ = writer.Rollback()
	_ = reader.Rollback()

	// Page-level coverage check: read enough distinct pages to promote tuples
	// to pages but stay below the table threshold, then write an unread key
	// that hashes onto a visited page.
	e2 := NewEngine(Config{MaxTupleLocks: 1, MaxPageLocks: 64, PagesPerTable: 4})
	seed(t, e2, "p1", "0", "p2", "0", "p3", "0")
	r2 := e2.Begin()
	mustGet(t, r2, "p1", "0", true)
	mustGet(t, r2, "p2", "0", true)
	if r2.locks.table || len(r2.locks.tuples) != 0 || len(r2.locks.pages) == 0 {
		t.Fatalf("expected page-level locks, got tuples=%d pages=%d table=%v",
			len(r2.locks.tuples), len(r2.locks.pages), r2.locks.table)
	}
	targetPage := e2.pageOf("p1")
	phantom := ""
	for n := 0; n < 10000; n++ {
		candidate := "candidate-" + itoa(n)
		if e2.pageOf(candidate) == targetPage && candidate != "p1" && candidate != "p2" {
			phantom = candidate
			break
		}
	}
	if phantom == "" {
		t.Fatal("could not find a phantom key on a visited page")
	}
	w2 := e2.Begin()
	if err := w2.Put(phantom, "9"); err != nil {
		t.Fatalf("phantom write: %v", err)
	}
	if _, hasEdge := r2.outEdges[w2]; !hasEdge {
		t.Fatal("page SIREAD lock failed to cover an unread key on the same leaf page")
	}
	_ = w2.Rollback()
	_ = r2.Rollback()
}

// TestReadOnlyDeferrable_ROSafe_NeverAborts verifies the RO-Safe path: a
// deferrable read-only transaction waits for concurrent r/w transactions to
// finish, is marked safe, takes no predicate locks, and sails through an
// interleaving that aborts a normal read-only transaction.
func TestReadOnlyDeferrable_ROSafe_NeverAborts(t *testing.T) {
	e := NewEngine(DefaultConfig())
	seed(t, e, "x", "1", "y", "1")

	rw := e.Begin()
	if e.ActiveRWCount() != 1 {
		t.Fatal("expected one active r/w transaction")
	}

	safeRO := make(chan *Txn, 1)
	go func() { safeRO <- e.BeginReadOnlyDeferrable() }()

	// The deferrable transaction must wait while the r/w transaction is active.
	select {
	case <-safeRO:
		t.Fatal("deferrable RO began before a safe snapshot was available")
	case <-time.After(50 * time.Millisecond):
	}

	if err := rw.Put("x", "2"); err != nil {
		t.Fatalf("rw put: %v", err)
	}
	if err := rw.Commit(); err != nil {
		t.Fatalf("rw commit: %v", err)
	}

	var ro *Txn
	select {
	case ro = <-safeRO:
	case <-time.After(time.Second):
		t.Fatal("deferrable RO never woke after r/w commit")
	}
	if !ro.Safe() {
		t.Fatal("deferrable RO should hold a safe snapshot")
	}

	// Recreate the 3-txn anomaly shape; the safe RO neither locks nor edges.
	t2 := e.Begin()
	t3 := e.Begin()
	mustGet(t, ro, "y", "1", true)
	if len(ro.locks.tuples) != 0 || len(ro.locks.pages) != 0 || ro.locks.table {
		t.Fatal("safe RO must not acquire SIREAD locks")
	}
	mustGet(t, t2, "y", "1", true)
	if err := t3.Put("x", "3"); err != nil {
		t.Fatalf("t3 put: %v", err)
	}
	if err := t3.Commit(); err != nil {
		t.Fatalf("t3 commit: %v", err)
	}
	if err := t2.Put("y", "4"); err != nil {
		t.Fatalf("t2 put: %v", err)
	}
	if err := t2.Commit(); err != nil {
		t.Fatalf("t2 commit: %v (safe RO must not cause an abort)", err)
	}
	if err := ro.Commit(); err != nil {
		t.Fatalf("safe RO commit = %v, want nil", err)
	}
}

// TestRangeScanPhantomProtection verifies that a range scan's page/relation
// SIREAD locks catch a brand-new key inserted into the scanned range, the
// phantom-read case that tuple locks alone would miss.
func TestRangeScanPhantomProtection(t *testing.T) {
	e := NewEngine(Config{MaxTupleLocks: 256, MaxPageLocks: 64, PagesPerTable: 128})
	seed(t, e, "a1", "1")

	reader := e.Begin()
	rows, err := reader.Scan("a", "az")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(rows) != 1 || rows[0].Key != "a1" {
		t.Fatalf("initial scan = %v", rows)
	}

	writer := e.Begin()
	// Search a fresh phantom key that lands on one of the pages the scan read.
	var pages []uint64
	for page := range reader.locks.pages {
		pages = append(pages, page)
	}
	if len(pages) == 0 {
		t.Fatal("scan must hold page-level SIREAD locks")
	}
	phantom := ""
	for n := 0; n < 100000 && phantom == ""; n++ {
		candidate := "a" + itoa(1000+n)
		if candidate >= "a" && candidate <= "az" {
			p := e.pageOf(candidate)
			for _, locked := range pages {
				if p == locked {
					phantom = candidate
					break
				}
			}
		}
	}
	if phantom == "" {
		t.Fatal("no phantom key mapped to a scanned page")
	}
	if err := writer.Put(phantom, "new"); err != nil {
		t.Fatalf("phantom insert must not block: %v", err)
	}
	if _, ok := reader.outEdges[writer]; !ok {
		t.Fatal("phantom insert did not create rw edge through the scan's page lock")
	}
	_ = writer.Rollback()
	_ = reader.Rollback()
}

// TestNormalReadOnly_StillAborted contrasts RO-Safe with an ordinary
// read-only transaction. The same three-transaction shape must abort the
// pivot when the reader is a normal RO transaction (which takes SIREAD locks
// and participates in the rw graph), proving RO-Safe is a precise exemption
// rather than a missed detection.
func TestNormalReadOnly_StillAborted(t *testing.T) {
	e := NewEngine(DefaultConfig())
	seed(t, e, "x", "10", "y", "10")

	t1 := e.BeginReadOnly() // ordinary RO: predicate-locked, graph-participating
	t3 := e.Begin()
	t2 := e.Begin()

	if err := t3.Put("z", "20"); err != nil {
		t.Fatalf("t3 put: %v", err)
	}
	if err := t3.Commit(); err != nil {
		t.Fatalf("t3 commit: %v", err)
	}
	mustGet(t, t2, "z", "", false) // t2 -> t3
	if err := t2.Put("y", "99"); err != nil {
		t.Fatalf("t2 put: %v", err)
	}
	if _, _, err := t1.Get("y"); err != nil {
		t.Fatalf("t1 get: %v", err)
	}
	if t2.status != statusAborted {
		t.Fatal("normal RO participation must still trigger the pivot abort")
	}
	if err := t2.Commit(); !errors.Is(err, ErrSerialization) {
		t.Fatalf("t2 commit = %v, want ErrSerialization", err)
	}
	_ = t1.Rollback()
}

// TestSnapshotIsolationBasics pins down the SI guarantees SSI builds on: no
// dirty reads, repeatable reads and first-updater-wins write conflicts.
func TestSnapshotIsolationBasics(t *testing.T) {
	e := NewEngine(DefaultConfig())
	seed(t, e, "k", "v0")

	// hold opens its snapshot before anything else changes the key, which
	// later proves repeatable reads across concurrent commits.
	hold := e.Begin()

	// No dirty read: a concurrent reader keeps seeing the committed value
	// while another transaction buffers an uncommitted write.
	reader := e.Begin()
	writer := e.Begin()
	if err := writer.Put("k", "v1"); err != nil {
		t.Fatalf("writer put: %v", err)
	}
	mustGet(t, reader, "k", "v0", true)

	// First-updater-wins: reader cannot write a key writer is actively writing.
	if err := reader.Put("k", "v2"); !errors.Is(err, ErrWriteWriteConflict) {
		t.Fatalf("reader put = %v, want ErrWriteWriteConflict", err)
	}
	if err := reader.Rollback(); err != nil {
		t.Fatalf("explicit rollback = %v, want nil", err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatalf("writer commit: %v", err)
	}

	// Repeatable read: hold's older snapshot never moves to v1.
	mustGet(t, hold, "k", "v0", true)
	updater := e.Begin()
	if err := updater.Put("k", "v2"); err != nil {
		t.Fatalf("updater put: %v", err)
	}
	// hold still sees its own snapshot while updater's write is in flight.
	mustGet(t, hold, "k", "v0", true)
	if err := updater.Commit(); err != nil {
		t.Fatalf("updater commit: %v", err)
	}
	mustGet(t, hold, "k", "v0", true)
	if err := hold.Rollback(); err != nil {
		t.Fatalf("hold rollback: %v", err)
	}

	t3 := e.Begin()
	mustGet(t, t3, "k", "v2", true)

	// Range scan observes snapshot contents and own buffered writes.
	if err := t3.Put("ka", "va"); err != nil {
		t.Fatalf("t3 put ka: %v", err)
	}
	rows, err := t3.Scan("k", "kz")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []KeyValue{{Key: "k", Value: "v2"}, {Key: "ka", Value: "va"}}
	if len(rows) != len(want) {
		t.Fatalf("scan rows = %v, want %v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("scan row %d = %v, want %v", i, rows[i], want[i])
		}
	}
	if err := t3.Commit(); err != nil {
		t.Fatalf("t3 commit: %v", err)
	}
}

// TestConcurrentStress exercises many goroutines performing SI+SSI work and
// asserts every failure is a 40001-class serialization error that callers can
// retry, and that the engine state stays internally consistent.
func TestConcurrentStress(t *testing.T) {
	e := NewEngine(Config{MaxTupleLocks: 8, MaxPageLocks: 4, PagesPerTable: 16})
	seed(t, e, "counter", "0")

	const goroutines = 16
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			succeeded := false
			for attempt := 0; attempt < 500; attempt++ {
				txn := e.Begin()
				v, ok, err := txn.Get("counter")
				if err != nil {
					_ = txn.Rollback()
					continue
				}
				if !ok {
					v = "0"
				}
				next := itoa(atoi(v) + 1)
				if err := txn.Put("counter", next); err != nil {
					_ = txn.Rollback()
					continue
				}
				if err := txn.Commit(); err != nil {
					if !IsSerializationFailure(err) {
						t.Errorf("unexpected non-serialization error: %v", err)
						return
					}
					time.Sleep(time.Duration(attempt%5) * time.Microsecond)
					continue
				}
				succeeded = true
				break
			}
			if !succeeded {
				t.Error("worker exhausted its retry budget without committing")
			}
		}()
	}
	wg.Wait()

	txn := e.BeginReadOnlyDeferrable()
	v, ok, err := txn.Get("counter")
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	if !ok || atoi(v) != goroutines {
		t.Fatalf("counter = %q (ok=%v), want %d", v, ok, goroutines)
	}
	if err := txn.Commit(); err != nil {
		t.Fatalf("final commit: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
