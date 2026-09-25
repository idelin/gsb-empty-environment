package idedup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var errInjected = errors.New("injected crash")

// armOnce returns a fault hook that fires exactly once, at the named point.
func armOnce(point string) func(string) error {
	fired := false
	return func(p string) error {
		if !fired && p == point {
			fired = true
			return errInjected
		}
		return nil
	}
}

func openAt(t *testing.T, dir string, minRetain int) *Store {
	t.Helper()
	s, err := Open(dir, Options{MinRetain: minRetain})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func resultOf(id string) func() []byte {
	return func() []byte { return []byte("result:" + id) }
}

func TestDuplicateSameContentReturnsOriginal(t *testing.T) {
	dir := t.TempDir()
	s := openAt(t, dir, 0)
	defer s.Close()

	r1, err := s.Execute("op-1", []byte("payload"), resultOf("op-1"))
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	runs := 0
	r2, err := s.Execute("op-1", []byte("payload"), func() []byte {
		runs++
		return []byte("other")
	})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if string(r1) != string(r2) {
		t.Fatalf("retry returned %q, want original %q", r2, r1)
	}
	if runs != 0 {
		t.Fatalf("business function re-ran %d times on retry", runs)
	}
	if got := len(s.BusinessLog()); got != 1 {
		t.Fatalf("business log has %d records, want 1", got)
	}
}

func TestConflictDifferentContent(t *testing.T) {
	dir := t.TempDir()
	s := openAt(t, dir, 0)

	if _, err := s.Execute("op-1", []byte("a"), resultOf("op-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute("op-1", []byte("b"), resultOf("op-1")); !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
	s.Close()

	// Conflict rule must survive restart and must never overwrite.
	s = openAt(t, dir, 0)
	defer s.Close()
	if _, err := s.Execute("op-1", []byte("b"), resultOf("op-1")); !errors.Is(err, ErrConflict) {
		t.Fatalf("after restart got %v, want ErrConflict", err)
	}
	log := s.BusinessLog()
	if len(log) != 1 || string(log[0].Content) != "a" {
		t.Fatalf("record overwritten or duplicated: %+v", log)
	}
}

func TestConcurrentSameIDPublishesOneRecord(t *testing.T) {
	dir := t.TempDir()
	s := openAt(t, dir, 0)
	defer s.Close()

	const clients = 8
	var wg sync.WaitGroup
	results := make([][]byte, clients)
	var runsMu sync.Mutex
	runs := 0
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := s.Execute("op-1", []byte("payload"), func() []byte {
				runsMu.Lock()
				runs++
				runsMu.Unlock()
				return []byte("result:op-1")
			})
			if err != nil {
				t.Errorf("client %d: %v", i, err)
				return
			}
			results[i] = r
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if string(r) != "result:op-1" {
			t.Fatalf("client %d got %q", i, r)
		}
	}
	if runs != 1 {
		t.Fatalf("business function ran %d times, want 1", runs)
	}
	if got := len(s.BusinessLog()); got != 1 {
		t.Fatalf("business log has %d records, want 1", got)
	}
}

// Crash between record write and result return: after recovery the retry
// must return the original result and there must still be exactly one
// business record.
func TestCrashAtEachCommitPoint(t *testing.T) {
	for _, point := range []string{
		PointBeforeWrite, PointAfterWrite, PointAfterSync, PointBeforeRespond,
	} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			s := openAt(t, dir, 0)
			s.SetFaultHook(armOnce(point))
			_, err := s.Execute("op-1", []byte("payload"), resultOf("op-1"))
			if !errors.Is(err, errInjected) {
				t.Fatalf("got %v, want injected crash", err)
			}
			if _, err := s.Execute("op-2", []byte("x"), resultOf("op-2")); !errors.Is(err, ErrCrashed) {
				t.Fatalf("crashed store accepted work: %v", err)
			}

			s = openAt(t, dir, 0)
			defer s.Close()
			committed := point == PointAfterSync || point == PointBeforeRespond
			log := s.BusinessLog()
			if committed {
				if len(log) != 1 || string(log[0].Result) != "result:op-1" {
					t.Fatalf("committed record lost after crash at %s: %+v", point, log)
				}
				// Retry returns the original result, business fn not re-run.
				r, err := s.Execute("op-1", []byte("payload"), func() []byte {
					t.Fatal("business function re-ran after crash")
					return nil
				})
				if err != nil || string(r) != "result:op-1" {
					t.Fatalf("retry after crash: r=%q err=%v", r, err)
				}
			} else {
				if len(log) != 0 {
					t.Fatalf("uncommitted record survived crash at %s: %+v", point, log)
				}
				// Retry executes anew; still exactly one record.
				if _, err := s.Execute("op-1", []byte("payload"), resultOf("op-1")); err != nil {
					t.Fatal(err)
				}
			}
			if got := len(s.BusinessLog()); got != 1 {
				t.Fatalf("business log has %d records, want exactly 1", got)
			}
		})
	}
}

func TestRestartDoesNotReappend(t *testing.T) {
	dir := t.TempDir()
	s := openAt(t, dir, 0)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("op-%d", i)
		if _, err := s.Execute(id, []byte("p"+id), resultOf(id)); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	walSize := func() int64 {
		fi, err := os.Stat(filepath.Join(dir, walFileName))
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	before := walSize()

	for restart := 0; restart < 3; restart++ {
		s = openAt(t, dir, 0)
		if got := len(s.BusinessLog()); got != 5 {
			t.Fatalf("restart %d: %d records, want 5", restart, got)
		}
		s.Close()
	}
	if after := walSize(); after != before {
		t.Fatalf("restart appended to WAL: %d -> %d bytes", before, after)
	}
}

func TestReclaimRequiresBothWatermarkAndRetention(t *testing.T) {
	dir := t.TempDir()
	const minRetain = 2
	s := openAt(t, dir, minRetain)
	defer s.Close()
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("op-%d", i)
		if _, err := s.Execute(id, []byte("p"), resultOf(id)); err != nil {
			t.Fatal(err)
		}
	}

	// Retention rule alone is not enough: no caller watermark, no reclaim.
	if n, err := s.Reclaim(); err != nil || n != 0 {
		t.Fatalf("reclaim without watermark: n=%d err=%v, want 0", n, err)
	}
	// Caller confirms up to op-4, but the newest MinRetain=2 entries
	// (op-4, op-5) are protected: only op-0..op-3 may go.
	if err := s.ConfirmWatermark("op-4"); err != nil {
		t.Fatal(err)
	}
	n, err := s.Reclaim()
	if err != nil || n != 4 {
		t.Fatalf("reclaim: n=%d err=%v, want 4", n, err)
	}
	if got := s.ReclaimedWatermark(); got != "op-3" {
		t.Fatalf("reclaimed watermark %q, want op-3", got)
	}
	if got := len(s.BusinessLog()); got != 2 {
		t.Fatalf("business log has %d records, want 2 survivors", got)
	}
	// Watermark gate still binds: confirming nothing more, nothing more
	// can be reclaimed.
	if n, _ := s.Reclaim(); n != 0 {
		t.Fatalf("second reclaim dropped %d, want 0", n)
	}
}

func TestExpiredIDRuleIsExplicit(t *testing.T) {
	dir := t.TempDir()
	s := openAt(t, dir, 0)
	defer s.Close()
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("op-%d", i)
		if _, err := s.Execute(id, []byte("p"), resultOf(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ConfirmWatermark("op-1"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Reclaim(); err != nil || n != 2 {
		t.Fatalf("reclaim: n=%d err=%v", n, err)
	}
	// Retry of a reclaimed ID: explicit ErrExpired, never re-executed.
	if _, err := s.Execute("op-0", []byte("p"), resultOf("op-0")); !errors.Is(err, ErrExpired) {
		t.Fatalf("reclaimed retry: got %v, want ErrExpired", err)
	}
	// A brand-new ID below the watermark is also expired.
	if _, err := s.Execute("op-1", []byte("p"), resultOf("op-1")); !errors.Is(err, ErrExpired) {
		t.Fatalf("new ID below watermark: got %v, want ErrExpired", err)
	}
	// Live entries still dedup normally.
	r, err := s.Execute("op-2", []byte("p"), func() []byte {
		t.Fatal("re-ran live entry")
		return nil
	})
	if err != nil || string(r) != "result:op-2" {
		t.Fatalf("live retry: r=%q err=%v", r, err)
	}
}

func TestOutOfOrderIDRejected(t *testing.T) {
	dir := t.TempDir()
	s := openAt(t, dir, 0)
	defer s.Close()
	if _, err := s.Execute("op-5", []byte("p"), resultOf("op-5")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute("op-4", []byte("p"), resultOf("op-4")); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("got %v, want ErrOutOfOrder", err)
	}
}

func TestReclaimCrashRecovery(t *testing.T) {
	setup := func(t *testing.T, dir string) *Store {
		s := openAt(t, dir, 0)
		for i := 0; i < 4; i++ {
			id := fmt.Sprintf("op-%d", i)
			if _, err := s.Execute(id, []byte("p"), resultOf(id)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.ConfirmWatermark("op-2"); err != nil {
			t.Fatal(err)
		}
		return s
	}

	t.Run(PointSnapBeforeRename, func(t *testing.T) {
		dir := t.TempDir()
		s := setup(t, dir)
		s.SetFaultHook(armOnce(PointSnapBeforeRename))
		if _, err := s.Reclaim(); !errors.Is(err, errInjected) {
			t.Fatalf("got %v, want injected crash", err)
		}
		s = openAt(t, dir, 0)
		defer s.Close()
		// Crash before rename: reclaim did not happen, nothing lost.
		if got := len(s.BusinessLog()); got != 4 {
			t.Fatalf("%d records after recovery, want 4", got)
		}
		if got := s.ReclaimedWatermark(); got != "" {
			t.Fatalf("reclaimed watermark %q, want empty", got)
		}
	})

	for _, point := range []string{PointSnapAfterRename, PointSnapAfterTruncate} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			s := setup(t, dir)
			s.SetFaultHook(armOnce(point))
			if _, err := s.Reclaim(); !errors.Is(err, errInjected) {
				t.Fatalf("got %v, want injected crash", err)
			}
			s = openAt(t, dir, 0)
			defer s.Close()
			// Crash after the snapshot was renamed: the reclaim is
			// durable, replay of the stale WAL must be idempotent.
			if got := len(s.BusinessLog()); got != 1 {
				t.Fatalf("%d records after recovery, want 1 survivor", got)
			}
			if got := s.ReclaimedWatermark(); got != "op-2" {
				t.Fatalf("reclaimed watermark %q, want op-2", got)
			}
			if got := s.ReclaimedCount(); got != 3 {
				t.Fatalf("reclaimed count %d, want 3", got)
			}
			if _, err := s.Execute("op-1", []byte("p"), resultOf("op-1")); !errors.Is(err, ErrExpired) {
				t.Fatalf("reclaimed retry: got %v, want ErrExpired", err)
			}
			r, err := s.Execute("op-3", []byte("p"), resultOf("op-3"))
			if err != nil || string(r) != "result:op-3" {
				t.Fatalf("survivor retry: r=%q err=%v", r, err)
			}
		})
	}
}

func TestTornTailTruncated(t *testing.T) {
	dir := t.TempDir()
	s := openAt(t, dir, 0)
	if _, err := s.Execute("op-1", []byte("p"), resultOf("op-1")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	// Simulate a torn write at the tail (power loss mid-append).
	f, err := os.OpenFile(filepath.Join(dir, walFileName), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0xde, 0xad, 0xbe}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s = openAt(t, dir, 0)
	defer s.Close()
	if got := len(s.BusinessLog()); got != 1 {
		t.Fatalf("%d records after torn-tail recovery, want 1", got)
	}
	r, err := s.Execute("op-1", []byte("p"), resultOf("op-1"))
	if err != nil || string(r) != "result:op-1" {
		t.Fatalf("retry after torn tail: r=%q err=%v", r, err)
	}
}

func TestReclaimStateSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s := openAt(t, dir, 1)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("op-%d", i)
		if _, err := s.Execute(id, []byte("p"), resultOf(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ConfirmWatermark("op-4"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Reclaim(); err != nil || n != 4 {
		t.Fatalf("reclaim: n=%d err=%v", n, err)
	}
	s.Close()

	s = openAt(t, dir, 0)
	defer s.Close()
	if got := s.ReclaimedWatermark(); got != "op-3" {
		t.Fatalf("reclaimed watermark %q, want op-3", got)
	}
	if got := s.ConfirmedWatermark(); got != "op-4" {
		t.Fatalf("confirmed watermark %q, want op-4", got)
	}
	if _, err := s.Execute("op-2", []byte("p"), resultOf("op-2")); !errors.Is(err, ErrExpired) {
		t.Fatalf("reclaimed retry after restart: got %v, want ErrExpired", err)
	}
}
