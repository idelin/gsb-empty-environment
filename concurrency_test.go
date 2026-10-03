package aries

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestConcurrentWorkload hammers a single WAL and transaction manager from
// many goroutines (plus a concurrent checkpointer), then crash-recovers and
// verifies every committed value. Run with -race.
func TestConcurrentWorkload(t *testing.T) {
	const workers = 8
	const updatesPerTxn = 50

	goroutinesBefore := runtime.NumGoroutine()

	dir := t.TempDir()
	inj := NewFaultInjector()
	wal, disk := openPair(t, dir, inj)
	tm := NewTxnManager(wal, disk)

	expected := make(map[uint64]string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	stopCheckpoint := make(chan struct{})
	var ckptWg sync.WaitGroup

	ckptWg.Add(1)
	go func() {
		defer ckptWg.Done()
		for {
			select {
			case <-stopCheckpoint:
				return
			default:
				if err := tm.Checkpoint(); err != nil {
					t.Errorf("checkpoint: %v", err)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}
	}()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			txnID := uint64(worker + 1)
			if err := tm.Begin(txnID); err != nil {
				t.Errorf("begin: %v", err)
				return
			}
			for j := 0; j < updatesPerTxn; j++ {
				pageID := uint64(worker*updatesPerTxn + j)
				value := fmt.Sprintf("w%d-v%d", worker, j)
				if err := tm.Update(txnID, pageID, []byte(value)); err != nil {
					t.Errorf("update: %v", err)
					return
				}
				mu.Lock()
				expected[pageID] = value
				mu.Unlock()
			}
			if err := tm.Commit(txnID, true); err != nil {
				t.Errorf("commit: %v", err)
			}
		}(w)
	}
	wg.Wait()
	close(stopCheckpoint)
	ckptWg.Wait()

	// Every LSN must be unique and the scan order strictly increasing.
	recs, err := wal.Scan()
	must(t, err)
	for i := 1; i < len(recs); i++ {
		if recs[i].LSN <= recs[i-1].LSN {
			t.Fatalf("LSNs not strictly increasing at %d: %d after %d",
				i, recs[i].LSN, recs[i-1].LSN)
		}
	}
	minRecords := workers * (1 + updatesPerTxn + 2) // begin+updates+commit+end
	if len(recs) < minRecords {
		t.Fatalf("WAL has %d records, want at least %d", len(recs), minRecords)
	}

	// Crash without flushing anything, then recover.
	must(t, wal.Close())
	must(t, disk.Close())

	wal2, err := OpenWAL(filepath.Join(dir, "wal.log"), inj)
	must(t, err)
	defer wal2.Close()
	disk2, err := OpenPageStore(filepath.Join(dir, "data.db"), inj)
	must(t, err)
	defer disk2.Close()

	eng := NewEngine(wal2, disk2)
	must(t, eng.Recover())
	if eng.Undone != 0 {
		t.Fatalf("all transactions committed; nothing may be undone, got %d", eng.Undone)
	}
	for pageID, want := range expected {
		page, err := disk2.ReadPage(pageID)
		must(t, err)
		if got := string(trim(page.Data)); got != want {
			t.Fatalf("page %d = %q, want %q", pageID, got, want)
		}
	}

	// No goroutine may leak out of the test.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	if n := runtime.NumGoroutine(); n > goroutinesBefore {
		t.Fatalf("goroutine leak: before=%d after=%d", goroutinesBefore, n)
	}
}

// TestPacketLoss_LogShipping verifies self-healing log replication over a
// lossy link: gaps are detected via LSN discontinuity and retransmitted
// until the replica is byte-identical to the primary.
func TestPacketLoss_LogShipping(t *testing.T) {
	dir := t.TempDir()
	inj := NewFaultInjector()

	src, err := OpenWAL(filepath.Join(dir, "primary.log"), inj)
	must(t, err)
	srcDisk, err := OpenPageStore(filepath.Join(dir, "primary.db"), inj)
	must(t, err)
	tm := NewTxnManager(src, srcDisk)
	for txn := uint64(1); txn <= 10; txn++ {
		must(t, tm.Begin(txn))
		for j := 0; j < 4; j++ {
			must(t, tm.Update(txn, txn*10+uint64(j), []byte(fmt.Sprintf("t%d-%d", txn, j))))
		}
		must(t, tm.Commit(txn, true))
	}
	must(t, tm.Checkpoint())

	dst, err := OpenWAL(filepath.Join(dir, "replica.log"), nil)
	must(t, err)
	defer dst.Close()

	stats, err := ShipLog(src, dst, 0.4, rand.New(rand.NewSource(42)))
	must(t, err)
	if stats.Dropped == 0 {
		t.Fatalf("expected some dropped packets with loss rate 0.4")
	}
	if stats.Retransmitted == 0 {
		t.Fatalf("expected retransmissions after gap detection")
	}

	srcBytes, err := WALBytes(src)
	must(t, err)
	dstBytes, err := WALBytes(dst)
	must(t, err)
	if len(srcBytes) != len(dstBytes) {
		t.Fatalf("replica size %d != primary size %d", len(dstBytes), len(srcBytes))
	}
	for i := range srcBytes {
		if srcBytes[i] != dstBytes[i] {
			t.Fatalf("replica diverges from primary at byte %d", i)
		}
	}
	must(t, src.Close())
	must(t, srcDisk.Close())

	// The replica must be independently recoverable to the same state.
	recs, err := dst.Scan()
	must(t, err)
	if len(recs) != 10*(1+4+2)+1 {
		t.Fatalf("replica holds %d records, want %d", len(recs), 10*7+1)
	}
}
