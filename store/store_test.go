package store

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func payloads(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte(fmt.Sprintf("record-%04d-%s", i, strings.Repeat("x", i%50)))
	}
	return out
}

func openMem(t *testing.T) (*Store, *CrashStorage) {
	cs := NewCrashStorage(NewMemStorage())
	st, err := Open(cs)
	if err != nil {
		t.Fatal(err)
	}
	return st, cs
}

func appendAll(t *testing.T, st *Store, recs [][]byte) {
	t.Helper()
	for _, r := range recs {
		if err := st.Append(r); err != nil {
			t.Fatalf("Append(%q): %v", r, err)
		}
	}
}

func TestBasicAppendAndRecover(t *testing.T) {
	st, cs := openMem(t)
	recs := payloads(10)
	appendAll(t, st, recs)

	// Simulate crash + restart: reopen on the same device.
	st2, err := Open(cs)
	if err != nil {
		t.Fatal(err)
	}
	got := st2.Records()
	if len(got) != len(recs) {
		t.Fatalf("recovered %d records, want %d", len(got), len(recs))
	}
	for i := range recs {
		if !bytes.Equal(got[i], recs[i]) {
			t.Fatalf("record %d mismatch", i)
		}
	}
}

func TestPowerLossMidWriteRollsBack(t *testing.T) {
	st, cs := openMem(t)
	recs := payloads(5)
	appendAll(t, st, recs)
	committedBytes := st.CommittedBytes()

	// Next write is cut after 3 bytes: torn record lands on "disk".
	cs.ArmPowerLoss(3)
	err := st.Append([]byte("victim-record-that-never-commits"))
	if err != ErrPowerLoss {
		t.Fatalf("err = %v, want ErrPowerLoss", err)
	}
	// In-memory state rolled back.
	if st.Count() != len(recs) {
		t.Fatalf("count = %d, want %d after rollback", st.Count(), len(recs))
	}
	if st.CommittedBytes() != committedBytes {
		t.Fatalf("tail moved after rollback: %d -> %d", committedBytes, st.CommittedBytes())
	}
	// On-disk file rolled back too, and recovery sees exactly the old set.
	if cs.Size() != committedBytes {
		t.Fatalf("device size = %d, want rolled-back %d", cs.Size(), committedBytes)
	}
	st2, err := Open(cs)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Count() != len(recs) {
		t.Fatalf("recovered %d records, want %d", st2.Count(), len(recs))
	}
	// Self-heal: appends work again after the fault.
	if err := st.Append([]byte("after-heal")); err != nil {
		t.Fatalf("append after power loss: %v", err)
	}
}

func TestPowerLossAtEveryTearPoint(t *testing.T) {
	// For every possible tear budget, the store must stay consistent:
	// either the record is fully committed or fully absent after recovery.
	victim := []byte("tear-me-at-every-possible-byte-offset")
	for budget := 0; budget < 60; budget++ {
		cs := NewCrashStorage(NewMemStorage())
		st, err := Open(cs)
		if err != nil {
			t.Fatal(err)
		}
		base := payloads(3)
		appendAll(t, st, base)

		cs.ArmPowerLoss(budget)
		writeErr := st.Append(victim)

		st2, err := Open(cs)
		if err != nil {
			t.Fatalf("budget %d: recover: %v", budget, err)
		}
		got := st2.Records()
		want := len(base)
		if writeErr == nil {
			want++ // write survived the "cut" because budget >= record size
		}
		if len(got) != want {
			t.Fatalf("budget %d: recovered %d records, want %d (writeErr=%v)", budget, len(got), want, writeErr)
		}
		for i := range base {
			if !bytes.Equal(got[i], base[i]) {
				t.Fatalf("budget %d: base record %d corrupted", budget, i)
			}
		}
		if writeErr == nil && !bytes.Equal(got[len(got)-1], victim) {
			t.Fatalf("budget %d: victim record corrupted", budget)
		}
	}
}

func TestDiskFullRollbackAndSelfHeal(t *testing.T) {
	st, cs := openMem(t)
	base := payloads(4)
	appendAll(t, st, base)
	committed := st.CommittedBytes()

	cs.SetDiskFull(true)
	for i := 0; i < 3; i++ {
		if err := st.Append([]byte("no-space")); err != ErrDiskFull {
			t.Fatalf("err = %v, want ErrDiskFull", err)
		}
	}
	if st.Count() != len(base) || st.CommittedBytes() != committed {
		t.Fatal("disk-full writes must not alter committed state")
	}

	cs.SetDiskFull(false) // operator freed space: store self-heals
	if err := st.Append([]byte("space-again")); err != nil {
		t.Fatalf("append after disk healed: %v", err)
	}
	st2, err := Open(cs)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Count() != len(base)+1 {
		t.Fatalf("recovered %d, want %d", st2.Count(), len(base)+1)
	}
}

func TestCorruptCrcRolledBackOnRecovery(t *testing.T) {
	cs := NewCrashStorage(NewMemStorage())
	st, err := Open(cs)
	if err != nil {
		t.Fatal(err)
	}
	appendAll(t, st, payloads(3))
	// Corrupt the last record's payload directly on the device,
	// bypassing the store (models bit rot / torn sector).
	size := cs.Size()
	buf := make([]byte, 1)
	if _, err := cs.ReadAt(buf, size-1); err != nil {
		t.Fatal(err)
	}
	buf[0] ^= 0xff
	if _, err := cs.Inner.WriteAt(buf, size-1); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(cs)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Count() != 2 {
		t.Fatalf("recovered %d records, want 2 (corrupt tail rolled back)", st2.Count())
	}
	// The corrupt tail must have been truncated away: file ends at the
	// last good record.
	if cs.Size() != st2.CommittedBytes() {
		t.Fatalf("device size %d != committed %d after self-heal", cs.Size(), st2.CommittedBytes())
	}
}

func TestLossyTransportWithRetryDeliversAll(t *testing.T) {
	tr := NewLossyTransport(0.5, 12345) // 50% packet loss
	sink := NewMemStorage()
	st, err := Open(sink)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex

	frames := payloads(50)
	for i, frame := range frames {
		delivered := false
		for attempt := 0; attempt < 1000 && !delivered; attempt++ {
			err := tr.Send(frame, func(f []byte) error {
				mu.Lock()
				defer mu.Unlock()
				return st.Append(f)
			})
			if err == ErrPacketLoss {
				continue // sender retries, exactly-once via retry loop
			}
			if err != nil {
				t.Fatalf("frame %d: %v", i, err)
			}
			delivered = true
		}
		if !delivered {
			t.Fatalf("frame %d never delivered", i)
		}
	}
	if st.Count() != len(frames) {
		t.Fatalf("delivered %d frames, want %d", st.Count(), len(frames))
	}
	sent, dropped := tr.Stats()
	if dropped == 0 {
		t.Fatal("expected some drops at 50% loss")
	}
	if sent != len(frames) {
		t.Fatalf("delivered %d, want %d", sent, len(frames))
	}
	t.Logf("lossy link: %d delivered, %d dropped, all recovered via retry", sent, dropped)
}

func TestConcurrentAppendsConsistent(t *testing.T) {
	st, cs := openMem(t)
	const workers = 16
	const perWorker = 50

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				rec := []byte(fmt.Sprintf("w%02d-%04d", w, i))
				if err := st.Append(rec); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	if st.Count() != workers*perWorker {
		t.Fatalf("count = %d, want %d", st.Count(), workers*perWorker)
	}
	// Recovery must reproduce exactly the committed set.
	st2, err := Open(cs)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Count() != workers*perWorker {
		t.Fatalf("recovered %d, want %d", st2.Count(), workers*perWorker)
	}
	seen := make(map[string]bool)
	for _, r := range st2.Records() {
		if seen[string(r)] {
			t.Fatalf("duplicate record %q", r)
		}
		seen[string(r)] = true
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("unique records = %d, want %d", len(seen), workers*perWorker)
	}
}

func TestConcurrentFaultInjectionAndReads(t *testing.T) {
	st, cs := openMem(t)
	var wg sync.WaitGroup
	var readersWG sync.WaitGroup
	stop := make(chan struct{})

	// Readers never see a torn state.
	for r := 0; r < 4; r++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				runtime.Gosched()
				recs := st.Records()
				n := st.Count() // taken after; appends only grow the log
				if n < len(recs) {
					t.Errorf("torn read: count %d but %d records", n, len(recs))
					return
				}
			}
		}()
	}
	// Writers with random faults armed.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if i%7 == 0 {
					cs.ArmPowerLoss(i % 5)
				}
				if i%11 == 0 {
					cs.SetDiskFull(true)
				}
				err := st.Append([]byte(fmt.Sprintf("w%d-%d", w, i)))
				if i%11 == 0 {
					cs.SetDiskFull(false)
				}
				if err != nil && err != ErrPowerLoss && err != ErrDiskFull {
					t.Errorf("unexpected error: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	readersWG.Wait()

	// After the storm, recovery must succeed and match the in-memory view.
	st2, err := Open(cs)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Count() != st.Count() {
		t.Fatalf("recovered %d, in-memory %d", st2.Count(), st.Count())
	}
}

func TestNoGoroutineLeaks(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		st, _ := openMem(t)
		appendAll(t, st, payloads(5))
		var wg sync.WaitGroup
		for w := 0; w < 8; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				st.Records()
			}()
		}
		wg.Wait()
	}
	// Allow the runtime time to reap anything pending (race detector and
	// GC keep transient helpers alive briefly).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

func TestFileStoreEndToEnd(t *testing.T) {
	dir, err := ioutil.TempDir("", "waltest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := dir + "/wal.log"
	fs, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(fs)
	if err != nil {
		t.Fatal(err)
	}
	recs := payloads(20)
	appendAll(t, st, recs)
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen from disk: full recovery.
	fs2, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fs2.Close()
	st2, err := Open(fs2)
	if err != nil {
		t.Fatal(err)
	}
	got := st2.Records()
	if len(got) != len(recs) {
		t.Fatalf("recovered %d, want %d", len(got), len(recs))
	}
	for i := range recs {
		if !bytes.Equal(got[i], recs[i]) {
			t.Fatalf("record %d mismatch after file recovery", i)
		}
	}
}
