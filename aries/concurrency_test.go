package aries

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// TestConcurrentWorkload hammers the system from many goroutines —
// transactions committing, snapshots being taken, checkpoints firing —
// then crashes and recovers. Every committed write must survive; no
// goroutine may be leaked.
func TestConcurrentWorkload(t *testing.T) {
	baseline := countGoroutines()

	dir := tempDir(t)
	defer cleanup(dir)
	snapDir := filepath.Join(dir, "snaps")

	wal, store, pool, tm := openEnv(t, dir)
	sn, err := NewSnapshotter(snapDir, pool, wal)
	if err != nil {
		t.Fatalf("snapshotter: %v", err)
	}

	const workers = 8
	const txnsPerWorker = 25

	// expected[worker][i] is the value the worker's i-th committed txn
	// wrote to its private page.
	var mu sync.Mutex
	expected := make(map[uint32]string)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			page := uint32(w)
			for i := 0; i < txnsPerWorker; i++ {
				tx := tm.Begin()
				val := fmt.Sprintf("w%02d-t%03d", w, i)
				old, err := tm.ReadAt(page, 0, 8)
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
				if _, err := tm.Write(tx, page, 0, old, []byte(val)); err != nil {
					t.Errorf("write: %v", err)
					return
				}
				if err := tm.Commit(tx); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
				mu.Lock()
				expected[page] = val
				mu.Unlock()
			}
		}(w)
	}

	// Concurrent snapshots and checkpoints while the workers run.
	var ctrl sync.WaitGroup
	ctrl.Add(2)
	stop := make(chan struct{})
	go func() {
		defer ctrl.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := sn.Take(); err != nil {
					t.Errorf("snapshot: %v", err)
					return
				}
			}
		}
	}()
	go func() {
		defer ctrl.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := tm.Checkpoint(); err != nil {
					t.Errorf("checkpoint: %v", err)
					return
				}
			}
		}
	}()

	wg.Wait()
	close(stop)
	ctrl.Wait()

	// Clean shutdown of the pre-crash world: log durable, pool discarded.
	if err := wal.Flush(); err != nil {
		t.Fatalf("flush wal: %v", err)
	}
	wal.Close()
	store.Close()
	sn.Close()

	// Reboot and recover.
	wal2, store2, pool2, _ := openEnv(t, dir)
	stats, err := Recover(wal2, pool2)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if len(stats.Losers) != 0 {
		t.Fatalf("unexpected losers after clean commit workload: %v", stats.Losers)
	}

	// Every worker's final committed value must be on disk.
	mu.Lock()
	for page, want := range expected {
		if got := pageString(t, store2, page, 8); got != want {
			t.Fatalf("page %d = %q, want last committed %q", page, got, want)
		}
	}
	mu.Unlock()
	if len(expected) != workers {
		t.Fatalf("expected map has %d pages, want %d", len(expected), workers)
	}

	wal2.Close()
	store2.Close()
	assertNoLeak(t, baseline)
}

// TestConcurrentSnapshotConsistency verifies that a snapshot taken while
// transactions run always contains whole-page images (no torn pages) and
// that restore yields a state where every page holds *some* fully
// committed value.
func TestConcurrentSnapshotConsistency(t *testing.T) {
	dir := tempDir(t)
	defer cleanup(dir)
	snapDir := filepath.Join(dir, "snaps")

	wal, store, pool, tm := openEnv(t, dir)
	sn, err := NewSnapshotter(snapDir, pool, wal)
	if err != nil {
		t.Fatalf("snapshotter: %v", err)
	}

	const workers = 4
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			page := uint32(w)
			for i := 0; i < 40; i++ {
				tx := tm.Begin()
				val := fmt.Sprintf("W%dV%05d", w, i) // 8 bytes
				old, err := tm.ReadAt(page, 0, 8)
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
				if _, err := tm.Write(tx, page, 0, old, []byte(val)); err != nil {
					t.Errorf("write: %v", err)
					return
				}
				if err := tm.Commit(tx); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
			}
		}(w)
	}
	for i := 0; i < 10; i++ {
		if _, err := sn.Take(); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
	}
	wg.Wait()
	if _, err := sn.Take(); err != nil { // final delta covering the tail
		t.Fatalf("final snapshot: %v", err)
	}
	sn.Close()
	wal.Close()
	store.Close()

	// Restore and validate: each page must contain a well-formed value
	// from its worker's value domain — never a torn mix.
	rstore, err := OpenPageStore(filepath.Join(dir, "restored.db"), 64)
	if err != nil {
		t.Fatalf("open restore store: %v", err)
	}
	defer rstore.Close()
	if err := RestoreSnapshots(snapDir, rstore); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for w := 0; w < workers; w++ {
		got := pageString(t, rstore, uint32(w), 8)
		var idx int
		if _, err := fmt.Sscanf(got, fmt.Sprintf("W%dV%%05d", w), &idx); err != nil {
			t.Fatalf("page %d holds torn/foreign value %q: %v", w, got, err)
		}
	}
}

// TestLeakFreeLifecycle asserts the whole open->work->close cycle leaves
// no goroutines behind and that Close is idempotent.
func TestLeakFreeLifecycle(t *testing.T) {
	baseline := countGoroutines()

	dir := tempDir(t)
	defer cleanup(dir)
	wal, store, pool, tm := openEnv(t, dir)
	sn, err := NewSnapshotter(filepath.Join(dir, "snaps"), pool, wal)
	if err != nil {
		t.Fatalf("snapshotter: %v", err)
	}
	tx := tm.Begin()
	mustWrite(t, tm, tx, 0, "leaktest")
	if err := tm.Commit(tx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := sn.Take(); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := tm.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	// Close everything twice: Close must be idempotent.
	for i := 0; i < 2; i++ {
		if err := sn.Close(); err != nil {
			t.Fatalf("snapshotter close %d: %v", i, err)
		}
		if err := wal.Close(); err != nil {
			t.Fatalf("wal close %d: %v", i, err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("store close %d: %v", i, err)
		}
	}
	assertNoLeak(t, baseline)
}
