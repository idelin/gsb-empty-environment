package kvimport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var errSimulatedCrash = errors.New("simulated crash")

// crashAt panics with errSimulatedCrash when the given fault point fires
// for the nth time, simulating an abrupt process crash / power loss.
type crashAt struct {
	point FaultPoint
	nth   int
	count int
}

func (c *crashAt) OnFault(p FaultPoint) {
	if p == c.point {
		c.count++
		if c.count >= c.nth {
			panic(errSimulatedCrash)
		}
	}
}

func runCatching(cfg Config) (res Result, err error, crashed bool) {
	defer func() {
		if r := recover(); r != nil {
			if r == errSimulatedCrash {
				crashed = true
				return
			}
			panic(r)
		}
	}()
	res, err = Run(cfg)
	return
}

func writeInput(t *testing.T, content string) (dir, input string) {
	t.Helper()
	dir = t.TempDir()
	input = filepath.Join(dir, "input.txt")
	if err := os.WriteFile(input, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, input
}

func makeRecords(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "k%d=v%d\n", i, i)
	}
	return b.String()
}

func mustRun(t *testing.T, cfg Config) Result {
	t.Helper()
	res, err, crashed := runCatching(cfg)
	if crashed {
		t.Fatal("unexpected crash")
	}
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestBasicImportPublishesStore(t *testing.T) {
	dir, input := writeInput(t, makeRecords(5))

	res := mustRun(t, Config{Dir: dir, InputPath: input, BatchSize: 2})
	if res.Resumed || res.AlreadyDone {
		t.Fatalf("unexpected flags: %+v", res)
	}
	if res.Batches != 3 { // 2 + 2 + 1 records
		t.Fatalf("batches = %d, want 3", res.Batches)
	}
	if res.KeysTotal != 5 || res.RejectsTotal != 0 {
		t.Fatalf("keys=%d rejects=%d, want 5/0", res.KeysTotal, res.RejectsTotal)
	}

	store, err := LoadPublished(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := store.Get("k3")
	if !ok || v.Value != "v3" {
		t.Fatalf("k3 = %+v, want v3", v)
	}
	// Each record line is 6 bytes ("k3=v3\n"), so k3 starts at offset 18.
	if v.Version != 18 {
		t.Fatalf("k3 version = %d, want 18 (input offset)", v.Version)
	}
	if _, err := os.Stat(filepath.Join(dir, publishedName+".tmp")); !os.IsNotExist(err) {
		t.Fatal("stale temp publish file left behind")
	}
}

func TestBadRecordsIsolatedAndImportContinues(t *testing.T) {
	dir, input := writeInput(t, "good1=1\nbadline\n=nokey\n\ngood2=2\nk=v=with=equals\n")

	res := mustRun(t, Config{Dir: dir, InputPath: input, BatchSize: 2})
	if res.KeysTotal != 3 {
		t.Fatalf("keys = %d, want 3", res.KeysTotal)
	}
	if res.RejectsTotal != 2 {
		t.Fatalf("rejects = %d, want 2", res.RejectsTotal)
	}

	store, err := LoadPublished(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := store.Get("k"); v.Value != "v=with=equals" {
		t.Fatalf("k = %+v, want v=with=equals", v)
	}

	rejects, err := os.ReadFile(filepath.Join(dir, workDirName, rejectsName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rejects), "badline") || !strings.Contains(string(rejects), "empty key") {
		t.Fatalf("rejects.log missing quarantined records:\n%s", rejects)
	}
}

func TestTruncatedTailNotCommitted(t *testing.T) {
	// The last line has no terminating newline: it may be a torn write,
	// so it must be quarantined, never committed as half a record.
	dir, input := writeInput(t, "a=1\nb=2\nc=3")

	res := mustRun(t, Config{Dir: dir, InputPath: input, BatchSize: 10})
	if res.KeysTotal != 2 || res.RejectsTotal != 1 {
		t.Fatalf("keys=%d rejects=%d, want 2/1", res.KeysTotal, res.RejectsTotal)
	}

	store, err := LoadPublished(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("c"); ok {
		t.Fatal("truncated tail record c=3 was committed")
	}

	rejects, _ := os.ReadFile(filepath.Join(dir, workDirName, rejectsName))
	if !strings.Contains(string(rejects), "truncated tail") {
		t.Fatalf("tail not quarantined:\n%s", rejects)
	}

	// The confirmed offset must stop before the partial line.
	ck, err := loadCheckpoint(filepath.Join(dir, workDirName))
	if err != nil {
		t.Fatal(err)
	}
	if ck.Offset != int64(len("a=1\nb=2\n")) {
		t.Fatalf("confirmed offset = %d, want %d", ck.Offset, len("a=1\nb=2\n"))
	}
}

func TestCrashBeforeCheckpointResumesWithoutVersionGrowth(t *testing.T) {
	content := makeRecords(10)

	// Clean reference run.
	cleanDir, cleanInput := writeInput(t, content)
	mustRun(t, Config{Dir: cleanDir, InputPath: cleanInput, BatchSize: 3})
	cleanSnapshot, err := os.ReadFile(filepath.Join(cleanDir, publishedName))
	if err != nil {
		t.Fatal(err)
	}

	// Crash after the 2nd batch's WAL fsync but before its checkpoint:
	// the WAL is ahead of the checkpoint and the last batch is replayed.
	dir, input := writeInput(t, content)
	_, err, crashed := runCatching(Config{
		Dir: dir, InputPath: input, BatchSize: 3,
		Injector: &crashAt{point: FaultBeforeCheckpoint, nth: 2},
	})
	if !crashed || err != nil {
		t.Fatalf("crashed=%v err=%v, want crash", crashed, err)
	}

	res := mustRun(t, Config{Dir: dir, InputPath: input, BatchSize: 3})
	if !res.Resumed {
		t.Fatal("expected a resumed run")
	}

	snapshot, err := os.ReadFile(filepath.Join(dir, publishedName))
	if err != nil {
		t.Fatal(err)
	}
	if string(snapshot) != string(cleanSnapshot) {
		t.Fatalf("recovered snapshot differs from clean run:\n%s\nvs\n%s", snapshot, cleanSnapshot)
	}

	// Replaying the last batch must not grow versions: k3 still carries
	// its input offset as version, not a bumped counter.
	store, err := LoadPublished(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := store.Get("k3"); v.Version != 18 {
		t.Fatalf("k3 version = %d, want 18 after replay", v.Version)
	}
}

func TestCrashAfterCheckpointResumes(t *testing.T) {
	dir, input := writeInput(t, makeRecords(7))

	_, _, crashed := runCatching(Config{
		Dir: dir, InputPath: input, BatchSize: 2,
		Injector: &crashAt{point: FaultAfterCheckpoint, nth: 2},
	})
	if !crashed {
		t.Fatal("expected crash")
	}

	res := mustRun(t, Config{Dir: dir, InputPath: input, BatchSize: 2})
	if !res.Resumed || res.KeysTotal != 7 {
		t.Fatalf("resumed=%v keys=%d, want true/7", res.Resumed, res.KeysTotal)
	}
}

func TestCrashBeforePublishKeepsUnfinishedBatchInvisible(t *testing.T) {
	// Fresh directory: nothing may be visible after the crash.
	dir, input := writeInput(t, makeRecords(4))
	_, _, crashed := runCatching(Config{
		Dir: dir, InputPath: input, BatchSize: 2,
		Injector: &crashAt{point: FaultBeforePublish, nth: 1},
	})
	if !crashed {
		t.Fatal("expected crash")
	}
	if _, err := LoadPublished(dir); !errors.Is(err, ErrNoPublished) {
		t.Fatalf("LoadPublished = %v, want ErrNoPublished", err)
	}

	res := mustRun(t, Config{Dir: dir, InputPath: input, BatchSize: 2})
	if res.KeysTotal != 4 {
		t.Fatalf("keys = %d, want 4", res.KeysTotal)
	}

	// A previously published store must survive a crashed re-import.
	dir2, input2 := writeInput(t, makeRecords(4))
	old := []byte(`{"old":{"value":"v","version":1}}`)
	if err := os.WriteFile(filepath.Join(dir2, publishedName), old, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, crashed := runCatching(Config{
		Dir: dir2, InputPath: input2, BatchSize: 2,
		Injector: &crashAt{point: FaultBeforePublish, nth: 1},
	}); !crashed {
		t.Fatal("expected crash")
	}
	got, err := os.ReadFile(filepath.Join(dir2, publishedName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(old) {
		t.Fatalf("previously published store was modified before publish:\n%s", got)
	}
}

func TestCrashAfterPublishIsIdempotent(t *testing.T) {
	dir, input := writeInput(t, makeRecords(4))

	_, _, crashed := runCatching(Config{
		Dir: dir, InputPath: input, BatchSize: 2,
		Injector: &crashAt{point: FaultAfterPublish, nth: 1},
	})
	if !crashed {
		t.Fatal("expected crash")
	}
	// The rename already happened: the store is visible and complete.
	store, err := LoadPublished(dir)
	if err != nil {
		t.Fatal(err)
	}
	if store.Len() != 4 {
		t.Fatalf("keys = %d, want 4", store.Len())
	}

	// Recovery republishes deterministically and marks the run done.
	mustRun(t, Config{Dir: dir, InputPath: input, BatchSize: 2})
	res := mustRun(t, Config{Dir: dir, InputPath: input, BatchSize: 2})
	if !res.AlreadyDone {
		t.Fatal("third run should report AlreadyDone")
	}
}

func TestInputChangeRejectedOnRecovery(t *testing.T) {
	crashOnce := func(t *testing.T) (dir, input string) {
		dir, input = writeInput(t, makeRecords(10))
		if _, _, crashed := runCatching(Config{
			Dir: dir, InputPath: input, BatchSize: 3,
			Injector: &crashAt{point: FaultAfterCheckpoint, nth: 1},
		}); !crashed {
			t.Fatal("expected crash")
		}
		return dir, input
	}

	t.Run("content flipped same size", func(t *testing.T) {
		dir, input := crashOnce(t)
		data, _ := os.ReadFile(input)
		data[1] = 'X' // inside the confirmed prefix
		if err := os.WriteFile(input, data, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err, _ := runCatching(Config{Dir: dir, InputPath: input, BatchSize: 3})
		if !IsInputChanged(err) {
			t.Fatalf("err = %v, want InputChangedError", err)
		}
	})

	t.Run("appended", func(t *testing.T) {
		dir, input := crashOnce(t)
		f, _ := os.OpenFile(input, os.O_WRONLY|os.O_APPEND, 0o644)
		fmt.Fprintln(f, "extra=1")
		f.Close()
		_, err, _ := runCatching(Config{Dir: dir, InputPath: input, BatchSize: 3})
		if !IsInputChanged(err) {
			t.Fatalf("err = %v, want InputChangedError", err)
		}
	})

	t.Run("mtime changed", func(t *testing.T) {
		dir, input := crashOnce(t)
		when := time.Now().Add(time.Hour)
		if err := os.Chtimes(input, when, when); err != nil {
			t.Fatal(err)
		}
		_, err, _ := runCatching(Config{Dir: dir, InputPath: input, BatchSize: 3})
		if !IsInputChanged(err) {
			t.Fatalf("err = %v, want InputChangedError", err)
		}
	})
}

func TestStoreApplyIgnoresReplayedOffsets(t *testing.T) {
	s := NewStore()
	if !s.Apply(10, "k", "v1") {
		t.Fatal("first apply should change the store")
	}
	if s.Apply(10, "k", "v1") {
		t.Fatal("replaying the same offset must be a no-op")
	}
	if s.Apply(5, "k", "older") {
		t.Fatal("applying an older offset must be a no-op")
	}
	v, _ := s.Get("k")
	if v.Version != 10 || v.Value != "v1" {
		t.Fatalf("got %+v, want {v1 10}", v)
	}
	if !s.Apply(11, "k", "v2") {
		t.Fatal("newer offset should change the store")
	}
	v, _ = s.Get("k")
	if v.Version != 11 || v.Value != "v2" {
		t.Fatalf("got %+v, want {v2 11}", v)
	}
}
