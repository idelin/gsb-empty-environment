package store

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"pbufsafe/testutil"
)

func TestBasicAppendCommitScan(t *testing.T) {
	sim := NewSimFile()
	st, err := Open(sim)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	payloads := [][]byte{[]byte("alpha"), []byte("beta"), []byte{0x00, 0xFF, 0x10}}
	for _, p := range payloads {
		if err := st.Append(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := st.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payloads) {
		t.Fatalf("got %d records, want %d", len(got), len(payloads))
	}
	for i := range payloads {
		if !bytes.Equal(got[i], payloads[i]) {
			t.Errorf("record %d: got %q want %q", i, got[i], payloads[i])
		}
	}
	if st.Stats().Records != 3 {
		t.Errorf("stats records = %d, want 3", st.Stats().Records)
	}
}

// TestPowerLossRecovery: committed records survive a sudden power cut;
// staged-but-never-synced records are rolled back by recovery.
func TestPowerLossRecovery(t *testing.T) {
	sim := NewSimFile()
	st, err := Open(sim)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Append([]byte("txn-1")); err != nil {
		t.Fatal(err)
	}
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}
	committedOffset := st.Stats().Offset
	// Stage two more records but never commit: power cuts now.
	if err := st.Append([]byte("txn-2-uncommitted")); err != nil {
		t.Fatal(err)
	}
	if err := st.Append([]byte("txn-3-uncommitted")); err != nil {
		t.Fatal(err)
	}
	// The OS writeback daemon flushes a torn prefix of the dirty pages
	// (the first 10 bytes of txn-2's header) to disk just before the cut.
	sim.FlushPartial(committedOffset + 10)
	sim.Crash()
	sim.Reopen()

	st2, err := Open(sim)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	got, err := st2.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0]) != "txn-1" {
		t.Fatalf("after crash got %q, want only [txn-1]", got)
	}
	if !st2.Stats().Recovered {
		t.Error("expected Stats().Recovered to report the rollback")
	}
	// The store must be writable after recovery (self-healing).
	if err := st2.Append([]byte("txn-4")); err != nil {
		t.Fatal(err)
	}
	if err := st2.Commit(); err != nil {
		t.Fatal(err)
	}
	got, _ = st2.Scan()
	if len(got) != 2 || string(got[1]) != "txn-4" {
		t.Fatalf("post-recovery append broken: %q", got)
	}
}

// TestCrashMidRecord simulates power loss in the middle of a record write:
// the torn tail must be detected and truncated on recovery.
func TestCrashMidRecord(t *testing.T) {
	sim := NewSimFile()
	st, _ := Open(sim)
	st.Append([]byte("good-1"))
	st.Commit()

	// Hand-craft a torn record directly on the file: valid header start,
	// then the power dies halfway through the payload.
	torn := encodeRecord([]byte("this payload never fully reached the disk"))
	torn = torn[:len(torn)/2]
	off := st.Stats().Offset
	if _, err := sim.WriteAt(torn, off); err != nil {
		t.Fatal(err)
	}
	sim.Sync() // the torn bytes DID reach the disk before the cut
	sim.Crash()
	sim.Reopen()

	st2, err := Open(sim)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	got, _ := st2.Scan()
	if len(got) != 1 || string(got[0]) != "good-1" {
		t.Fatalf("torn tail not rolled back: %q", got)
	}
	if st2.Stats().Offset != off {
		t.Errorf("offset = %d, want rollback to %d", st2.Stats().Offset, off)
	}
}

// TestCorruptPayloadRecovery flips a bit in a synced record and expects
// recovery to roll back to the record before it.
func TestCorruptPayloadRecovery(t *testing.T) {
	sim := NewSimFile()
	st, _ := Open(sim)
	st.Append([]byte("first"))
	st.Append([]byte("second"))
	st.Commit()

	// Corrupt one payload byte of the second record on the durable image.
	first := encodeRecord([]byte("first"))
	corruptAt := int64(len(first)) + headerSize // payload of record 2
	if _, err := sim.WriteAt([]byte{0xFF}, corruptAt); err != nil {
		t.Fatal(err)
	}
	sim.Sync()
	sim.Crash()
	sim.Reopen()

	st2, err := Open(sim)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	got, _ := st2.Scan()
	if len(got) != 1 || string(got[0]) != "first" {
		t.Fatalf("CRC violation not rolled back: %q", got)
	}
}

// TestDiskFullRollback: ENOSPC mid-append must roll the file back to the
// last consistent offset and leave the store usable.
func TestDiskFullRollback(t *testing.T) {
	sim := NewSimFile()
	st, _ := Open(sim)
	defer st.Close()
	if err := st.Append([]byte("small")); err != nil {
		t.Fatal(err)
	}
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}
	goodOffset := st.Stats().Offset

	// Leave room for only 8 more bytes: a 4 KiB append must fail.
	sim.FullAfter = goodOffset + 8
	err := st.Append(make([]byte, 4096))
	if err == nil {
		t.Fatal("expected ENOSPC, got nil")
	}
	// Offset unchanged, file rolled back to the consistent prefix.
	if st.Stats().Offset != goodOffset {
		t.Errorf("offset = %d, want %d after rollback", st.Stats().Offset, goodOffset)
	}
	if sim.Size() != goodOffset {
		t.Errorf("file size = %d, want %d (torn bytes rolled back)", sim.Size(), goodOffset)
	}
	// Store still works once space is freed.
	sim.FullAfter = 0
	if err := st.Append([]byte("after-free")); err != nil {
		t.Fatal(err)
	}
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Scan()
	if len(got) != 2 || string(got[1]) != "after-free" {
		t.Fatalf("post-ENOSPC append broken: %q", got)
	}
}

// TestBatchAtomicity: a batch interrupted by a crash loses ALL its records,
// never a prefix of them.
func TestBatchAtomicity(t *testing.T) {
	sim := NewSimFile()
	st, _ := Open(sim)
	st.Append([]byte("committed"))
	st.Commit()

	batch := [][]byte{[]byte("b1"), []byte("b2"), []byte("b3")}
	if err := st.AppendBatch(batch); err != nil {
		t.Fatal(err)
	}
	// Only part of the batch reaches the disk before the power cut.
	full := sim.volatile
	cut := st.Stats().Offset - 7 // mid-record of the batch
	sim.durable = append([]byte(nil), full[:cut]...)
	sim.Crash()
	sim.Reopen()

	st2, err := Open(sim)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	got, _ := st2.Scan()
	if len(got) != 1 || string(got[0]) != "committed" {
		t.Fatalf("partial batch survived: %q", got)
	}
}

// TestConcurrentAppendCommit hammers the store from many goroutines and
// verifies no record is lost or corrupted, and no goroutine leaks.
func TestConcurrentAppendCommit(t *testing.T) {
	defer testutil.AssertNoGoroutineLeak(t)()
	sim := NewSimFile()
	st, err := Open(sim)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 16
	const perWorker = 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				p := []byte(fmt.Sprintf("w%02d-%03d", id, i))
				if err := st.Append(p); err != nil {
					t.Error(err)
					return
				}
				if i%10 == 0 {
					if err := st.Commit(); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := st.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != workers*perWorker {
		t.Fatalf("got %d records, want %d", len(got), workers*perWorker)
	}
	seen := make(map[string]bool, len(got))
	for _, p := range got {
		if seen[string(p)] {
			t.Fatalf("duplicate record %q", p)
		}
		seen[string(p)] = true
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	// Use after close must fail cleanly.
	if err := st.Append([]byte("x")); err != ErrClosed {
		t.Errorf("append after close: got %v, want ErrClosed", err)
	}
}

// TestRecoveryOnRealDisk exercises the os.File adapter end to end.
func TestRecoveryOnRealDisk(t *testing.T) {
	dir, err := ioutil.TempDir("", "waltest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "wal.log")
	st, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := st.Append([]byte(fmt.Sprintf("rec-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	got, _ := st2.Scan()
	if len(got) != 5 {
		t.Fatalf("reopened log has %d records, want 5", len(got))
	}
}

// TestENOSPCIsSyscallError documents that the injector surfaces the real
// errno so callers can special-case disk full.
func TestENOSPCIsSyscallError(t *testing.T) {
	sim := NewSimFile()
	sim.FullAfter = 1
	_, err := sim.WriteAt([]byte("toolong"), 0)
	if err != syscall.ENOSPC {
		t.Fatalf("got %v, want syscall.ENOSPC", err)
	}
}
