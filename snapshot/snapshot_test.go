package snapshot

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "snapshot-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPutGetDelete(t *testing.T) {
	s := NewStore()
	s.Put("a", []byte("1"))
	s.Put("b", []byte("2"))
	if v, ok := s.Get("a"); !ok || string(v) != "1" {
		t.Fatalf("Get(a) = %q, %v", v, ok)
	}
	s.Del("a")
	if _, ok := s.Get("a"); ok {
		t.Fatal("expected a to be deleted")
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
	// value is copied: mutating the caller's slice must not affect the store
	raw := []byte("xyz")
	s.Put("c", raw)
	raw[0] = 'Q'
	if v, _ := s.Get("c"); string(v) != "xyz" {
		t.Fatalf("stored value aliases caller buffer: %q", v)
	}
}

func TestDeltaSinceAndCompaction(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	a, err := NewArchiver(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore()
	s.Put("k1", []byte("v1"))
	v2 := s.Put("k2", []byte("v2"))

	muts, to, err := s.DeltaSince(0)
	if err != nil || len(muts) != 2 || to != v2 {
		t.Fatalf("DeltaSince(0): %d mutations, to=%d, err=%v", len(muts), to, err)
	}
	cv, err := a.Checkpoint(s)
	if err != nil {
		t.Fatal(err)
	}
	if cv != v2 {
		t.Fatalf("checkpoint version = %d, want %d", cv, v2)
	}
	// versions folded into the checkpoint are compacted away
	if _, _, err := s.DeltaSince(cv - 1); err != ErrCompacted {
		t.Fatalf("want ErrCompacted, got %v", err)
	}
	// journal compacted
	if s.JournalLen() != 0 {
		t.Fatalf("journal not compacted: %d entries", s.JournalLen())
	}
	// new mutations are still delta-able from the checkpoint version
	s.Put("k3", []byte("v3"))
	muts, _, err = s.DeltaSince(cv)
	if err != nil || len(muts) != 1 || muts[0].Key != "k3" {
		t.Fatalf("DeltaSince(cv): %+v, err=%v", muts, err)
	}
}

func TestCheckpointRestoreRoundTrip(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	a, _ := NewArchiver(dir)

	s := NewStore()
	for i := 0; i < 1000; i++ {
		s.Put(fmt.Sprintf("key-%04d", i), []byte(strconv.Itoa(i)))
	}
	for i := 0; i < 500; i += 2 {
		s.Del(fmt.Sprintf("key-%04d", i))
	}
	want, wv := s.Snapshot()
	cv, err := a.Checkpoint(s)
	if err != nil {
		t.Fatal(err)
	}
	if cv != wv {
		t.Fatalf("checkpoint version = %d, want %d", cv, wv)
	}

	restored, rv, err := a.Load()
	if err != nil {
		t.Fatal(err)
	}
	if rv != wv {
		t.Fatalf("restored version = %d, want %d", rv, wv)
	}
	got, _ := restored.Snapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored state mismatch: got %d keys, want %d", len(got), len(want))
	}
}

func TestIncrementalDeltaArchive(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	a, _ := NewArchiver(dir)

	s := NewStore()
	s.Put("base", []byte("0"))
	cv, err := a.Checkpoint(s)
	if err != nil {
		t.Fatal(err)
	}

	// two incremental delta rounds
	s.Put("d1", []byte("1"))
	s.Del("base")
	to1, err := a.ArchiveDelta(s, cv)
	if err != nil {
		t.Fatal(err)
	}
	s.Put("d2", []byte("2"))
	s.Put("d1", []byte("1b"))
	to2, err := a.ArchiveDelta(s, to1)
	if err != nil {
		t.Fatal(err)
	}
	// no-op delta round writes no file
	if _, err := a.ArchiveDelta(s, to2); err != nil {
		t.Fatal(err)
	}
	ckpts, deltas, err := a.scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(ckpts) != 1 || len(deltas) != 2 {
		t.Fatalf("archive layout: %d checkpoints, %d deltas", len(ckpts), len(deltas))
	}

	restored, rv, err := a.Load()
	if err != nil {
		t.Fatal(err)
	}
	if rv != to2 {
		t.Fatalf("restored version = %d, want %d", rv, to2)
	}
	want, _ := s.Snapshot()
	got, _ := restored.Snapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incremental restore mismatch: got %v, want %v", got, want)
	}
	if _, ok := got["base"]; ok {
		t.Fatal("tombstone not replayed: base should be deleted")
	}
}

func TestLoadWithoutCheckpoint(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	a, _ := NewArchiver(dir)
	if _, _, err := a.Load(); err != ErrNoCheckpoint {
		t.Fatalf("want ErrNoCheckpoint, got %v", err)
	}
}

func TestCorruptArchiveRejected(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	a, _ := NewArchiver(dir)
	s := NewStore()
	s.Put("k", []byte("v"))
	cv, err := a.Checkpoint(s)
	if err != nil {
		t.Fatal(err)
	}
	// truncate the checkpoint file mid-payload
	path := filepath.Join(dir, checkpointName(cv))
	raw, _ := ioutil.ReadFile(path)
	if err := ioutil.WriteFile(path, raw[:len(raw)-2], 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Load(); err == nil {
		t.Fatal("expected error loading truncated checkpoint")
	}
	// bad magic
	if err := ioutil.WriteFile(path, []byte("XXXX-corrupted"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Load(); err == nil {
		t.Fatal("expected error loading bad-magic checkpoint")
	}
}

func TestDoubleCheckpointRejected(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	a, _ := NewArchiver(dir)
	s := NewStore()
	s.Put("k", []byte("v"))
	base, overlay, v, err := s.beginCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.beginCheckpoint(); err != ErrCheckpointInProgress {
		t.Fatalf("want ErrCheckpointInProgress, got %v", err)
	}
	s.finishCheckpoint(MergeLayers(base, overlay), v)
	// after finishing, a fresh checkpoint works
	if _, err := a.Checkpoint(s); err != nil {
		t.Fatal(err)
	}
}

func TestAbortCheckpointRestoresState(t *testing.T) {
	s := NewStore()
	s.Put("a", []byte("1"))
	base, overlay, _, err := s.beginCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	_ = base
	_ = overlay
	// writes racing the failed checkpoint
	s.Put("b", []byte("2"))
	s.Del("a")
	s.abortCheckpoint()
	if _, ok := s.Get("a"); ok {
		t.Fatal("tombstone lost after abort")
	}
	if v, ok := s.Get("b"); !ok || string(v) != "2" {
		t.Fatal("racing write lost after abort")
	}
	if s.PendingMerge() {
		t.Fatal("checkpoint flag not cleared after abort")
	}
}

// TestConcurrentCheckpoint hammers the store from many goroutines while
// checkpoints and delta archives run concurrently. Run with -race.
func TestConcurrentCheckpoint(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	a, _ := NewArchiver(dir)
	s := NewStore()

	const workers = 8
	const opsPerWorker = 500

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// checkpoint + delta archiver goroutine
	var archErr error
	var archMu sync.Mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		last := uint64(0)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if v, err := a.Checkpoint(s); err != nil && err != ErrCheckpointInProgress {
				archMu.Lock()
				archErr = err
				archMu.Unlock()
				return
			} else {
				last = v
			}
			if _, err := a.ArchiveDelta(s, last); err != nil && err != ErrCompacted {
				archMu.Lock()
				archErr = err
				archMu.Unlock()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// writer/reader workers
	var writers sync.WaitGroup
	for w := 0; w < workers; w++ {
		writers.Add(1)
		go func(id int) {
			defer writers.Done()
			for i := 0; i < opsPerWorker; i++ {
				key := fmt.Sprintf("w%02d-k%04d", id, i%64)
				s.Put(key, []byte(fmt.Sprintf("val-%d-%d", id, i)))
				if i%7 == 0 {
					s.Del(key)
				}
				if i%3 == 0 {
					s.Get(key)
				}
				if i%11 == 0 {
					s.Snapshot()
				}
			}
		}(w)
	}

	writers.Wait()
	close(stop)
	wg.Wait()

	archMu.Lock()
	if archErr != nil {
		t.Fatalf("archiver error: %v", archErr)
	}
	archMu.Unlock()

	// final checkpoint + restore must reproduce the exact final state
	if _, err := a.Checkpoint(s); err != nil {
		t.Fatal(err)
	}
	restored, _, err := a.Load()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := s.Snapshot()
	got, _ := restored.Snapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("concurrent restore mismatch: got %d keys, want %d", len(got), len(want))
	}
}

// TestNoGoroutineOrStateLeak asserts the engine spawns no goroutines and
// leaves no residual internal state after checkpoints.
func TestNoGoroutineOrStateLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	dir := tempDir(t)
	defer os.RemoveAll(dir)
	a, _ := NewArchiver(dir)
	s := NewStore()
	for round := 0; round < 20; round++ {
		for i := 0; i < 100; i++ {
			s.Put(fmt.Sprintf("k%d", i), []byte("v"))
		}
		if _, err := a.Checkpoint(s); err != nil {
			t.Fatal(err)
		}
	}
	// internal state fully folded: no pending merge, empty journal
	if s.PendingMerge() {
		t.Fatal("leaked pending merge")
	}
	if s.JournalLen() != 0 {
		t.Fatalf("leaked journal entries: %d", s.JournalLen())
	}

	// let any stray goroutine surface
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestSnapshotConsistencyUnderWrite(t *testing.T) {
	s := NewStore()
	s.Put("x", []byte("0"))
	state1, v1 := s.Snapshot()
	s.Put("x", []byte("1"))
	// earlier snapshot must be unaffected by later writes
	if string(state1["x"]) != "0" {
		t.Fatalf("snapshot mutated by later write: %q", state1["x"])
	}
	state2, v2 := s.Snapshot()
	if v2 <= v1 || string(state2["x"]) != "1" {
		t.Fatalf("bad second snapshot: v=%d x=%q", v2, state2["x"])
	}
}

func TestValueIsolation(t *testing.T) {
	s := NewStore()
	s.Put("k", []byte("abc"))
	v, _ := s.Get("k")
	if !bytes.Equal(v, []byte("abc")) {
		t.Fatalf("Get = %q", v)
	}
}
