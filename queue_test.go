package diskpq

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func open(t *testing.T, dir string, opts ...Option) *Queue {
	t.Helper()
	q, err := Open(dir, opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return q
}

func popIDs(t *testing.T, q *Queue, n int) []string {
	t.Helper()
	items, err := q.PopBatch(n)
	if err != nil {
		t.Fatalf("PopBatch: %v", err)
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids
}

func enqueue(t *testing.T, q *Queue, id string, pri int64) {
	t.Helper()
	if err := q.Enqueue(id, pri); err != nil {
		t.Fatalf("Enqueue(%q): %v", id, err)
	}
}

func TestPriorityAndFIFOOrder(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)
	defer q.Close()

	enqueue(t, q, "low-1", 10)
	enqueue(t, q, "mid-1", 5)
	enqueue(t, q, "high-1", 1)
	enqueue(t, q, "mid-2", 5)
	enqueue(t, q, "mid-3", 5)
	enqueue(t, q, "low-2", 10)

	got := popIDs(t, q, 100)
	want := []string{"high-1", "mid-1", "mid-2", "mid-3", "low-1", "low-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestNegativePriorities(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)
	defer q.Close()

	enqueue(t, q, "zero", 0)
	enqueue(t, q, "neg-big", -100)
	enqueue(t, q, "pos", 7)
	enqueue(t, q, "neg-small", -1)

	got := popIDs(t, q, 100)
	want := []string{"neg-big", "neg-small", "zero", "pos"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestEmptyQueue(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)
	defer q.Close()

	items, err := q.PopBatch(5)
	if err != nil {
		t.Fatalf("PopBatch on empty: %v", err)
	}
	if items != nil {
		t.Fatalf("empty pop = %v, want nil", items)
	}
	if q.Len() != 0 {
		t.Fatalf("Len = %d, want 0", q.Len())
	}
	// n <= 0 is a no-op even on a non-empty queue.
	enqueue(t, q, "a", 1)
	if items, err := q.PopBatch(0); err != nil || items != nil {
		t.Fatalf("PopBatch(0) = %v, %v", items, err)
	}
	if q.Len() != 1 {
		t.Fatalf("Len = %d, want 1", q.Len())
	}
}

func TestUpdateReordersAndMissingIsIgnored(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)
	defer q.Close()

	enqueue(t, q, "a", 1)
	enqueue(t, q, "b", 2)
	enqueue(t, q, "c", 3)

	ok, err := q.Update("c", -50)
	if err != nil || !ok {
		t.Fatalf("Update existing = %v, %v", ok, err)
	}
	ok, err = q.Update("ghost", 0)
	if err != nil || ok {
		t.Fatalf("Update missing = %v, %v, want false, nil", ok, err)
	}
	if q.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (update must not create)", q.Len())
	}

	got := popIDs(t, q, 100)
	want := []string{"c", "a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestDeleteAndNoResurrect(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)
	defer q.Close()

	enqueue(t, q, "a", 1)
	enqueue(t, q, "b", 2)

	ok, err := q.Delete("a")
	if err != nil || !ok {
		t.Fatalf("Delete existing = %v, %v", ok, err)
	}
	ok, err = q.Delete("a")
	if err != nil || ok {
		t.Fatalf("Delete twice = %v, %v, want false, nil", ok, err)
	}
	// Stale update after delete must not resurrect.
	ok, err = q.Update("a", -999)
	if err != nil || ok {
		t.Fatalf("Update deleted = %v, %v, want false, nil", ok, err)
	}
	if q.Len() != 1 {
		t.Fatalf("Len = %d, want 1", q.Len())
	}
	got := popIDs(t, q, 100)
	if want := []string{"b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pop = %v, want %v", got, want)
	}
}

func TestRestartPreservesOrderAndSeq(t *testing.T) {
	dir := t.TempDir()

	q := open(t, dir)
	enqueue(t, q, "first", 5)
	enqueue(t, q, "second", 5)
	enqueue(t, q, "third", 1)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	q2 := open(t, dir)
	defer q2.Close()
	// New enqueues keep FIFO order relative to pre-restart same-priority tasks.
	enqueue(t, q2, "fourth", 5)

	got := popIDs(t, q2, 100)
	want := []string{"third", "first", "second", "fourth"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order after restart = %v, want %v", got, want)
	}
}

func TestPopBatchAtomicFailureKeepsTasks(t *testing.T) {
	dir := t.TempDir()
	injected := errors.New("injected fsync failure")
	fail := true
	hooks := Hooks{
		BeforeWALSync: func(op string) error {
			if fail && op == opPop {
				return injected
			}
			return nil
		},
	}
	q := open(t, dir, WithHooks(hooks))
	defer q.Close()

	enqueue(t, q, "a", 1)
	enqueue(t, q, "b", 2)
	enqueue(t, q, "c", 3)

	if _, err := q.PopBatch(2); !errors.Is(err, injected) {
		t.Fatalf("PopBatch err = %v, want injected", err)
	}
	if q.Len() != 3 {
		t.Fatalf("Len after failed pop = %d, want 3 (nothing lost)", q.Len())
	}

	fail = false
	got := popIDs(t, q, 2)
	if want := []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pop after recovery = %v, want %v", got, want)
	}
	if q.Len() != 1 {
		t.Fatalf("Len = %d, want 1", q.Len())
	}
}

func TestCrashAfterPopCommitLosesNothingAndDuplicatesNothing(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)

	enqueue(t, q, "a", 1)
	enqueue(t, q, "b", 2)
	enqueue(t, q, "c", 3)

	// Commit a batch pop, then simulate a crash: abandon q without Close and
	// reopen the directory. The committed pop must be replayed exactly once.
	if _, err := q.PopBatch(2); err != nil {
		t.Fatalf("PopBatch: %v", err)
	}
	// No Close: q "crashed" right after the pop commit.

	q2 := open(t, dir)
	defer q2.Close()
	if q2.Len() != 1 {
		t.Fatalf("Len after crash = %d, want 1", q2.Len())
	}
	got := popIDs(t, q2, 100)
	if want := []string{"c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("remaining = %v, want %v", got, want)
	}
}

func TestCrashBeforeFsyncDropsOnlyUncommittedOp(t *testing.T) {
	dir := t.TempDir()
	crash := errors.New("crash before fsync")
	enqCount := 0
	q := open(t, dir, WithHooks(Hooks{
		BeforeWALSync: func(op string) error {
			if op == opEnqueue && enqCount >= 1 {
				return crash
			}
			return nil
		},
	}))

	enqueue(t, q, "durable-1", 1)
	enqCount++
	// This enqueue fails before fsync: as if the process died mid-write.
	if err := q.Enqueue("lost", 2); !errors.Is(err, crash) {
		t.Fatalf("Enqueue err = %v, want crash", err)
	}
	// "Crash": abandon without Close, reopen.
	q2 := open(t, dir)
	defer q2.Close()
	if q2.Len() != 1 {
		t.Fatalf("Len = %d, want 1", q2.Len())
	}
	got := popIDs(t, q2, 100)
	if want := []string{"durable-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pop = %v, want %v", got, want)
	}
}

func TestTornWALTailIsDiscarded(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)
	enqueue(t, q, "a", 1)
	enqueue(t, q, "b", 2)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Simulate a power-loss torn write: partial line + garbage after valid data.
	f, err := os.OpenFile(filepath.Join(dir, walFileName), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("zz{\"op\":\"enq\",\"id\":\"gho"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	q2 := open(t, dir)
	defer q2.Close()
	if q2.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (torn tail discarded)", q2.Len())
	}
	got := popIDs(t, q2, 100)
	if want := []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pop = %v, want %v", got, want)
	}
}

func TestStaleUpdateRecordInWALCannotResurrect(t *testing.T) {
	dir := t.TempDir()
	// Hand-craft a WAL: enqueue, delete, then a stale update for the deleted id
	// (e.g. replaying an old segment after compaction history).
	q := open(t, dir)
	enqueue(t, q, "victim", 1)
	if ok, err := q.Delete("victim"); err != nil || !ok {
		t.Fatalf("Delete = %v, %v", ok, err)
	}
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Append a forged stale update record (valid CRC, as a real old record
	// would have had) directly to the WAL.
	stale := walRecord{Op: opUpdate, ID: "victim", Priority: -1000}
	payload, _ := json.Marshal(stale)
	f, err := os.OpenFile(filepath.Join(dir, walFileName), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "%s %s\n", string(padHex(crc32.Checksum(payload, crcTable))), payload)
	f.Close()

	q2 := open(t, dir)
	defer q2.Close()
	if q2.Len() != 0 {
		t.Fatalf("Len = %d, want 0: stale update resurrected a deleted task", q2.Len())
	}
}

func TestCompactionKeepsOrderAndSeqMonotonic(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir, WithCompactThreshold(1)) // compact after every op

	for i := 0; i < 20; i++ {
		enqueue(t, q, fmt.Sprintf("task-%02d", i), 5)
	}
	if ok, err := q.Delete("task-03"); err != nil || !ok {
		t.Fatalf("Delete = %v, %v", ok, err)
	}
	if ok, err := q.Update("task-10", -1); err != nil || !ok {
		t.Fatalf("Update = %v, %v", ok, err)
	}
	if _, err := q.PopBatch(2); err != nil {
		t.Fatalf("PopBatch: %v", err)
	}
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Snapshot exists and WAL was truncated.
	if _, err := os.Stat(filepath.Join(dir, snapshotFileName)); err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}

	q2 := open(t, dir)
	defer q2.Close()
	// Seq continues monotonically across compaction+restart.
	enqueue(t, q2, "task-20", 5)

	// PopBatch(2) took task-10 (-1) then task-00 (5, oldest); task-03 was
	// deleted. Remaining same-priority tasks stay in Seq order, task-20 last.
	var want []string
	for i := 1; i <= 20; i++ {
		if i == 3 || i == 10 {
			continue
		}
		want = append(want, fmt.Sprintf("task-%02d", i))
	}
	got := popIDs(t, q2, 100)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order after compaction+restart =\n%v\nwant\n%v", got, want)
	}
}

func TestPopBatchPartialAndExact(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)
	defer q.Close()

	enqueue(t, q, "a", 1)
	enqueue(t, q, "b", 2)
	enqueue(t, q, "c", 3)

	got := popIDs(t, q, 10) // n > Len: capped
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pop = %v, want %v", got, want)
	}
	if q.Len() != 0 {
		t.Fatalf("Len = %d, want 0", q.Len())
	}
}

func TestDuplicateIDRejected(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)
	defer q.Close()

	enqueue(t, q, "a", 1)
	if err := q.Enqueue("a", 9); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate Enqueue err = %v, want ErrDuplicateID", err)
	}
	if q.Len() != 1 {
		t.Fatalf("Len = %d, want 1", q.Len())
	}
}

func TestClosedQueueRejectsOps(t *testing.T) {
	dir := t.TempDir()
	q := open(t, dir)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := q.Enqueue("a", 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("Enqueue after close = %v", err)
	}
	if _, err := q.PopBatch(1); !errors.Is(err, ErrClosed) {
		t.Fatalf("PopBatch after close = %v", err)
	}
	if err := q.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
}
